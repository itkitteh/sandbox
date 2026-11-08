package assets

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	embedded "github.com/itkitteh/sandbox/assets"
)

func TestExtractIsIdempotent(t *testing.T) {
	root := t.TempDir()
	fsys := fstest.MapFS{
		"compose.yaml":          {Data: []byte("name: x\n")},
		"gateway/entrypoint.sh": {Data: []byte("#!/bin/sh\n")},
	}
	first, err := Extract(fsys, root)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(first, "marker")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := Extract(fsys, root)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("same content extracted to %s and %s", first, second)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("second Extract rewrote an existing directory")
	}
	fi, err := os.Stat(filepath.Join(first, "gateway", "entrypoint.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("script mode %v is not executable", fi.Mode().Perm())
	}
}

func TestChangedContentGetsNewDirectory(t *testing.T) {
	root := t.TempDir()
	a, err := Extract(fstest.MapFS{"compose.yaml": {Data: []byte("a")}}, root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Extract(fstest.MapFS{"compose.yaml": {Data: []byte("b")}}, root)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("different content shared a directory")
	}
}

func TestEmbeddedStackIsComplete(t *testing.T) {
	dir, err := Extract(embedded.FS, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		"compose.yaml",
		"gateway/Dockerfile", "gateway/entrypoint.sh", "gateway/resolv.conf",
		"gateway/nftables.nft.tmpl", "gateway/sing-box.json.tmpl", "gateway/keyproxy/main.go",
		"image/Dockerfile", "image/entrypoint.sh", "image/bootstrap.sh", "image/opencode.json",
	} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
}
