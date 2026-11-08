// Package check runs leak and isolation probes against a running sandbox and
// reports PASS, FAIL or SKIP for each.
package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/itkitteh/sandbox/internal/compose"
	"github.com/itkitteh/sandbox/internal/config"
)

// Options tune the run.
type Options struct {
	// CompareDirect also looks up the host's direct IP at the same echo service.
	// That pairs your home IP with the proxy's exit IP there, so it is opt-in.
	CompareDirect bool
	// Spend runs one real opencode prompt, which costs a few tokens.
	Spend bool
	// Skip names checks to skip.
	Skip []string
}

// errSkip marks a check that could not apply, as opposed to one that failed.
type errSkip struct{ reason string }

func (e errSkip) Error() string { return e.reason }

type env struct {
	p     *compose.Project
	cfg   config.Config
	opts  Options
	probe string
	// egress is the workspace's exit IP, set by checkEgress.
	egress string
}

type probe struct {
	name string
	run  func(*env) error
}

var probes = []probe{
	{"workspace reaches the internet", checkEgress},
	{"exit IP is in EXPECTED_EGRESS", checkExpectedEgress},
	{"exit IP differs from the host's direct IP (--compare-direct)", checkDirectDiffers},
	{"kill switch blocks direct egress", checkKillSwitch},
	{"DNS resolves only through the tunnel", checkDNS},
	{"LAN, router and host are unreachable", checkUnreachable},
	{"no host credentials, sockets or capabilities", checkHostLeaks},
	{"commits use the sandbox identity in UTC", checkIdentity},
	{"keyproxy reaches OpenRouter and the key stays out of the workspace", checkKeyproxy},
	{"opencode is wired to keyproxy and knows its model", checkOpencodeConfig},
	{"opencode completes a prompt (--spend)", checkOpencode},
	{"editor answers on localhost", checkEditor},
}

// Run starts a probe container, runs every check, and returns the number of failures.
func Run(p *compose.Project, cfg config.Config, opts Options, out io.Writer) (int, error) {
	e := &env{p: p, cfg: cfg, opts: opts, probe: p.Name + "-check"}
	if err := e.startProbe(); err != nil {
		return 0, err
	}
	defer func() { _ = exec.Command("docker", "rm", "-f", e.probe).Run() }()

	failed := 0
	for _, pr := range probes {
		if contains(opts.Skip, pr.name) {
			fmt.Fprintf(out, "SKIP  %s (--skip)\n", pr.name)
			continue
		}
		err := pr.run(e)
		var skip errSkip
		switch {
		case err == nil:
			fmt.Fprintf(out, "PASS  %s\n", pr.name)
		case errors.As(err, &skip):
			fmt.Fprintf(out, "SKIP  %s: %s\n", pr.name, skip.reason)
		default:
			failed++
			fmt.Fprintf(out, "FAIL  %s\n      %s\n", pr.name, strings.ReplaceAll(err.Error(), "\n", "\n      "))
		}
	}
	return failed, nil
}

// Names lists the checks, for --skip help.
func Names() []string {
	var n []string
	for _, p := range probes {
		n = append(n, p.name)
	}
	return n
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), s) {
			return true
		}
	}
	return false
}

func (e *env) startProbe() error {
	_ = exec.Command("docker", "rm", "-f", e.probe).Run()
	if out, err := e.p.Output(nil, "run", "-d", "--name", e.probe, "workspace", "sleep", "900"); err != nil {
		return fmt.Errorf("start probe container: %v\n%s", err, out)
	}
	// The entrypoint runs first-run setup before exec'ing sleep.
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if _, code := e.ws("pgrep -x sleep"); code == 0 {
			return nil
		}
		state, _ := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", e.probe).Output()
		if strings.TrimSpace(string(state)) == "false" {
			logs, _ := exec.Command("docker", "logs", e.probe).CombinedOutput()
			return fmt.Errorf("probe container exited during startup:\n%s", logs)
		}
		time.Sleep(time.Second)
	}
	return errors.New("probe container did not finish startup in 3 minutes")
}

