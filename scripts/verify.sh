#!/usr/bin/env bash
# verify.sh — the durable, goal-independent verification gate.
#
# Salvaged from the now-retired scripts/verify-tui.sh (which was over-fitted to
# the completed TUI overhaul). Two checks remain, both goal-independent:
#
#   1. Anti-cheat scan  — no stubs/panics in non-test source; no t.Skip or bare
#                         //nolint anywhere. Catches an agent faking done-ness.
#   2. Race + twice     — `go test -race` over the whole module, run TWICE, to
#                         surface global-state leakage and nondeterminism.
#
# Escape hatch: any line containing `gate-ok:` (with a reason) is exempt from the
# anti-cheat scan. Adding one is a visible, reviewable act — that is the point.
#
# Run from the repo root:  bash scripts/verify.sh   (or `just verify`)
set -euo pipefail
cd "$(dirname "$0")/.."

red()  { printf '\033[31m%s\033[0m\n' "$*"; }
grn()  { printf '\033[32m%s\033[0m\n' "$*"; }
fail() { red "GATE FAILED: $*"; exit 1; }

# ---- 1. Anti-cheat scan -----------------------------------------------------
# Scope: Go source under internal/ and cmd/. TODO/FIXME are intentionally NOT
# forbidden — this repo uses TODO.md as its backlog and documents missing runner
# endpoints with inline TODO pointers. We forbid the things that fake completion.
# Generated files (*.gen.go) are never scanned (machine-authored).

SCAN_PATHS=(internal cmd)

# (a) Stubs/panics in NON-test source.
FORBID_SRC='panic\(|not implemented|unimplemented|return nil // stub'
if rg -n -g '*.go' -g '!*_test.go' -g '!*.gen.go' -e "$FORBID_SRC" "${SCAN_PATHS[@]}" \
     | rg -v 'gate-ok:' ; then
  fail "anti-cheat: stub/panic in non-test source (annotate with '// gate-ok: <reason>' if legitimate)"
fi

# (b) Skipped tests in ANY Go file.
if rg -n -g '*.go' -g '!*.gen.go' -e 't\.Skip' "${SCAN_PATHS[@]}" \
     | rg -v 'gate-ok:' ; then
  fail "anti-cheat: t.Skip (annotate with '// gate-ok: <reason>' if legitimate)"
fi

# (c) Blanket lint suppression. A bare //nolint is forbidden; the qualified
#     //nolint:linter form is allowed (it names what it silences). Filtering out
#     the qualified form leaves only bare suppressions.
if rg -n -g '*.go' -g '!*.gen.go' -e '//nolint' "${SCAN_PATHS[@]}" \
     | rg -v '//nolint:' | rg -v 'gate-ok:' ; then
  fail "anti-cheat: bare //nolint — use //nolint:<linter> with a reason, or '// gate-ok: <reason>'"
fi
grn "1/2 anti-cheat scan clean"

# ---- 2. Race tests, run twice ----------------------------------------------
# Twice catches global-state leakage (run 2 sees state left by run 1) and
# nondeterministic output. Whole module: no hardcoded package list.
#
# In-sandbox caveat: internal/runner + internal/models bind httptest ports,
# which the agent command-sandbox blocks. Run this with the sandbox disabled
# (it is unrestricted in CI and normal local dev).
go test -race -count=1 ./... || fail "race tests failed (run 1)"
go test -race -count=1 ./... || fail "race tests failed (run 2 — state leak / nondeterminism)"
grn "2/2 race tests pass twice"

grn "verify.sh: all gates passed"
