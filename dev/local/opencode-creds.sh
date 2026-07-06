#!/usr/bin/env bash
# opencode-creds.sh — resolve the OpenCode Zen API key for the LOCAL KIND dev
# cluster and provision it as the `opencode-credentials` Secret (key: opencode-api-key).
#
# Why this exists
#   An opencode-server session pod reads OPENCODE_API_KEY from a SecretKeyRef on
#   the cluster Secret `opencode-credentials` (key `opencode-api-key`) — see
#   internal/k8s/backend.go (opencodeSecretName / opencodeSecretKeyZen). On a real
#   cluster that Secret is materialised by External Secrets Operator from the 1Password
#   vault `k8s-secrets`. For the throwaway local dev cluster there is no ESO, so
#   this script reproduces the same wiring from the SAME source of truth.
#
# Token source precedence (first hit wins)
#   1. 1Password:  `op read "$SANDBOX_OPENCODE_OP_REF"`
#                  (default op://k8s-secrets/opencode-credentials/opencode-api-key)
#   2. host env:   $OPENCODE_API_KEY
#
# Subcommands
#   ensure-secret  Resolve the key and create/update the opencode-credentials
#                  Secret in agent-sessions (idempotent). If no op/env key is
#                  available it leaves any pre-existing Secret untouched and warns.
#                  Needs kubectl + a cluster.
#   check          Non-invasive: report (to stderr) whether a key SOURCE is
#                  available, WITHOUT reading the secret. Always exits 0.
#   status         Actively resolve the key (may prompt 1Password) and report its
#                  source + length only. NEVER prints any bytes of the secret
#                  (not even a prefix — a prefix is still key material).
set -uo pipefail

OP_REF="${SANDBOX_OPENCODE_OP_REF:-op://k8s-secrets/opencode-credentials/opencode-api-key}"
# Namespace: overridable via $SANDBOX_NAMESPACE, else the kubeconfig context's
# default namespace, else agent-sessions (the backend default). No hardcoded
# assumption — this must match wherever the session pods actually run.
NS="${SANDBOX_NAMESPACE:-}"
if [ -z "$NS" ] && command -v kubectl >/dev/null 2>&1; then
    NS="$(kubectl config view --minify -o 'jsonpath={..namespace}' 2>/dev/null || true)"
fi
NS="${NS:-agent-sessions}"
SECRET="opencode-credentials"
KEY="opencode-api-key"

# Local overlay file that may hold a plaintext key; warn if world/group-readable.
OVERLAY="${SANDBOX_SECRET_OVERLAY:-dev/local/secret.local.yaml}"

err()  { printf '%s\n' "$*" >&2; }
warn() { printf '\033[33m%s\033[0m\n' "$*" >&2; }
ok()   { printf '\033[32m%s\033[0m\n' "$*" >&2; }

# check_overlay_perms warns (and tightens to 0600) if the local overlay file that
# may contain a plaintext provider key is group/world-readable. Best-effort: a
# missing file or a stat/chmod failure is silent (nothing to protect).
check_overlay_perms() {
    [ -f "$OVERLAY" ] || return 0
    local mode
    # BSD stat (macOS) uses -f '%Lp'; GNU stat uses -c '%a'. Try both.
    mode="$(stat -f '%Lp' "$OVERLAY" 2>/dev/null || stat -c '%a' "$OVERLAY" 2>/dev/null || true)"
    [ -n "$mode" ] || return 0
    # Owner-only when the last two octal digits (group, other) are both 0.
    # Length-agnostic so it handles "600" and "0600"/special-bit "2600" alike.
    case "$mode" in
    *00) return 0 ;; # e.g. 600/700/0600: group+other have no bits, fine
    *)
        warn "opencode-creds: $OVERLAY is mode $mode (group/other-readable) — it may hold a plaintext key."
        if chmod 600 "$OVERLAY" 2>/dev/null; then
            warn "  tightened to 0600."
        else
            warn "  run: chmod 600 $OVERLAY"
        fi
        ;;
    esac
}

