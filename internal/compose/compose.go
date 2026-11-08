// Package compose runs docker compose against the extracted sandbox stack.
package compose

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// Project runs `docker compose` with a fixed project name, directory and env file.
type Project struct {
	Name    string
	Dir     string
	EnvFile string
	// Env is added to the process environment; compose reads it before the env file.
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// New returns a Project wired to the process's stdio.
func New(name, dir, envFile string) *Project {
	return &Project{Name: name, Dir: dir, EnvFile: envFile, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
}

// Args returns the full docker argv for a compose subcommand.
func (p *Project) Args(args ...string) []string {
	base := []string{
		"compose",
		"--project-name", p.Name,
		"--project-directory", p.Dir,
		"--file", filepath.Join(p.Dir, "compose.yaml"),
		"--env-file", p.EnvFile,
	}
	return append(base, args...)
}

// Command returns an unstarted docker compose command with stdio attached.
func (p *Project) Command(args ...string) *exec.Cmd {
	cmd := exec.Command("docker", p.Args(args...)...)
	cmd.Env = append(os.Environ(), p.Env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = p.Stdin, p.Stdout, p.Stderr
	return cmd
}

// Run runs a compose subcommand with stdio attached.
func (p *Project) Run(args ...string) error {
	return p.Command(args...).Run()
}

// Output runs a compose subcommand and returns stdout. Stderr goes to the
// combined buffer too, so callers can show why a probe failed.
func (p *Project) Output(stdin io.Reader, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.Command("docker", p.Args(args...)...)
	cmd.Env = append(os.Environ(), p.Env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}
