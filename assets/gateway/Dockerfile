FROM golang:1.27-alpine AS keyproxy
WORKDIR /src
COPY keyproxy/*.go ./
RUN go mod init keyproxy && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /keyproxy .

FROM ghcr.io/sagernet/sing-box:v1.14.4

RUN apk add --no-cache nftables gettext-envsubst iproute2 curl bind-tools su-exec \
 && adduser -D -H -u 4000 -s /sbin/nologin keyproxy \
 && mkdir -p /var/lib/sandbox-secrets \
 && chown keyproxy:keyproxy /var/lib/sandbox-secrets \
 && chmod 0700 /var/lib/sandbox-secrets

COPY --from=keyproxy /keyproxy /usr/local/bin/keyproxy
COPY --chmod=0755 entrypoint.sh /usr/local/bin/gateway-entrypoint
COPY --chmod=0644 nftables.nft.tmpl sing-box.json.tmpl /etc/sandbox/

HEALTHCHECK --interval=2s --timeout=3s --start-period=5s --retries=15 \
	CMD nft list table inet sandbox_killswitch >/dev/null && ip link show tun0 >/dev/null

ENTRYPOINT ["/usr/local/bin/gateway-entrypoint"]
