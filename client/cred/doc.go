// Package cred is the public SDK surface for agent credentials: a local
// multi-account store for Anthropic credentials — a metadata manifest plus a
// secret backend (macOS Keychain, or per-account 0600 files elsewhere) — and
// the pure token-parsing helpers the CLI and TUI share. SDK consumers pair it
// with the parent client package (CreateOptions.UseAnthropicAccount /
// SelectAnthropicAccount) to run sessions on a stored account. See Store and
// docs/archive/anthropic-account-auth-plan.md.
//
// The store reads and writes token material by necessity, but one logging
// invariant holds throughout the package: secret bytes are never printed,
// logged, or embedded in an error message. Secrets stay as []byte, never
// appear in the manifest, and never reach argv (Keychain writes go via stdin).
//
// The offline per-agent auth *status* report (the read side behind `sandbox
// auth status`) is CLI presentation, not SDK capability, and lives in the
// internal authstatus package.
package cred
