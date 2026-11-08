// Package config reads and writes the host-side sandbox config, an env file
// that is also passed to docker compose as --env-file.
package config

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Keys written by Init, in file order. Other keys in the file are kept.
var Keys = []string{
	"PROXY_HOST", "PROXY_PORT", "BUILD_PROXY",
	"GIT_NAME", "GIT_EMAIL",
	"OPENCODE_MODEL", "OPENCODE_SMALL_MODEL",
	"EDITOR_PASSWORD", "EDITOR_HOST_PORT",
	"SANDBOX_SUBNET",
}

// Required keys must be non-empty before the stack can start.
var Required = []string{"PROXY_HOST", "PROXY_PORT", "GIT_NAME", "GIT_EMAIL", "EDITOR_PASSWORD"}

// Config is the parsed env file.
type Config map[string]string

// DefaultPath is $XDG_CONFIG_HOME/sandbox/config.env, else ~/.config/sandbox/config.env.
func DefaultPath() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "sandbox", "config.env"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "sandbox", "config.env"), nil
}

// Defaults returns a config with every optional key filled in.
func Defaults() (Config, error) {
	pw, err := randomPassword()
	if err != nil {
		return nil, err
	}
	return Config{
		"PROXY_HOST":           "10.0.0.1",
		"PROXY_PORT":           "1080",
		"BUILD_PROXY":          "socks5h://10.0.0.1:1080",
		"OPENCODE_MODEL":       "openrouter/anthropic/claude-sonnet-5.5",
		"OPENCODE_SMALL_MODEL": "openrouter/anthropic/claude-haiku-5.5",
		"EDITOR_PASSWORD":      pw,
		"EDITOR_HOST_PORT":     "8443",
		"SANDBOX_SUBNET":       "172.29.250.0/24",
	}, nil
}

// Load parses an env file of KEY=VALUE lines. Values may be single- or
// double-quoted; blank lines and # comments are skipped.
func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	c := Config{}
	s := bufio.NewScanner(f)
	for n := 1; s.Scan(); n++ {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected KEY=VALUE", path, n)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		c[strings.TrimSpace(k)] = v
	}
	return c, s.Err()
}

// Save writes c to path with mode 0600, Keys first, then any extra keys sorted.
func (c Config) Save(path string) error {
	var b strings.Builder
	b.WriteString("# sandbox config. Passed to docker compose as --env-file.\n")
	b.WriteString("# No account secrets belong here; they live in Docker volumes.\n")
	seen := map[string]bool{}
	write := func(k string) error {
		v := c[k]
		if strings.ContainsAny(v, "'\n\r") {
			return fmt.Errorf("value for %s may not contain quotes or newlines", k)
		}
		fmt.Fprintf(&b, "%s='%s'\n", k, v)
		seen[k] = true
		return nil
	}
	for _, k := range Keys {
		if err := write(k); err != nil {
			return err
		}
	}
	var extra []string
	for k := range c {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		if err := write(k); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// Validate reports every required key that is empty.
func (c Config) Validate() error {
	var missing []string
	for _, k := range Required {
		if strings.TrimSpace(c[k]) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return errors.New("missing " + strings.Join(missing, ", "))
	}
	return nil
}

func randomPassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
