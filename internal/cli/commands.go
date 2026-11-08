package cli

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/itkitteh/sandbox/internal/check"
	"github.com/itkitteh/sandbox/internal/config"
)

func (a *app) initCmd() *cobra.Command {
	var name, email, proxy string
	var force bool
	c := &cobra.Command{
		Use:   "init",
		Short: "Write the host config (proxy, git identity, editor password)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := a.resolvedConfigPath()
			if err != nil {
				return err
			}
			cfg, err := config.Defaults()
			if err != nil {
				return err
			}
			if old, err := config.Load(path); err == nil {
				if !force {
					return fmt.Errorf("%s already exists; edit it, or pass --force to fill in missing values", path)
				}
				for k, v := range old {
					cfg[k] = v
				}
			}
			if proxy != "" {
				host, port, ok := strings.Cut(proxy, ":")
				if !ok {
					return fmt.Errorf("--proxy wants HOST:PORT, got %q", proxy)
				}
				cfg["PROXY_HOST"], cfg["PROXY_PORT"] = host, port
				cfg["BUILD_PROXY"] = "socks5h://" + proxy
			}
			if name != "" {
				cfg["GIT_NAME"] = name
			}
			if email != "" {
				cfg["GIT_EMAIL"] = email
			}
			if isTTY(os.Stdin) {
				in := bufio.NewReader(os.Stdin)
				prompt(in, cfg, "GIT_NAME", "Git name for the sandbox account")
				prompt(in, cfg, "GIT_EMAIL", "Git email (use the account's noreply address)")
			}
			if err := cfg.Save(path); err != nil {
				return err
			}
			fmt.Printf("Wrote %s\n", path)
			if err := cfg.Validate(); err != nil {
				fmt.Printf("Still %s; edit the file before running sandbox.\n", err)
			}
			return nil
		},
	}
	c.Flags().StringVar(&name, "git-name", "", "git user.name for the sandbox account")
	c.Flags().StringVar(&email, "git-email", "", "git user.email (the account's noreply address)")
	c.Flags().StringVar(&proxy, "proxy", "", "SOCKS5 proxy as HOST:PORT (default 10.0.0.1:1080)")
	c.Flags().BoolVar(&force, "force", false, "update an existing config, keeping its values unless overridden")
	return c
}

func prompt(in *bufio.Reader, cfg config.Config, key, label string) {
	if cfg[key] != "" {
		return
	}
	fmt.Printf("%s: ", label)
	line, _ := in.ReadString('\n')
	cfg[key] = strings.TrimSpace(line)
}

