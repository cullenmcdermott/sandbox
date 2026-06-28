#!/usr/bin/env bash
# claude-creds.sh — resolve the Claude Code OAuth token for the LOCAL KIND dev
# cluster and provision it as the `anthropic-credentials` Secret.
#
# Why this exists
#   A claude-sdk session pod reads CLAUDE_CODE_OAUTH_TOKEN from a SecretKeyRef on
#   the cluster Secret `anthropic-credentials` (key `api-key`) — see
#   internal/k8s/backend.go (anthropicSecretName / buildEnv). On a real cluster
#   that Secret is materialised by External Secrets Operator from the 1Password
#   vault `k8s-secrets`. For the throwaway local dev cluster there is no ESO, so
#   this script reproduces the same wiring from the SAME source of truth, so
#   `just dev` (claude) works without hand-maintaining dev/local/secret.local.yaml.
#
# Token source precedence (first hit wins)
#   1. 1Password:  `op read "$SANDBOX_CLAUDE_OP_REF"`
#                  (default op://k8s-secrets/anthropic-credentials/api-key)
#   2. host env:   $CLAUDE_CODE_OAUTH_TOKEN
#
# Subcommands
#   ensure-secret  Resolve a token and create/update the anthropic-credentials
#                  Secret in agent-sessions (idempotent). If no op/env token is
#                  available it leaves any pre-existing Secret untouched (never
#                  clobbers with emptiness) and warns. Needs kubectl + a cluster.
#   check          Non-invasive: report (to stderr) whether a token SOURCE is
#                  available, WITHOUT reading the secret — so it can run on every
#                  `flox activate` without triggering a 1Password unlock prompt.
#                  Always exits 0; a warning must never break shell activation.
#   status         Actively resolve a token (may prompt 1Password) and report its
#                  source + a redacted preview — a debugging aid. Never prints the
#                  secret body (only the non-secret `sk-ant-oat01` type prefix).
set -uo pipefail

OP_REF="${SANDBOX_CLAUDE_OP_REF:-op://k8s-secrets/anthropic-credentials/api-key}"
NS="agent-sessions"
SECRET="anthropic-credentials"
KEY="api-key"

err()  { printf '%s\n' "$*" >&2; }
warn() { printf '\033[33m%s\033[0m\n' "$*" >&2; }
ok()   { printf '\033[32m%s\033[0m\n' "$*" >&2; }

# resolve_token: on success set TOKEN + TOKEN_SOURCE and return 0; else return 1.
# Uses globals (not stdout) so the secret never rides a command-substitution or a
# subshell. May trigger a 1Password unlock prompt via `op read` — fine for the
# interactive bootstrap, but NOT for `check` (which must never read the secret).
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
    if [ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]; then
        TOKEN="$CLAUDE_CODE_OAUTH_TOKEN"
        TOKEN_SOURCE="\$CLAUDE_CODE_OAUTH_TOKEN env"
        return 0
    fi
    return 1
}

# apply_secret: declaratively upsert the Secret with $1 as the api-key value. The
# token is base64-encoded into the manifest body and piped on stdin, so it never
# appears on a command line (no `ps`/argv exposure) and `apply` stays idempotent.
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
        err "claude-creds: kubectl not found on PATH; cannot provision $NS/$SECRET."
        return 1
    fi
    if resolve_token; then
        if apply_secret "$TOKEN"; then
            ok "claude-creds: provisioned Secret $NS/$SECRET from $TOKEN_SOURCE"
            return 0
        fi
        err "claude-creds: failed to apply Secret $NS/$SECRET (is the local cluster up?)"
        return 1
    fi
    # No op/env token. Respect an existing Secret (e.g. dev/local/secret.local.yaml
    # or an out-of-band ESO/manual value) instead of wiping it.
    local existing
    if existing="$(kubectl -n "$NS" get secret "$SECRET" -o "jsonpath={.data['$KEY']}" 2>/dev/null)" \
        && [ -n "$existing" ]; then
        ok "claude-creds: $NS/$SECRET already populated — kept (no op/env token to override it)"
        return 0
    fi
    warn "claude-creds: no Claude OAuth token found — claude backend stays plumbing-only."
    warn "  Provide one of:"
    warn "    • 1Password: sign in so '$OP_REF' resolves (op signin)"
    warn "    • env:       export CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-..."
    warn "    • manual:    edit dev/local/secret.local.yaml (see dev/local/README.md)"
    return 0
}

cmd_check() {
    # Fast + side-effect-free: never run `op read`/`op signin` here (they can pop a
    # 1Password unlock prompt on every shell activation). Only confirm a token is
    # OBTAINABLE — not that this shell happens to hold an unlocked session.
    if [ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]; then
        return 0 # env source present — silent
    fi
    if command -v op >/dev/null 2>&1; then
        # Two ways the token resolves at bootstrap, neither of which `op whoami`
        # alone detects:
        #   • this shell already holds a signed-in session (`op whoami` ok), OR
        #   • an account is configured and desktop-app/biometric integration is on,
        #     so `op read` Touch-ID-prompts and resolves on demand in any fresh
        #     shell — no per-tab `op signin` needed. `op account list` reads local
        #     config WITHOUT prompting, so it's a safe proxy for "source exists".
        if op whoami >/dev/null 2>&1 || op account list >/dev/null 2>&1; then
            return 0 # token resolvable at bootstrap — silent
        fi
        warn "sandbox: 1Password CLI present but no account configured — the Claude OAuth token won't resolve."
        warn "  Fix: \`op signin\`  (or: export CLAUDE_CODE_OAUTH_TOKEN=...)   ref: $OP_REF"
        return 0
    fi
    warn "sandbox: no Claude OAuth token source — 'just dev' (claude) will be plumbing-only."
    warn "  Fix: sign in to the 1Password CLI (op), or export CLAUDE_CODE_OAUTH_TOKEN=..."
    return 0
}

cmd_status() {
    if resolve_token; then
        ok "claude-creds: token resolves from $TOKEN_SOURCE"
        # Redacted: only the non-secret type prefix + length, never the body.
        printf '  preview: %s… (length %d)\n' "${TOKEN:0:12}" "${#TOKEN}" >&2
        case "$TOKEN" in
        sk-ant-oat01-*) : ;;
        *) warn "  note: value does not start with sk-ant-oat01- — is it really a Claude OAuth token?" ;;
        esac
        return 0
    fi
    warn "claude-creds: no Claude OAuth token resolves from 1Password ($OP_REF) or \$CLAUDE_CODE_OAUTH_TOKEN."
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
