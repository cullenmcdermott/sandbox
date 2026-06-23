#!/bin/sh
# Runner pod entrypoint: prepare sshd for Mutagen sync, then exec the runner.
set -e

# Generate host keys if the image/PVC doesn't already have them.
ssh-keygen -A

# Install the per-session SSH public key (projected from the runner Secret at
# /etc/sandbox-ssh/authorized_key) as root's authorized_keys. The key may be
# empty when SSH sync is disabled, in which case no key is installed.
AUTH_KEY=/etc/sandbox-ssh/authorized_key
if [ -s "$AUTH_KEY" ]; then
  mkdir -p /root/.ssh
  chmod 700 /root/.ssh
  cp "$AUTH_KEY" /root/.ssh/authorized_keys
  chmod 600 /root/.ssh/authorized_keys
fi

# Start sshd in the background for the Mutagen transport (key auth only).
/usr/sbin/sshd

# Hand off to the runner as PID 1's child, preserving the container env.
exec node dist/index.js
