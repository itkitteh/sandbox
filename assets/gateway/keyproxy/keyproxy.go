package main

// The proxy forwards OpenRouter API calls and adds the real API key, so the
// key never enters the workspace container.

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// PathPrefix is the only path forwarded upstream.
const PathPrefix = "/api/v1/"

const maxKeyBytes = 4096

// ErrNoKey means the key file is missing or empty.
var ErrNoKey = errors.New("no API key stored")

// Proxy is an http.Handler that forwards PathPrefix requests to Upstream.
type Proxy struct {
	Upstream *url.URL
	KeyFile  string
	rp       *httputil.ReverseProxy
}

// New returns a Proxy for upstream that reads its key from keyFile on every request,
// so `sandbox key set` takes effect without a restart.
func New(upstream *url.URL, keyFile string) *Proxy {
	p := &Proxy{Upstream: upstream, KeyFile: keyFile}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(upstream)
			r.Out.Host = upstream.Host
		},
		// Streamed (SSE) completions must not be buffered.
		FlushInterval: -1,
		ErrorLog:      log.New(os.Stderr, "keyproxy: ", log.LstdFlags),
	}
	return p
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, PathPrefix) {
		http.Error(w, "keyproxy: only "+PathPrefix+"* is forwarded", http.StatusNotFound)
		return
	}
	key, err := ReadSecret(p.KeyFile)
	if err != nil {
		msg := "keyproxy: cannot read API key: " + err.Error()
		if errors.Is(err, ErrNoKey) {
			msg = "keyproxy: no OpenRouter key stored; run `sandbox key set openrouter` on the host"
		}
		http.Error(w, msg, http.StatusServiceUnavailable)
		return
	}
	r.Header.Del("Authorization")
	r.Header.Del("Proxy-Authorization")
	r.Header.Set("Authorization", "Bearer "+key)
	p.rp.ServeHTTP(w, r)
}

// ReadSecret reads a small secret file, refusing symlinks and anything that is
// not a regular file. The file is checked before and after opening so it cannot
// be swapped in between.
func ReadSecret(path string) (string, error) {
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNoKey
	}
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(before, after) {
		return "", fmt.Errorf("%s changed while opening", path)
	}
	b, err := io.ReadAll(io.LimitReader(f, maxKeyBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxKeyBytes {
		return "", fmt.Errorf("%s is larger than %d bytes", path, maxKeyBytes)
	}
	key := strings.TrimSpace(string(b))
	if key == "" {
		return "", ErrNoKey
	}
	if strings.ContainsAny(key, "\r\n") {
		return "", fmt.Errorf("%s holds more than one line", path)
	}
	return key, nil
}

// StoreSecret writes one line from r to path with mode 0600. It writes a temp
// file and renames it, so a concurrent reader never sees a partial key.
func StoreSecret(path string, r io.Reader) error {
	b, err := io.ReadAll(io.LimitReader(r, maxKeyBytes+1))
	if err != nil {
		return err
	}
	if len(b) > maxKeyBytes {
		return fmt.Errorf("key is larger than %d bytes", maxKeyBytes)
	}
	key := strings.TrimSpace(string(b))
	if key == "" {
		return errors.New("empty key on stdin")
	}
	if strings.ContainsAny(key, "\r\n") {
		return errors.New("key on stdin spans more than one line")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".key-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(key + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