// ws runs a shell snippet in the probe workspace container.
func (e *env) ws(script string) (string, int) {
	return run(exec.Command("docker", "exec", e.probe, "sh", "-c", script))
}

// gw runs a shell snippet in the gateway as root.
func (e *env) gw(script string) (string, int) {
	cmd := exec.Command("docker", e.p.Args("exec", "-T", "gateway", "sh", "-c", script)...)
	cmd.Env = append(os.Environ(), e.p.Env...)
	return run(cmd)
}

func host(name string, args ...string) (string, int) {
	return run(exec.Command(name, args...))
}

func run(cmd *exec.Cmd) (string, int) {
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	}
	return strings.TrimSpace(string(out)), code
}

func (e *env) proxy() string {
	return net.JoinHostPort(e.cfg["PROXY_HOST"], e.cfg["PROXY_PORT"])
}

const ipEcho = "https://api.ipify.org"

// The echo service only ever sees the workspace, which exits through the
// proxy. Asking it from the host too would pair your home IP with the proxy's.
func checkEgress(e *env) error {
	ip, code := e.ws("curl -fsS -m 15 " + ipEcho)
	if code != 0 || net.ParseIP(ip) == nil {
		return fmt.Errorf("no egress from the workspace: %s", ip)
	}
	e.egress = ip
	return nil
}

func checkExpectedEgress(e *env) error {
	if e.egress == "" {
		return errors.New("no exit IP to compare (see the check above)")
	}
	spec := e.cfg["EXPECTED_EGRESS"]
	if strings.TrimSpace(spec) == "" {
		return errSkip{fmt.Sprintf("workspace exits from %s; once you have confirmed that is the proxy's exit, "+
			"set EXPECTED_EGRESS='%s' in the config to verify it on every run", e.egress, e.egress)}
	}
	ok, err := MatchEgress(e.egress, spec)
	if err != nil {
		return fmt.Errorf("EXPECTED_EGRESS: %w", err)
	}
	if !ok {
		return fmt.Errorf("workspace exits from %s, which is not in EXPECTED_EGRESS (%s); is the proxy exiting directly?", e.egress, spec)
	}
	return nil
}

func checkDirectDiffers(e *env) error {
	if !e.opts.CompareDirect {
		return errSkip{"opt-in: it shows the echo service your home IP next to the proxy's"}
	}
	if e.egress == "" {
		return errors.New("no exit IP to compare (see the check above)")
	}
	direct, code := host("curl", "-fsS", "-m", "15", ipEcho)
	if code != 0 {
		return fmt.Errorf("direct lookup from the host failed: %s", direct)
	}
	if direct == e.egress {
		return fmt.Errorf("workspace exit IP %s equals the host's direct IP; is the proxy actually tunnelling?", e.egress)
	}
	return nil
}

// MatchEgress reports whether ip matches any entry of spec, a comma- or
// space-separated list of IPs and CIDR ranges.
func MatchEgress(ip, spec string) (bool, error) {
	addr := net.ParseIP(ip)
	if addr == nil {
		return false, fmt.Errorf("%q is not an IP", ip)
	}
	entries := strings.FieldsFunc(spec, func(r rune) bool { return r == ',' || r == ' ' })
	if len(entries) == 0 {
		return false, errors.New("empty list")
	}
	for _, entry := range entries {
		if strings.Contains(entry, "/") {
			_, n, err := net.ParseCIDR(entry)
			if err != nil {
				return false, fmt.Errorf("bad range %q", entry)
			}
			if n.Contains(addr) {
				return true, nil
			}
			continue
		}
		want := net.ParseIP(entry)
		if want == nil {
			return false, fmt.Errorf("bad IP %q", entry)
		}
		if want.Equal(addr) {
			return true, nil
		}
	}
	return false, nil
}

func checkKillSwitch(e *env) error {
	if out, code := e.gw("curl -sS -m 5 --interface eth0 -o /dev/null https://1.1.1.1"); code == 0 {
		return fmt.Errorf("direct connection on eth0 succeeded: %s", out)
	}
	out, code := e.gw("curl -fsS -m 15 --interface eth0 --socks5-hostname " + e.proxy() + " " + ipEcho)
	if code != 0 {
		return fmt.Errorf("the proxy itself is unreachable on eth0: %s", out)
	}
	return nil
}

