package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type seen struct {
	auth, host, path string
}

func startUpstream(t *testing.T) (*url.URL, *seen) {
	t.Helper()
	s := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.auth, s.host, s.path = r.Header.Get("Authorization"), r.Host, r.URL.Path
		io.WriteString(w, "upstream-ok")
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u, s
}

func writeKey(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "openrouter")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func do(t *testing.T, h http.Handler, path, auth string) *httptest.ResponseRecorder {
	t.Helper()
	front := httptest.NewServer(h)
	t.Cleanup(front.Close)
	req, _ := http.NewRequest(http.MethodGet, front.URL+path, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rec := httptest.NewRecorder()
	rec.Code = resp.StatusCode
	io.Copy(rec.Body, resp.Body)
	return rec
}

func TestReplacesClientAuthWithStoredKey(t *testing.T) {
	up, s := startUpstream(t)
	p := New(up, writeKey(t, "sk-or-real\n"))

	rec := do(t, p, "/api/v1/key", "Bearer client-dummy")

	if rec.Code != http.StatusOK || rec.Body.String() != "upstream-ok" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	if s.auth != "Bearer sk-or-real" {
		t.Errorf("upstream saw Authorization %q", s.auth)
	}
	if s.host != up.Host || s.path != "/api/v1/key" {
		t.Errorf("upstream saw host %q path %q", s.host, s.path)
	}
}

func TestRefusesPathsOutsidePrefix(t *testing.T) {
	up, s := startUpstream(t)
	p := New(up, writeKey(t, "sk-or-real"))

	for _, path := range []string{"/", "/api/v2/x", "/api/v1", "/other"} {
		if rec := do(t, p, path, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", path, rec.Code)
		}
	}
	if s.path != "" {
		t.Errorf("upstream was called for %q", s.path)
	}
}

func TestNoKeyMeansNoUpstreamCall(t *testing.T) {
	dir := t.TempDir()
	target := writeKey(t, "sk-or-real")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"missing": filepath.Join(dir, "absent"),
		"empty":   writeKey(t, "  \n"),
		"symlink": link,
		"dir":     dir,
		"twoline": writeKey(t, "a\nb"),
	}
	for name, keyFile := range cases {
		t.Run(name, func(t *testing.T) {
			up, s := startUpstream(t)
			rec := do(t, New(up, keyFile), "/api/v1/key", "")
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("got %d, want 503", rec.Code)
			}
			if s.path != "" {
				t.Errorf("upstream was called without a usable key")
			}
		})
	}
}

func TestMissingKeyMessageNamesTheFix(t *testing.T) {
	up, _ := startUpstream(t)
	rec := do(t, New(up, filepath.Join(t.TempDir(), "absent")), "/api/v1/key", "")
	if !strings.Contains(rec.Body.String(), "sandbox key set openrouter") {
		t.Errorf("body %q does not name the fix", rec.Body.String())
	}
}

func TestKeyChangeTakesEffectWithoutRestart(t *testing.T) {
	up, s := startUpstream(t)
	keyFile := writeKey(t, "first")
	p := New(up, keyFile)
	do(t, p, "/api/v1/key", "")
	if err := os.WriteFile(keyFile, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	do(t, p, "/api/v1/key", "")
	if s.auth != "Bearer second" {
		t.Errorf("upstream saw %q after key change", s.auth)
	}
}

func TestStoreSecretRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openrouter")
	if err := StoreSecret(path, strings.NewReader("  sk-or-new \n")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	if got, err := ReadSecret(path); err != nil || got != "sk-or-new" {
		t.Errorf("ReadSecret = %q, %v", got, err)
	}
}

func TestStoreSecretRejectsBadInput(t *testing.T) {
	for name, in := range map[string]string{"empty": " \n", "twoline": "a\nb", "huge": strings.Repeat("x", maxKeyBytes+1)} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "openrouter")
			if err := StoreSecret(path, strings.NewReader(in)); err == nil {
				t.Error("expected an error")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Error("key file was written")
			}
		})
	}
}
