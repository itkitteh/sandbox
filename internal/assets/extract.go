// Package assets writes the embedded compose stack to disk for docker compose.
package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Hash returns a short content hash over every file path and body in fsys.
func Hash(fsys fs.FS) (string, error) {
	var files []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, p := range files {
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// Extract writes fsys to root/<hash> and returns that directory. An existing
// directory is reused as is, since its name is the hash of its content.
func Extract(fsys fs.FS, root string) (string, error) {
	sum, err := Hash(fsys)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(root, sum)
	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(root, ".extract-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(tmp, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, fileMode(p))
	})
	if err != nil {
		return "", err
	}
	// Another process may have won the race; its copy has the same content.
	if err := os.Rename(tmp, dest); err != nil {
		if _, statErr := os.Stat(dest); statErr == nil {
			return dest, nil
		}
		return "", err
	}
	return dest, nil
}

// embed.FS drops file modes, so scripts get their exec bit back by extension.
func fileMode(p string) fs.FileMode {
	if strings.HasSuffix(path.Base(p), ".sh") {
		return 0o755
	}
	return 0o644
}
