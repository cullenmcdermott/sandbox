#!/bin/sh
# Runner pod entrypoint: prepare sshd for Mutagen sync, then exec the runner.
set -e

# Persist SSH host keys on the PVC so the pod keeps a stable host identity across
# suspend/resume (BR4). /etc/ssh lives on the pod's ephemeral filesystem, so the
# old `ssh-keygen -A` regenerated host keys on every boot — i.e. a new host
# identity on every resume. Generate them once into the PVC and reuse. (The
# Mutagen sync client skips host-key verification by design — the auth boundary
# is the per-session key over a local port-forward — so this is hygiene plus the
# prerequisite for ever enabling host-key pinning.)
HOST_KEY_DIR=/session/state/sandbox/ssh
mkdir -p "$HOST_KEY_DIR"
if [ ! -f "$HOST_KEY_DIR/ssh_host_ed25519_key" ]; then
  ssh-keygen -q -t ed25519 -N '' -f "$HOST_KEY_DIR/ssh_host_ed25519_key"
  ssh-keygen -q -t rsa -b 4096 -N '' -f "$HOST_KEY_DIR/ssh_host_rsa_key"
  ssh-keygen -q -t ecdsa -N '' -f "$HOST_KEY_DIR/ssh_host_ecdsa_key"
fi
cp "$HOST_KEY_DIR"/ssh_host_* /etc/ssh/
chmod 600 /etc/ssh/ssh_host_*_key
chmod 644 /etc/ssh/ssh_host_*_key.pub

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
