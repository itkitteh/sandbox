// Package gitguard flags repo settings that would leak your host identity into
// the sandbox, and git files the sandbox changed that could run on the host.
package gitguard

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Warnings lists repo-local settings in dir that matter inside the sandbox.
// A dir that is not a git repo gets no warnings.
func Warnings(dir string) []string {
	var out []string
	if v := localConfig(dir, "user.email"); v != "" {
		out = append(out, "repo sets user.email="+v+" in .git/config; the sandbox overrides it with GIT_EMAIL, but your host git will use it")
	}
	if v := localConfig(dir, "credential.helper"); v != "" {
		out = append(out, "repo sets credential.helper="+v+" in .git/config; it will not find host credentials inside the sandbox")
	}
	if v := localConfig(dir, "core.hooksPath"); v != "" {
		out = append(out, "repo sets core.hooksPath="+v+"; hooks there run on the host too")
	}
	return out
}

func localConfig(dir, key string) string {
	out, err := exec.Command("git", "-C", dir, "config", "--local", "--get", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Snapshot is a set of file hashes, keyed by path relative to the repo's .git dir.
type Snapshot map[string]string

// Take hashes .git/config and every file under .git/hooks. Anything written
// there runs on the host the next time host git touches the repo.
func Take(dir string) Snapshot {
	s := Snapshot{}
	// os.Root keeps reads inside .git even if the sandbox plants symlinks there.
	root, err := os.OpenRoot(filepath.Join(dir, ".git"))
	if err != nil {
		return s
	}
	defer root.Close()
	if b, err := root.ReadFile("config"); err == nil {
		s["config"] = sum(b)
	}
	_ = fs.WalkDir(root.FS(), "hooks", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			target, _ := root.Readlink(p)
			s[p] = sum([]byte("symlink:" + target))
			return nil
		}
		if b, err := root.ReadFile(p); err == nil {
			s[p] = sum(b)
		}
		return nil
	})
	return s
}

// Changed lists paths added, removed, or modified between before and after.
func Changed(before, after Snapshot) []string {
	var out []string
	for p, h := range after {
		if before[p] != h {
			out = append(out, ".git/"+p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			out = append(out, ".git/"+p+" (removed)")
		}
	}
	sort.Strings(out)
	return out
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