func checkDNS(e *env) error {
	if out, code := e.ws("getent hosts example.com"); code != 0 {
		return fmt.Errorf("example.com does not resolve: %s", out)
	}
	if out, code := e.ws("dig +short +time=2 +tries=1 @127.0.0.11 example.com"); code == 0 && out != "" {
		return fmt.Errorf("the Docker resolver at 127.0.0.11 answered (%s); it resolves outside the proxy", out)
	}
	if out, _ := e.ws("cat /etc/resolv.conf"); !strings.Contains(out, "nameserver 172.19.0.2") {
		return fmt.Errorf("/etc/resolv.conf does not point at the tunnel:\n%s", out)
	}
	return nil
}

func checkUnreachable(e *env) error {
	subnetGW := gatewayIP(e.cfg["SANDBOX_SUBNET"])
	targets := []string{
		"http://" + net.JoinHostPort(e.cfg["PROXY_HOST"], "80"),
		"telnet://host.orb.internal:22",
		"telnet://host.docker.internal:22",
		"-6 https://[2606:4700:4700::1111]",
	}
	if subnetGW != "" {
		targets = append(targets, "telnet://"+net.JoinHostPort(subnetGW, "22"))
	}
	var reached []string
	for _, t := range targets {
		if _, code := e.ws("curl -sS -m 4 -o /dev/null " + t + " </dev/null"); code == 0 {
			reached = append(reached, t)
		}
	}
	if len(reached) > 0 {
		return fmt.Errorf("reachable from the workspace: %s", strings.Join(reached, ", "))
	}
	return nil
}

// gatewayIP returns the .1 address of an IPv4 /24-style subnet, or "".
func gatewayIP(subnet string) string {
	ip, _, err := net.ParseCIDR(subnet)
	if err != nil || ip.To4() == nil {
		return ""
	}
	ip4 := ip.To4()
	return net.IPv4(ip4[0], ip4[1], ip4[2], 1).String()
}

// allowedMounts are the mount points a plain workspace session should have.
var allowedMounts = map[string]bool{
	"/": true, "/home/dev": true, "/etc/resolv.conf": true, "/etc/hostname": true,
	"/etc/hosts": true, "/sbin/docker-init": true, "/usr/sbin/docker-init": true,
}

func checkHostLeaks(e *env) error {
	var problems []string
	if out, _ := e.ws(`printf %s "${SSH_AUTH_SOCK:-}"`); out != "" {
		problems = append(problems, "SSH_AUTH_SOCK is set: "+out)
	}
	for _, p := range []string{"/run/host-services", "/var/run/docker.sock", "/run/docker.sock", "/var/lib/sandbox-secrets"} {
		if _, code := e.ws("test -e " + p); code == 0 {
			problems = append(problems, p+" exists")
		}
	}
	if out, _ := e.ws("grep CapEff /proc/self/status"); !strings.HasSuffix(out, "0000000000000000") {
		problems = append(problems, "workspace has capabilities: "+out)
	}
	mounts, _ := e.ws("awk '{print $5}' /proc/self/mountinfo")
	for _, m := range strings.Fields(mounts) {
		if allowedMounts[m] || hasAnyPrefix(m, "/proc", "/dev", "/sys") {
			continue
		}
		problems = append(problems, "unexpected mount "+m)
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if s == p || strings.HasPrefix(s, p+"/") {
			return true
		}
	}
	return false
}

func checkIdentity(e *env) error {
	ident, code := e.ws("git var GIT_COMMITTER_IDENT")
	if code != 0 {
		return fmt.Errorf("git var failed: %s", ident)
	}
	if !strings.Contains(ident, "<"+e.cfg["GIT_EMAIL"]+">") || !strings.HasSuffix(ident, " +0000") {
		return fmt.Errorf("committer is %q; want <%s> with +0000", ident, e.cfg["GIT_EMAIL"])
	}
	return nil
}

