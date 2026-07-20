# sandbox-claude-runner — Claude Agent SDK runner pod image.
#
# Multi-stage: build TypeScript in a full Node image, copy the compiled dist
# into a slim runtime with sshd (for Mutagen sync) and sqlite3 (for events.db
# inspection). Node 24 matches the SDK's engine requirement.

# --- build stage ----------------------------------------------------------
FROM node:24-slim AS build
WORKDIR /app

# better-sqlite3 ships no prebuilt binary for Node 24, so npm ci compiles it
# from source via node-gyp — which needs python3, make, and a C++ toolchain.
RUN apt-get update \
 && apt-get install -y --no-install-recommends python3 make g++ \
 && rm -rf /var/lib/apt/lists/*

# Copy manifests first for layer caching.
COPY package.json package-lock.json* ./
RUN npm ci

COPY tsconfig.json ./
COPY src ./src
RUN npm run build

# Drop dev dependencies but keep the compiled better-sqlite3 native addon so
# the runtime stage can reuse node_modules without a toolchain of its own.
RUN npm prune --omit=dev

# --- runtime stage --------------------------------------------------------
FROM node:24-slim AS runtime

# sshd for Mutagen sync transport (spec 9.2); sqlite3 for events.db ops;
# git is commonly needed by the agent workspace. wget/netcat/dnsutils give the
# agent workspace a basic fetch/diagnostic surface and let integrators run
# network-isolation verification probes inside this image (no local "tools"
# rebuild needed downstream).
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      openssh-server \
      sqlite3 \
      git \
      ca-certificates \
      wget \
      netcat-openbsd \
      dnsutils \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /run/sshd \
 && command -v wget && command -v nc && command -v nslookup

# sshd config: key auth only. Mutagen logs in as root via the per-session key
# (prohibit-password = pubkey only, never a password). Single uid avoids
# file-ownership conflicts between the synced workspace and the runner; Plan 5
# can move this to a non-root user with a pod fsGroup.
RUN sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config \
 && sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config \
 && sed -i 's/^#\?PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config

WORKDIR /app

# Claude Code CLI at /usr/local/bin/claude for claude-pane sessions. Downloaded
# from the release CDN and checksum-verified (not npm-installed) so the exact
# bytes are pinned per arch. Pin CLAUDE_CODE_VERSION deliberately — hook /
# statusline payload compatibility is validated per bump, so a version change is
# a reviewed event, not a floating "latest". To bump: update the ARG and BOTH
# sha256s below from the release's {version}/manifest.json (the linux-x64 and
# linux-arm64 glibc entries — the Debian-slim base is glibc, not musl).
# TARGETARCH is BuildKit's automatic build arg; it must be re-declared here to
# be visible inside this RUN.
ARG TARGETARCH
ARG CLAUDE_CODE_VERSION=2.1.215
ARG CLAUDE_CODE_SHA256_AMD64=c1efffaaf370aa187cb6a09dd93d4e511c646899b0078476f83791b664bde7fe
ARG CLAUDE_CODE_SHA256_ARM64=2b43a3d5b0787217e5d7381fad42c7314292546fe9db9eb8b9b379de90509b30
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) platform=linux-x64;   sha256="${CLAUDE_CODE_SHA256_AMD64}" ;; \
      arm64) platform=linux-arm64; sha256="${CLAUDE_CODE_SHA256_ARM64}" ;; \
      *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    wget -qO /usr/local/bin/claude \
      "https://downloads.claude.ai/claude-code-releases/${CLAUDE_CODE_VERSION}/${platform}/claude"; \
    echo "${sha256}  /usr/local/bin/claude" | sha256sum -c -; \
    chmod 0755 /usr/local/bin/claude

# opencode CLI for opencode-server sessions: the runner supervises `opencode
# serve` and the local client attaches over a port-forward. Pin OPENCODE_VERSION
# to match the local `opencode` client — they share an OpenAPI contract and can
# break across majors. Kept at 1.17.7 to match nixpkgs#opencode (the host client
# is Nix-managed), since serve/attach must agree. Claude sessions never invoke it.
ARG OPENCODE_VERSION=1.17.7
RUN npm install -g opencode-ai@${OPENCODE_VERSION}

# codex CLI for codex-app-server sessions: the runner supervises `codex
# app-server` on a loopback WebSocket and the local `codex` client attaches over
# a port-forward. Pin CODEX_VERSION to match the local `codex` client — the two
# share the app-server JSON-RPC protocol and can break across versions. Claude and
# opencode sessions never invoke it.
ARG CODEX_VERSION=0.142.5
RUN npm install -g @openai/codex@${CODEX_VERSION}

# Reuse the production node_modules built (and compiled) in the build stage,
# avoiding a second native compile and keeping the runtime toolchain-free.
COPY package.json package-lock.json* ./
COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist

# State directories are mounted from the PVC at runtime (spec 8.2):
#   /session/state/sandbox/{session.json,events.db,audit.jsonl,outputs}
#   /session/state/claude           (CLAUDE_CONFIG_DIR)
#   /session/workspace/<project>    (cwd passed to the SDK)
RUN mkdir -p /session/state/sandbox/outputs /session/state/claude \
      /session/state/opencode/data /session/state/codex /session/workspace

# Entrypoint: generate host keys, install the per-session authorized_keys from
# the mounted Secret, start sshd, then exec the runner.
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

EXPOSE 8787 22

CMD ["/usr/local/bin/entrypoint.sh"]