func (a *app) editCmd() *cobra.Command {
	var stop bool
	c := &cobra.Command{
		Use:   "edit [DIR]",
		Short: "Start code-server on 127.0.0.1 with DIR mounted (replaces a running editor)",
		Long: `Starts code-server inside the sandbox. Open it in a browser profile that is
itself proxied through the router: webviews and extensions can make the
browser fetch remote content directly.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := ""
			if len(args) == 1 {
				dir = args[0]
			}
			m, err := resolveMount(dir)
			if err != nil {
				return err
			}
			if err := a.load(); err != nil {
				return err
			}
			name := a.project + "-editor"
			_ = exec.Command("docker", "rm", "-f", name).Run()
			if stop {
				fmt.Println("Editor stopped.")
				return nil
			}
			open := "/home/dev"
			if m.Container != "" {
				open = m.Container
				warnf("the editor runs detached, so changes to .git/hooks are not reported for it")
			}
			runArgs := append([]string{"run", "-d", "--rm", "--name", name}, mountArgs(m)...)
			runArgs = append(runArgs, "editor", "code-server", "--bind-addr=0.0.0.0:8443",
				"--auth=password", "--disable-telemetry", "--disable-update-check", open)
			if out, err := a.p.Output(nil, runArgs...); err != nil {
				return fmt.Errorf("start editor: %v\n%s", err, out)
			}
			url := "http://127.0.0.1:" + editorPort(a.cfg)
			if err := waitHTTP(url+"/healthz", 60*time.Second); err != nil {
				logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
				return fmt.Errorf("editor did not come up: %v\n%s", err, logs)
			}
			fmt.Printf("Editor: %s\nPassword: %s\nStop it with: sandbox edit --stop\n", url, a.cfg["EDITOR_PASSWORD"])
			return nil
		},
	}
	c.Flags().BoolVar(&stop, "stop", false, "stop the running editor")
	return c
}

func editorPort(cfg config.Config) string {
	if p := cfg["EDITOR_HOST_PORT"]; p != "" {
		return p
	}
	return "8443"
}

func waitHTTP(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url) //nolint:gosec // fixed 127.0.0.1 editor URL
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			err = fmt.Errorf("status %d", resp.StatusCode)
		}
		last = err
		time.Sleep(time.Second)
	}
	return last
}

func (a *app) keyCmd() *cobra.Command {
	key := &cobra.Command{Use: "key", Short: "Manage API keys held by the gateway"}
	key.AddCommand(&cobra.Command{
		Use:   "set openrouter",
		Short: "Store an OpenRouter key in the gateway-only secrets volume (reads stdin)",
		Long: `Reads the key from stdin (or prompts without echo) and stores it in a
volume only the gateway mounts. The workspace never sees it: keyproxy adds it
to requests opencode sends to 127.0.0.1:4000.

  pbpaste | sandbox key set openrouter`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"openrouter"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "openrouter" {
				return fmt.Errorf("unknown provider %q; only openrouter is supported", args[0])
			}
			if err := a.load(); err != nil {
				return err
			}
			var secret string
			if isTTY(os.Stdin) {
				fmt.Fprint(os.Stderr, "OpenRouter API key: ")
				b, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(os.Stderr)
				if err != nil {
					return err
				}
				secret = string(b)
			} else {
				s, err := readAllTrim(os.Stdin)
				if err != nil {
					return err
				}
				secret = s
			}
			if strings.TrimSpace(secret) == "" {
				return errors.New("empty key")
			}
			if err := a.upGateway(); err != nil {
				return err
			}
			out, err := a.p.Output(strings.NewReader(secret),
				"exec", "-T", "-u", "keyproxy", "gateway", "keyproxy", "-store")
			if err != nil {
				return fmt.Errorf("store key: %v\n%s", err, out)
			}
			fmt.Println("Stored. Verify with: sandbox check")
			return nil
		},
	})
	return key
}

func (a *app) upGateway() error {
	return a.p.Run("up", "--detach", "--wait", "gateway")
}

func (a *app) checkCmd() *cobra.Command {
	var opts check.Options
	c := &cobra.Command{
		Use:   "check",
		Short: "Run leak and isolation checks against the live sandbox",
		Long:  "Checks:\n  " + strings.Join(check.Names(), "\n  "),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.load(); err != nil {
				return err
			}
			if err := a.upGateway(); err != nil {
				return err
			}
			failed, err := check.Run(a.p, a.cfg, opts, os.Stdout)
			if err != nil {
				return err
			}
			if failed > 0 {
				return fmt.Errorf("%d check(s) failed", failed)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&opts.Spend, "spend", false, "also run one real opencode prompt (costs a few tokens)")
	c.Flags().BoolVar(&opts.CompareDirect, "compare-direct", false, "also compare against the host's direct IP (shows the echo service both IPs)")
	c.Flags().StringSliceVar(&opts.Skip, "skip", nil, "check names to skip")
	return c
}

func (a *app) upCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Start the gateway (sessions start it on demand too)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := a.load(); err != nil {
				return err
			}
			return a.upGateway()
		},
	}
}

func (a *app) downCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop everything; volumes are kept",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := a.load(); err != nil {
				return err
			}
			return a.p.Run("--profile", "editor", "down", "--remove-orphans")
		},
	}
}

func (a *app) rebuildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rebuild",
		Short: "Rebuild images with fresh base layers, then restart the gateway",
		Long:  "Running sessions lose network when the gateway restarts; start them again afterwards.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := a.load(); err != nil {
				return err
			}
			if err := a.p.Run("--profile", "editor", "build", "--pull"); err != nil {
				return err
			}
			return a.p.Run("up", "--detach", "--wait", "--force-recreate", "gateway")
		},
	}
}

func (a *app) logsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs",
		Short: "Follow gateway logs (sing-box and keyproxy)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := a.load(); err != nil {
				return err
			}
			return a.p.Run("logs", "--follow", "--tail", "100", "gateway")
		},
	}
}

func (a *app) nukeCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "nuke",
		Short: "Delete containers AND volumes: home (gh token, SSH key, opencode data) and secrets",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := a.load(); err != nil {
				return err
			}
			if !yes {
				if !isTTY(os.Stdin) {
					return errors.New("refusing to nuke without --yes when stdin is not a terminal")
				}
				fmt.Printf("This deletes the %q home and secrets volumes. Type the project name to confirm: ", a.project)
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				if strings.TrimSpace(line) != a.project {
					return errors.New("not confirmed")
				}
			}
			return a.p.Run("--profile", "editor", "down", "--volumes", "--remove-orphans")
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return c
}