func checkKeyproxy(e *env) error {
	status, _ := e.ws("curl -sS -m 15 -o /dev/null -w '%{http_code}' http://127.0.0.1:4000/api/v1/key")
	switch status {
	case "200":
	case "503":
		return errors.New("no key stored; run `sandbox key set openrouter`")
	case "401":
		return errors.New("OpenRouter rejected the stored key; set a valid one with `sandbox key set openrouter`")
	default:
		return fmt.Errorf("keyproxy returned %q", status)
	}
	return e.noKeyInEnv()
}

const keyproxyURL = "http://127.0.0.1:4000/api/v1"

// checkOpencodeConfig reads opencode's resolved config, so a system config it
// silently skipped shows up here instead of as a vague error mid-prompt.
func checkOpencodeConfig(e *env) error {
	raw, code := e.ws("$HOME/.opencode/bin/opencode debug config 2>/dev/null")
	if code != 0 {
		return fmt.Errorf("opencode debug config (exit %d): %s", code, raw)
	}
	var cfg struct {
		Share    string `json:"share"`
		Model    string `json:"model"`
		Provider map[string]struct {
			Options struct {
				BaseURL string `json:"baseURL"`
			} `json:"options"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return fmt.Errorf("parse opencode config: %v", err)
	}
	if got := cfg.Provider["openrouter"].Options.BaseURL; got != keyproxyURL {
		return fmt.Errorf("openrouter baseURL is %q, want %s; is /etc/opencode/opencode.json readable?", got, keyproxyURL)
	}
	if cfg.Share != "disabled" {
		return fmt.Errorf("share is %q, want \"disabled\"", cfg.Share)
	}
	provider, model, ok := strings.Cut(cfg.Model, "/")
	if !ok || provider != "openrouter" {
		return errSkip{fmt.Sprintf("model %q does not use keyproxy; not checking it", cfg.Model)}
	}
	models, code := e.ws("$HOME/.opencode/bin/opencode models openrouter 2>&1")
	if code != 0 {
		return fmt.Errorf("opencode models openrouter: %s", models)
	}
	for _, line := range strings.Fields(models) {
		if line == cfg.Model || line == model {
			return nil
		}
	}
	return fmt.Errorf("opencode does not know model %q; pick one from `opencode models openrouter` and set it in ~/.config/opencode/opencode.json", cfg.Model)
}

// noKeyInEnv fails if any project container has an OpenRouter key in its env.
func (e *env) noKeyInEnv() error {
	ids, _ := host("docker", "ps", "-q", "--filter", "label=com.docker.compose.project="+e.p.Name)
	for _, id := range strings.Fields(ids) {
		raw, code := host("docker", "inspect", "--format", "{{json .Config.Env}}", id)
		if code != 0 {
			continue
		}
		var vars []string
		if json.Unmarshal([]byte(raw), &vars) != nil {
			continue
		}
		for _, v := range vars {
			k, val, _ := strings.Cut(v, "=")
			if strings.Contains(strings.ToUpper(k), "OPENROUTER") || strings.HasPrefix(val, "sk-or-") {
				return fmt.Errorf("container %s has %s in its env", id, k)
			}
		}
	}
	return nil
}

func checkOpencode(e *env) error {
	if !e.opts.Spend {
		return errSkip{"pass --spend to run a real prompt"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "exec", e.probe, "sh", "-lc",
		`$HOME/.opencode/bin/opencode run "Reply with exactly the word PONG and nothing else."`)
	out, code := run(cmd)
	if code != 0 || !strings.Contains(out, "PONG") {
		return fmt.Errorf("opencode run (exit %d):\n%s", code, out)
	}
	return nil
}

func checkEditor(e *env) error {
	names, _ := host("docker", "ps", "--format", "{{.Names}}", "--filter", "name=^"+e.p.Name+"-editor$")
	if names == "" {
		return errSkip{"editor is not running (start it with `sandbox edit`)"}
	}
	port := e.cfg["EDITOR_HOST_PORT"]
	if port == "" {
		port = "8443"
	}
	if out, code := host("curl", "-fsS", "-m", "5", "-o", "/dev/null", "http://127.0.0.1:"+port+"/healthz"); code != 0 {
		return fmt.Errorf("http://127.0.0.1:%s/healthz: %s", port, out)
	}
	return nil
}
