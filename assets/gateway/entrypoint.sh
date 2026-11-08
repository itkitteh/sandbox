#!/bin/sh
# Gateway entrypoint: kill switch first, then the tunnel. Any failure exits,
# so dependent services never start without the firewall.
set -eu

: "${PROXY_HOST:?PROXY_HOST is required}"
: "${PROXY_PORT:?PROXY_PORT is required}"
: "${SANDBOX_SUBNET:?SANDBOX_SUBNET is required}"
: "${EDITOR_PORT:=8443}"
export PROXY_HOST PROXY_PORT SANDBOX_SUBNET EDITOR_PORT

# Literal names: envsubst replaces only these, leaving sing-box/nft syntax alone.
# shellcheck disable=SC2016
vars='${PROXY_HOST} ${PROXY_PORT} ${SANDBOX_SUBNET} ${EDITOR_PORT}'
mkdir -p /run/sandbox
envsubst "$vars" </etc/sandbox/nftables.nft.tmpl >/run/sandbox/nftables.nft
envsubst "$vars" </etc/sandbox/sing-box.json.tmpl >/run/sandbox/sing-box.json

sysctl -qw net.ipv6.conf.all.disable_ipv6=1 2>/dev/null || true
sysctl -qw net.ipv6.conf.default.disable_ipv6=1 2>/dev/null || true

nft -f /run/sandbox/nftables.nft
sing-box check -c /run/sandbox/sing-box.json

# keyproxy is the only gateway service the workspace can reach, so it runs
# without root. Ownership is reset in case the volume was created elsewhere.
chown keyproxy:keyproxy /var/lib/sandbox-secrets
chmod 0700 /var/lib/sandbox-secrets
su-exec keyproxy /usr/local/bin/keyproxy &

exec sing-box run -c /run/sandbox/sing-box.json