TOKEN=""
TOKEN_SOURCE=""
resolve_token() {
    TOKEN=""
    TOKEN_SOURCE=""
    if command -v op >/dev/null 2>&1; then
        local t
        if t="$(op read "$OP_REF" 2>/dev/null)" && [ -n "$t" ]; then
            TOKEN="$t"
            TOKEN_SOURCE="1Password ($OP_REF)"
            return 0
        fi
    fi
    if [ -n "${OPENCODE_API_KEY:-}" ]; then
        TOKEN="$OPENCODE_API_KEY"
        TOKEN_SOURCE="\$OPENCODE_API_KEY env"
        return 0
    fi
    return 1
}

apply_secret() {
    local token="$1" b64
    b64="$(printf '%s' "$token" | base64 | tr -d '\n')"
    kubectl apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: $SECRET
  namespace: $NS
type: Opaque
data:
  $KEY: $b64
EOF
}

cmd_ensure_secret() {
    if ! command -v kubectl >/dev/null 2>&1; then
        err "opencode-creds: kubectl not found on PATH; cannot provision $NS/$SECRET."
        return 1
    fi
    check_overlay_perms
    if resolve_token; then
        if apply_secret "$TOKEN"; then
            ok "opencode-creds: provisioned Secret $NS/$SECRET (key: $KEY) from $TOKEN_SOURCE"
            return 0
        fi
        err "opencode-creds: failed to apply Secret $NS/$SECRET (is the local cluster up?)"
        return 1
    fi
    # No op/env key. Respect an existing Secret instead of wiping it — but say so
    # LOUDLY: a kept Secret may be STALE, and session pods resolve provider keys
    # only at pod start, so even a fresh value here won't reach a running pod
    # without a restart (suspend/resume or destroy+recreate).
    local existing
    if existing="$(kubectl -n "$NS" get secret "$SECRET" -o "jsonpath={.data['$KEY']}" 2>/dev/null)" \
        && [ -n "$existing" ]; then
        warn "opencode-creds: $NS/$SECRET already populated but NO op/env key resolved to verify/refresh it."
        warn "  Keeping the existing (possibly STALE) Secret unchanged."
        warn "  Provide a source (op/env, below) to rotate it, and restart affected pods to adopt a new key."
        return 0
    fi
    warn "opencode-creds: no OpenCode Zen API key found — opencode Zen provider stays unconfigured."
    warn "  Provide one of:"
    warn "    • 1Password: store the key at '$OP_REF' and sign in (op signin)"
    warn "    • env:       export OPENCODE_API_KEY=<your-key>"
    warn "    • manual:    edit dev/local/secret.local.yaml (opencode-api-key field)"
    return 0
}

cmd_check() {
    if [ -n "${OPENCODE_API_KEY:-}" ]; then
        return 0
    fi
    if command -v op >/dev/null 2>&1; then
        if op whoami >/dev/null 2>&1 || op account list >/dev/null 2>&1; then
            return 0
        fi
        warn "sandbox: 1Password CLI present but no account configured — OpenCode Zen key won't resolve."
        warn "  Fix: \`op signin\`  (or: export OPENCODE_API_KEY=...)   ref: $OP_REF"
        return 0
    fi
    warn "sandbox: no OpenCode Zen API key source — opencode Zen provider will be unconfigured."
    warn "  Fix: store the key at '$OP_REF' in 1Password, or export OPENCODE_API_KEY=..."
    return 0
}

cmd_status() {
    check_overlay_perms
    if resolve_token; then
        # Report only that a key resolves + its length. NEVER print any bytes of
        # the secret (not even a prefix) — a prefix is still key material.
        ok "opencode-creds: key resolves from $TOKEN_SOURCE (length ${#TOKEN})"
        return 0
    fi
    warn "opencode-creds: no OpenCode Zen API key resolves from 1Password ($OP_REF) or \$OPENCODE_API_KEY."
    return 1
}

case "${1:-}" in
ensure-secret) cmd_ensure_secret ;;
check) cmd_check ;;
status) cmd_status ;;
*)
    err "usage: ${0##*/} {ensure-secret|check|status}"
    exit 2
    ;;
esac
