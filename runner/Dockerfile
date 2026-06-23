# sandbox-claude-runner — Claude Agent SDK runner pod image.
#
# Multi-stage: build TypeScript in a full Node image, copy the compiled dist
# into a slim runtime with sshd (for Mutagen sync) and sqlite3 (for events.db
# inspection). Node 24 matches the SDK's engine requirement.

# --- build stage ----------------------------------------------------------
FROM node:24-slim AS build
WORKDIR /app

# Copy manifests first for layer caching.
COPY package.json package-lock.json* ./
RUN npm ci

COPY tsconfig.json ./
COPY src ./src
RUN npm run build

# --- runtime stage --------------------------------------------------------
FROM node:24-slim AS runtime

# sshd for Mutagen sync transport (spec 9.2); sqlite3 for events.db ops;
# git is commonly needed by the agent workspace.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      openssh-server \
      sqlite3 \
      git \
      ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /run/sshd

# sshd config: key auth only. Mutagen logs in as root via the per-session key
# (prohibit-password = pubkey only, never a password). Single uid avoids
# file-ownership conflicts between the synced workspace and the runner; Plan 5
# can move this to a non-root user with a pod fsGroup.
RUN sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config \
 && sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config \
 && sed -i 's/^#\?PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config

WORKDIR /app

# Copy only production deps.
COPY package.json package-lock.json* ./
RUN npm ci --omit=dev

COPY --from=build /app/dist ./dist

# State directories are mounted from the PVC at runtime (spec 8.2):
#   /session/state/sandbox/{session.json,events.db,audit.jsonl,outputs}
#   /session/state/claude           (CLAUDE_CONFIG_DIR)
#   /session/workspace/<project>    (cwd passed to the SDK)
RUN mkdir -p /session/state/sandbox/outputs /session/state/claude /session/workspace

# Entrypoint: generate host keys, install the per-session authorized_keys from
# the mounted Secret, start sshd, then exec the runner.
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

EXPOSE 8787 22

CMD ["/usr/local/bin/entrypoint.sh"]
