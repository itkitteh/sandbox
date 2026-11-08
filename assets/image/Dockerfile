FROM debian:trixie-slim

# Optional SOCKS proxy for build-time downloads, e.g. socks5h://10.0.0.1:1080.
# Best effort: it is not enforced like the runtime kill switch.
ARG BUILD_PROXY=""
ARG CODE_SERVER_VERSION=4.143.1

ENV LANG=C.UTF-8 \
    TZ=UTC \
    DEBIAN_FRONTEND=noninteractive

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

RUN if [ -n "$BUILD_PROXY" ]; then \
      printf 'Acquire::http::Proxy "%s";\nAcquire::https::Proxy "%s";\n' "$BUILD_PROXY" "$BUILD_PROXY" \
        > /etc/apt/apt.conf.d/90build-proxy; \
    fi \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates curl gnupg git openssh-client \
      zsh tmux neovim less ripgrep fd-find jq fzf unzip xz-utils \
      build-essential pkg-config iproute2 dnsutils procps libcap2-bin locales tzdata just \
 && ln -s /usr/bin/fdfind /usr/local/bin/fd \
 && export ALL_PROXY="$BUILD_PROXY" \
 && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
 && echo "deb [signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
      > /etc/apt/sources.list.d/github-cli.list \
 && apt-get update && apt-get install -y --no-install-recommends gh \
 && rm -f /etc/apt/apt.conf.d/90build-proxy \
 && rm -rf /var/lib/apt/lists/*

RUN export ALL_PROXY="$BUILD_PROXY" \
 && curl -fsSL https://mise.run | MISE_INSTALL_PATH=/usr/local/bin/mise sh \
 && curl -fsSL https://code-server.dev/install.sh \
      | sh -s -- --method=standalone --prefix=/usr/local --version="$CODE_SERVER_VERSION"

# Both services mount the same home volume. Owning the mount points here means
# whichever starts first initialises the volume as dev:dev.
RUN useradd --uid 1000 --user-group --create-home --shell /usr/bin/zsh dev \
 && mkdir -p /work /home/dev/.local/bin \
 && chown -R dev:dev /work /home/dev

# Create the dir first: COPY --chmod also applies the file mode to directories
# it creates, which left /etc/opencode untraversable and the config ignored.
RUN install -d -m 0755 /etc/opencode
COPY --chmod=0644 opencode.json /etc/opencode/opencode.json
COPY --chmod=0644 zshrc /etc/zsh/zshrc.sandbox
COPY --chmod=0755 entrypoint.sh /usr/local/bin/workspace-entrypoint
COPY --chmod=0755 bootstrap.sh /usr/local/bin/sandbox-bootstrap
RUN echo 'source /etc/zsh/zshrc.sandbox' >> /etc/zsh/zshrc

USER dev
WORKDIR /home/dev
ENTRYPOINT ["/usr/local/bin/workspace-entrypoint"]
CMD ["zsh", "-l"]
