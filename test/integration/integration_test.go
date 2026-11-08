//go:build integration

// Package integration drives the real sandbox binary against Docker and a real
// SOCKS proxy. It uses its own project, subnet and editor port, so it does not
// touch your everyday sandbox.
//
//	SANDBOX_IT_PROXY=10.0.0.1:1080 SANDBOX_IT_EXPECTED_EGRESS=<proxy exit IP> \
//	  go test -tags integration ./test/integration/
package integration

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itkitteh/sandbox/internal/config"
)

const (
	project = "sandbox-it"
	email   = "1+sandbox-it@users.noreply.github.com"
)

type harness struct {
	t   *testing.T
	bin string
	env []string
}

func setup(t *testing.T) *harness {
	t.Helper()
	proxy := os.Getenv("SANDBOX_IT_PROXY")
	if proxy == "" {
		proxy = "10.0.0.1:1080"
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "sandbox")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/sandbox")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cfgPath := filepath.Join(dir, "config.env")
	h := &harness{t: t, bin: bin, env: append(os.Environ(),
		"SANDBOX_CONFIG="+cfgPath,
		"SANDBOX_PROJECT="+project,
		"XDG_DATA_HOME="+filepath.Join(dir, "data"),
	)}
	h.ok("init", "--git-name", "Sandbox IT", "--git-email", email, "--proxy", proxy)

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg["EXPECTED_EGRESS"] = os.Getenv("SANDBOX_IT_EXPECTED_EGRESS")
	cfg["EDITOR_HOST_PORT"] = "18443"
	cfg["SANDBOX_SUBNET"] = "172.29.251.0/24"
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.run(nil, "nuke", "--yes") })
	return h
}

func (h *harness) run(stdin []byte, args ...string) (string, error) {
	cmd := exec.Command(h.bin, args...)
	cmd.Env = h.env
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (h *harness) ok(args ...string) string {
	h.t.Helper()
	out, err := h.run(nil, args...)
	if err != nil {
		h.t.Fatalf("sandbox %v: %v\n%s", args, err, out)
	}
	return out
}

func TestSandbox(t *testing.T) {
	h := setup(t)

	t.Run("leak checks pass", func(t *testing.T) {
		out := h.ok("check", "--skip", "keyproxy reaches OpenRouter and the key stays out of the workspace")
		if strings.Contains(out, "FAIL") {
			t.Fatalf("check output:\n%s", out)
		}
	})

	t.Run("folder mount round-trips and identity is overridden", func(t *testing.T) {
		repo := filepath.Join(t.TempDir(), "my-project")
		os.MkdirAll(repo, 0o755)
		os.WriteFile(filepath.Join(repo, "from-host.txt"), []byte("hi"), 0o644)
		for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "me@personal.example"}} {
			if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}

		out := h.ok(repo, "--", "sh", "-c",
			`pwd; cat from-host.txt; echo; echo from-sandbox > from-sandbox.txt; git commit -q --allow-empty -m t && git log -1 --format=%ae`)

		for _, want := range []string{"/work/my-project", "hi", "warning: repo sets user.email=me@personal.example"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if got := lines[len(lines)-1]; got != email {
			t.Errorf("commit author email = %q, want %q", got, email)
		}
		if b, err := os.ReadFile(filepath.Join(repo, "from-sandbox.txt")); err != nil || string(b) != "from-sandbox\n" {
			t.Errorf("file written in the sandbox is missing on the host: %q %v", b, err)
		}
	})

	t.Run("exit code propagates", func(t *testing.T) {
		_, err := h.run(nil, "--", "sh", "-c", "exit 7")
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 7 {
			t.Errorf("err = %v, want exit 7", err)
		}
	})

	t.Run("home directory is refused", func(t *testing.T) {
		home, _ := os.UserHomeDir()
		if out, err := h.run(nil, home, "--", "true"); err == nil {
			t.Errorf("mounting $HOME succeeded:\n%s", out)
		}
	})

	t.Run("stored key never appears in the workspace", func(t *testing.T) {
		b := make([]byte, 12)
		rand.Read(b)
		key := "sk-or-it-" + hex.EncodeToString(b)
		if out, err := h.run([]byte(key+"\n"), "key", "set", "openrouter"); err != nil {
			t.Fatalf("key set: %v\n%s", err, out)
		}
		out := h.ok("--", "sh", "-c",
			"if grep -rqs "+key+" /home /work /etc /tmp /var /run /opt; then echo FOUND; else echo clean; fi")
		if !strings.Contains(out, "clean") {
			t.Errorf("key visible inside the workspace:\n%s", out)
		}
	})
}
