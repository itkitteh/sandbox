#!/bin/sh
# First-run setup inside the home volume. Every step is skipped when already done.
set -eu

if [ ! -x "$HOME/.opencode/bin/opencode" ]; then
	echo "sandbox: installing opencode into the home volume..." >&2
	log="$HOME/.cache/opencode-install.log"
	mkdir -p "$(dirname "$log")"
	if ! curl -fsSL https://opencode.ai/install | bash >"$log" 2>&1; then
		cat "$log" >&2
		exit 1
	fi
fi

# Seeded once so you can change the model. Enforced settings (keyproxy URL,
# sharing off) live in /etc/opencode, which overrides this file.
oc_user="$HOME/.config/opencode/opencode.json"
if [ ! -f "$oc_user" ]; then
	mkdir -p "$(dirname "$oc_user")"
	cat >"$oc_user" <<EOF
{
  "\$schema": "https://opencode.ai/config.json",
  "model": "${OPENCODE_MODEL:-openrouter/anthropic/claude-sonnet-5.5}",
  "small_model": "${OPENCODE_SMALL_MODEL:-openrouter/anthropic/claude-haiku-5.5}"
}
EOF
fi

if [ ! -f "$HOME/.ssh/id_ed25519" ] && [ -n "${GIT_EMAIL:-}" ]; then
	mkdir -p "$HOME/.ssh" && chmod 700 "$HOME/.ssh"
	ssh-keygen -q -t ed25519 -N '' -C "$GIT_EMAIL" -f "$HOME/.ssh/id_ed25519"
	echo "sandbox: generated ~/.ssh/id_ed25519; run 'sandbox-keyinfo' to add it to GitHub." >&2
fi

if [ -f "$HOME/.ssh/id_ed25519.pub" ] && [ -z "$(git config --global gpg.format || true)" ]; then
	git config --global gpg.format ssh
	git config --global user.signingkey "$HOME/.ssh/id_ed25519.pub"
	git config --global commit.gpgsign true
	git config --global tag.gpgsign true
fi
