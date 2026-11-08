#!/bin/sh
# Refuse to run unless traffic is routed into the gateway's tunnel. The
# workspace has no capabilities, so it cannot inspect nftables directly.
set -eu

fatal() {
	cat >&2 <<EOF
================================================================
 sandbox: $1
 Refusing to start without the gateway tunnel.
 Restart with: sandbox down && sandbox up
================================================================
EOF
	exit 1
}

[ -e /sys/class/net/tun0 ] || fatal "tun0 is missing (gateway down or restarted)"
ip route get 1.1.1.1 2>/dev/null | grep -q ' dev tun0 ' ||
	fatal "default route does not go through tun0"

/usr/local/bin/sandbox-bootstrap

exec "$@"
