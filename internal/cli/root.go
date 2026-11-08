// Package cli implements the sandbox command.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	embedded "github.com/itkitteh/sandbox/assets"
	"github.com/itkitteh/sandbox/internal/assets"
	"github.com/itkitteh/sandbox/internal/compose"
	"github.com/itkitteh/sandbox/internal/config"
	"github.com/itkitteh/sandbox/internal/gitguard"
	"github.com/itkitteh/sandbox/internal/mount"
)

// ExitError carries a child process's exit code up to main.
type ExitError struct{ Code int }

func (e ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

type app struct {
	configPath string
	project    string
	tag        string
	cfg        config.Config
	p          *compose.Project
}

// New returns the root command.
func New() *cobra.Command {
	a := &app{}
	root := &cobra.Command{
		Use:   "sandbox [DIR] [-- CMD...]",
		Short: "Isolated dev shell whose traffic all goes through the router's SOCKS proxy",
		Long: `Opens a shell (or runs CMD) in the sandbox workspace. With DIR, that one
folder is mounted read-write at /work/<name>. Without it, you get the home
volume only. All traffic leaves through the gateway's kill-switched tunnel.`,
		Example: `  sandbox                          shell in the home volume
  sandbox ~/Code/project           shell in /work/project
  sandbox ~/Code/project -- opencode`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version(),
		RunE:          a.runSession,
	}
	root.PersistentFlags().StringVar(&a.configPath, "config", os.Getenv("SANDBOX_CONFIG"), "config file (default ~/.config/sandbox/config.env)")
	root.PersistentFlags().StringVar(&a.project, "project", envOr("SANDBOX_PROJECT", "sandbox"), "compose project name; separate names get separate volumes")
	root.AddCommand(a.initCmd(), a.editCmd(), a.keyCmd(), a.checkCmd(),
		a.upCmd(), a.downCmd(), a.rebuildCmd(), a.logsCmd(), a.nukeCmd())
	return root
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "dev"
}

func (a *app) resolvedConfigPath() (string, error) {
	if a.configPath != "" {
		return a.configPath, nil
	}
	return config.DefaultPath()
}

// load reads and validates the config and extracts the embedded stack.
func (a *app) load() error {
	path, err := a.resolvedConfigPath()
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no config at %s; run `sandbox init` first", path)
	}
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("%s: %v; edit it or run `sandbox init --force`", path, err)
	}
	dir, err := assets.Extract(embedded.FS, filepath.Join(dataDir(), "assets"))
	if err != nil {
		return fmt.Errorf("extract compose stack: %w", err)
	}
	a.cfg = cfg
	a.p = compose.New(a.project, dir, path)
	// Tagging images by asset hash makes a binary with changed assets build
	// fresh images instead of reusing stale ones.
	a.tag = filepath.Base(dir)
	a.p.Env = []string{"SANDBOX_IMAGE_TAG=" + a.tag}
	return a.ensureImages()
}

// ensureImages builds missing images in the foreground, so a first run shows
// build progress instead of a silent wait inside `compose run`.
func (a *app) ensureImages() error {
	for _, img := range []string{"sandbox-gateway", "sandbox-workspace"} {
		if exec.Command("docker", "image", "inspect", img+":"+a.tag).Run() != nil {
			warnf("building images for this version (first run takes a few minutes)...")
			return a.p.Run("--profile", "editor", "build")
		}
	}
	return nil
}

func dataDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "sandbox")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "sandbox")
}

func isTTY(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

func warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "sandbox: "+format+"\n", args...)
}

// splitDash splits args at "--" into the optional DIR and the command.
func splitDash(cmd *cobra.Command, args []string) (dir string, rest []string, err error) {
	pre := args
	if at := cmd.ArgsLenAtDash(); at >= 0 {
		pre, rest = args[:at], args[at:]
	}
	switch len(pre) {
	case 0:
	case 1:
		dir = pre[0]
	default:
		return "", nil, fmt.Errorf("expected at most one DIR before --, got %q", pre)
	}
	return dir, rest, nil
}

// resolveMount validates dir and prints git warnings. A zero Mount means no dir.
func resolveMount(dir string) (mount.Mount, error) {
	if dir == "" {
		return mount.Mount{}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return mount.Mount{}, err
	}
	m, err := mount.Resolve(dir, home)
	if err != nil {
		return mount.Mount{}, err
	}
	for _, w := range gitguard.Warnings(m.Host) {
		warnf("warning: %s", w)
	}
	return m, nil
}

func mountArgs(m mount.Mount) []string {
	if m.Host == "" {
		return nil
	}
	return []string{"--volume", m.Host + ":" + m.Container, "--workdir", m.Container}
}

func (a *app) runSession(cmd *cobra.Command, args []string) error {
	dir, rest, err := splitDash(cmd, args)
	if err != nil {
		return err
	}
	m, err := resolveMount(dir)
	if err != nil {
		return err
	}
	if err := a.load(); err != nil {
		return err
	}

	runArgs := []string{"--progress", "quiet", "run", "--rm"}
	if !isTTY(os.Stdin) || !isTTY(os.Stdout) {
		runArgs = append(runArgs, "-T")
	}
	runArgs = append(runArgs, mountArgs(m)...)
	runArgs = append(runArgs, "workspace")
	runArgs = append(runArgs, rest...)

	var before gitguard.Snapshot
	if m.Host != "" {
		before = gitguard.Take(m.Host)
	}
	runErr := a.p.Run(runArgs...)
	if m.Host != "" {
		if changed := gitguard.Changed(before, gitguard.Take(m.Host)); len(changed) > 0 {
			warnf("warning: this session changed files that host git runs or reads:")
			for _, c := range changed {
				fmt.Fprintf(os.Stderr, "  %s/%s\n", m.Host, c)
			}
			warnf("review them before running git on the host")
		}
	}
	return exitErr(runErr)
}

// exitErr turns a child's non-zero exit into ExitError so main can mirror it.
func exitErr(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ExitError{Code: ee.ExitCode()}
	}
	return err
}

func readAllTrim(r io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, 8192))
	return string(b), err
}
