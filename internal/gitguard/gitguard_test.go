package gitguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	return dir
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestWarnsAboutLocalIdentityAndHelper(t *testing.T) {
	dir := gitRepo(t)
	git(t, dir, "config", "user.email", "me@personal.example")
	git(t, dir, "config", "credential.helper", "osxkeychain")

	w := strings.Join(Warnings(dir), "\n")
	if !strings.Contains(w, "me@personal.example") || !strings.Contains(w, "osxkeychain") {
		t.Errorf("warnings missing identity or helper:\n%s", w)
	}
}

func TestCleanRepoAndNonRepoHaveNoWarnings(t *testing.T) {
	if w := Warnings(gitRepo(t)); len(w) != 0 {
		t.Errorf("clean repo: %v", w)
	}
	if w := Warnings(t.TempDir()); len(w) != 0 {
		t.Errorf("non-repo: %v", w)
	}
}

func TestDetectsHookAndConfigChanges(t *testing.T) {
	dir := gitRepo(t)
	before := Take(dir)
	if len(Changed(before, Take(dir))) != 0 {
		t.Fatal("no-op reported changes")
	}

	hook := filepath.Join(dir, ".git", "hooks", "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ncurl evil\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "config", "core.fsmonitor", "/tmp/x")

	got := strings.Join(Changed(before, Take(dir)), ",")
	if !strings.Contains(got, ".git/hooks/post-checkout") || !strings.Contains(got, ".git/config") {
		t.Errorf("changes = %s", got)
	}
}

func TestDetectsRemovedHook(t *testing.T) {
	dir := gitRepo(t)
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	os.WriteFile(hook, []byte("#!/bin/sh\n"), 0o755)
	before := Take(dir)
	os.Remove(hook)
	if got := Changed(before, Take(dir)); len(got) != 1 || !strings.Contains(got[0], "removed") {
		t.Errorf("changes = %v", got)
	}
}
