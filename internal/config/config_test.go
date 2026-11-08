package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	c, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	c["GIT_NAME"] = "Sandbox Person"
	c["GIT_EMAIL"] = "1+x@users.noreply.github.com"
	c["CUSTOM_EXTRA"] = "kept"
	path := filepath.Join(t.TempDir(), "sandbox", "config.env")

	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range c {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	if err := got.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestLoadAcceptsHandEditedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.env")
	os.WriteFile(path, []byte("# comment\n\nGIT_NAME=Plain Value\nGIT_EMAIL=\"dq@example.com\"\n"), 0o600)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c["GIT_NAME"] != "Plain Value" || c["GIT_EMAIL"] != "dq@example.com" {
		t.Errorf("got %v", c)
	}
}

func TestValidateNamesEveryMissingKey(t *testing.T) {
	err := Config{"PROXY_HOST": "10.0.0.1"}.Validate()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, k := range []string{"PROXY_PORT", "GIT_NAME", "GIT_EMAIL", "EDITOR_PASSWORD"} {
		if !strings.Contains(err.Error(), k) {
			t.Errorf("error %q does not name %s", err, k)
		}
	}
}

func TestSaveRejectsQuotes(t *testing.T) {
	c := Config{"GIT_NAME": "O'Brien"}
	if err := c.Save(filepath.Join(t.TempDir(), "c.env")); err == nil {
		t.Error("expected an error for a value with a quote")
	}
}
