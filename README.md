# sandbox

[![CI](https://github.com/itkitteh/sandbox/actions/workflows/ci.yml/badge.svg)](https://github.com/itkitteh/sandbox/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

A dev container for working under a separate identity. All traffic goes
through a SOCKS5 proxy and fails closed. No credentials cross between the
sandbox and your host, in either direction. It ships git, gh, opencode (with
OpenRouter), just, mise, and an optional web editor.

```
 host ── sandbox CLI ── docker compose (OrbStack)
                         ┌──────────── one network namespace ────────────┐
                         │ gateway (NET_ADMIN)       workspace (no caps) │
                         │  nftables kill switch      uid 1000, no sudo  │
                         │  sing-box tun0 ──────────► SOCKS5 proxy       │
                         │  keyproxy 127.0.0.1:4000   opencode, gh, git  │
                         └───────────────────────────────────────────────┘
```

## Setup

Requires Docker (tested on OrbStack) and Go 1.27.

```sh
just install                 # builds ~/.local/bin/sandbox
sandbox init                 # writes ~/.config/sandbox/config.env, asks for git name/email
pbpaste | sandbox key set openrouter
sandbox check                # leak checks; the first run builds the images
```

In the first session, log in to the separate GitHub account and register the
SSH key the sandbox generated:

```sh
sandbox
gh auth login && gh auth setup-git
sandbox-keyinfo              # prints the key to add as an auth + signing key
```

Do the `gh` device-flow login in a browser profile that isn't signed in to
your main GitHub account.

## Usage

```sh
sandbox                              # shell in the home volume
sandbox ~/Code/project               # shell in /work/project, that folder mounted read-write
sandbox ~/Code/project -- opencode   # run a command instead of a shell
sandbox edit ~/Code/project          # code-server on http://127.0.0.1:8443
sandbox edit --stop
sandbox down | up | rebuild | logs | nuke
```

Each `sandbox DIR` is a throwaway container that shares the gateway's network
and the `home` volume. You can run several at once on different folders.

## How it works

- **Routing.** The workspace shares the gateway's network namespace. The
  gateway's nftables output policy is drop. It allows only `tun0`, loopback,
  and the proxy's own `HOST:PORT`. sing-box sends everything on `tun0` to the
  proxy, and rejects private and LAN destinations. If sing-box dies, traffic
  stops rather than leaving directly.
- **DNS.** Queries go to sing-box, which resolves over DoH through the proxy.
  Docker's resolver (127.0.0.11) is blocked, because it resolves from the host.
- **Credentials.**
  - Nothing from `$HOME` is mounted. That includes the SSH agent, gitconfig
    and the Docker socket.
  - The gh token, the SSH key and opencode state live in the `home` volume.
  - The OpenRouter key lives in a `secrets` volume that only the gateway
    mounts. keyproxy adds the key to opencode's requests, so a prompt-injected
    agent can spend credits but can't read the key.
- **Identity.**
  - `GIT_AUTHOR_*` and `GIT_COMMITTER_*` come from the config and override a
    repo's own `user.email`.
  - Timezone is UTC, and hostname and user are `dev`.
  - A mounted folder appears at `/work/<name>`, so your host path never shows
    up in tool state.

## Limits and hazards

- **A mounted folder is a two-way channel.** Anything the sandbox writes there
  can run on your host: `.git/hooks`, `.envrc`, editor tasks, build scripts. The
  CLI warns when `.git/hooks` or `.git/config` changed during a session.
  `$HOME` and credential folders are refused.
- **Accounts outside this design can still link to you.** OpenRouter billing
  is one example, along with anything you type into the sandbox.
- **Builds are best-effort proxied.** apt and curl downloads during an image
  build use `BUILD_PROXY`. Base image pulls and apk go through OrbStack from
  your host's IP. They're anonymous public downloads, but they aren't
  kill-switched.
- **Proxy the editor's browser too.** Open `sandbox edit` in a browser profile
  proxied through the same SOCKS proxy. Webviews and extensions can make the
  browser fetch remote content directly.
- **Gateway restarts drop running sessions.** If the gateway restarts
  (`rebuild`, a Docker restart), running sessions lose their network. They
  can't reconnect, so start them again.
- **No sudo.** To add a system package, edit `assets/image/Dockerfile` and
  run `sandbox rebuild`. User-level tools install fine with `mise`.

## Development

```sh
just test               # unit tests
just test-integration   # real Docker + proxy (default 10.0.0.1:1080)
just lint
```

Integration tests use their own project (`sandbox-it`), subnet and editor
port, so they don't touch your everyday sandbox.

## License

[Apache 2.0](LICENSE)
