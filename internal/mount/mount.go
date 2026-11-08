// Package mount validates a host folder before it is bind-mounted into the
// workspace, and picks its path inside the container.
package mount

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Root is where mounted folders appear inside the container.
const Root = "/work"

// Sensitive paths under $HOME. A folder inside one, or containing one, is refused.
var Sensitive = []string{".ssh", ".gnupg", ".config", ".claude", ".docker", ".aws", ".kube", "Library"}

// Mount is a validated host folder and its path in the container.
type Mount struct {
	Host      string
	Container string
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Resolve makes dir absolute, follows symlinks, and checks it against the deny list.
func Resolve(dir, home string) (Mount, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Mount{}, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Mount{}, err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return Mount{}, err
	}
	if !fi.IsDir() {
		return Mount{}, fmt.Errorf("%s is not a directory", dir)
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		realHome = filepath.Clean(home)
	}
	if real == "/" || real == realHome {
		return Mount{}, fmt.Errorf("refusing to mount %s: pick a project folder instead", real)
	}
	if within(realHome, real) {
		return Mount{}, fmt.Errorf("refusing to mount %s: it contains your home directory", real)
	}
	for _, s := range Sensitive {
		sp := filepath.Join(realHome, s)
		if within(real, sp) || within(sp, real) {
			return Mount{}, fmt.Errorf("refusing to mount %s: it overlaps %s, which holds credentials", real, sp)
		}
	}
	name := unsafeChars.ReplaceAllString(filepath.Base(real), "_")
	name = strings.Trim(name, ".")
	if name == "" {
		name = "project"
	}
	return Mount{Host: real, Container: Root + "/" + name}, nil
}

// within reports whether child is parent or inside it.
func within(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
