package mount

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHome builds a home directory with a project and credential dirs.
func fakeHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"Code/project", ".ssh", ".config/gh", "Code/odd name!"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestAcceptsProjectFolder(t *testing.T) {
	home := fakeHome(t)
	m, err := Resolve(filepath.Join(home, "Code/project"), home)
	if err != nil {
		t.Fatal(err)
	}
	if m.Host != filepath.Join(home, "Code/project") || m.Container != "/work/project" {
		t.Errorf("got %+v", m)
	}
}

func TestRelativePathIsResolved(t *testing.T) {
	home := fakeHome(t)
	t.Chdir(filepath.Join(home, "Code"))
	m, err := Resolve("project", home)
	if err != nil {
		t.Fatal(err)
	}
	if m.Host != filepath.Join(home, "Code/project") {
		t.Errorf("got %+v", m)
	}
}

func TestRefusesDangerousTargets(t *testing.T) {
	home := fakeHome(t)
	for name, dir := range map[string]string{
		"home":           home,
		"root":           "/",
		"parent of home": filepath.Dir(home),
		"ssh":            filepath.Join(home, ".ssh"),
		"inside config":  filepath.Join(home, ".config/gh"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Resolve(dir, home); err == nil {
				t.Errorf("%s was accepted", dir)
			}
		})
	}
}

func TestSymlinkCannotSmuggleCredentials(t *testing.T) {
	home := fakeHome(t)
	link := filepath.Join(home, "Code/innocent")
	if err := os.Symlink(filepath.Join(home, ".ssh"), link); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve(link, home)
	if err == nil || !strings.Contains(err.Error(), ".ssh") {
		t.Errorf("symlink to ~/.ssh: err = %v", err)
	}
}

func TestContainerNameIsSanitised(t *testing.T) {
	home := fakeHome(t)
	m, err := Resolve(filepath.Join(home, "Code/odd name!"), home)
	if err != nil {
		t.Fatal(err)
	}
	if m.Container != "/work/odd_name_" {
		t.Errorf("got %q", m.Container)
	}
}

func TestRefusesFile(t *testing.T) {
	home := fakeHome(t)
	f := filepath.Join(home, "Code/file.txt")
	os.WriteFile(f, nil, 0o644)
	if _, err := Resolve(f, home); err == nil {
		t.Error("a file was accepted")
	}
}
