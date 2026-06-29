# Open-source launch checklist (manual maintainer steps)

Everything in this file is a human/account action the code pass could not do.
The `docs/oss-launch/` directory itself is internal working material — delete it
(or promote pieces you want public) before the public commit.

## Blocking before publish
- [ ] **Decide the image story (deferred).** Defaults point at the private
  `registry.cullen.rocks` (`internal/cli/claude_remote.go` `defaultRunnerImage`,
  `internal/k8s/reaper.go` `DefaultReaperImage`), so an external user gets
  ImagePullBackOff out of the box. Either (a) publish the runner image
  (`runner/Dockerfile`) and a reaper image (`Dockerfile.reaper` — nothing builds
  it today) to a registry outsiders can pull and flip the defaults, or (b) keep
  the private defaults and rely on the README's "build & pass `--runner-image` /
  `--reaper-image`" guidance (already documented). README + `k8s/README.md` +
  SECURITY.md describe the bring-your-own-image path.
- [ ] **Create the public GitHub repo** and push the initial commit. Note the
  whole tree is currently uncommitted WIP (no HEAD) — review the staged set
  before the first commit. `internal/tui/model.go` is already `git rm --cached`'d;
  `runner/package-lock.json` is now committed; `/dist/` and `/mockup` are
  gitignored so the 47MB + 5.8MB binaries can't slip in.
- [ ] **Remove (or promote) `docs/oss-launch/`** — it holds this checklist,
  `ASSESSMENT.md`, `PLAN.md`, and the preserved `HARDENING-BACKLOG.md` /
  `TODO-ARCHIVE.md`. Don't ship the internal process docs as-is.
- [ ] **Confirm `docs/superpowers/` stays out.** It's gitignored via the global
  `~/.gitignore` (so it won't be committed), but a fresh clone / different
  machine may not ignore it. Keep it untracked or add a repo-local ignore.

## Recommended before/at publish
- [ ] **Seed GitHub Issues** from `docs/oss-launch/TODO-ARCHIVE.md` (open backlog)
  and `docs/oss-launch/HARDENING-BACKLOG.md` (production hardening). CLAUDE.md's
  task-backlog section now points contributors at Issues, not the deleted
  `TODO.md`.
- [ ] **Fill the placeholders** in `SECURITY.md` (disclosure channel — currently
  "GitHub Security Advisories") and `CODE_OF_CONDUCT.md` (enforcement contact).
- [ ] **Verify GHCR package visibility** if you point defaults at GHCR: the
  `sandbox-claude-runner` package is currently private (403 to anonymous pulls)
  and `sandbox-reaper` does not exist. Set visibility to public + link to the
  repo, or document an imagePullSecret.

## Post-launch hardening (tracked in HARDENING-BACKLOG.md — not blocking)
- [x] Readiness/liveness probes on the pod spec (`internal/k8s/backend.go`; covered
  by `internal/k8s/backend_test.go` `TestCreateSessionProbes`).
- [ ] `/metrics` endpoint + structured logging in the runner.
- [ ] Pin the runner image to a digest (not `:latest`) in
  `.github/workflows/build-runner-image.yml`.
- [ ] `runAsNonRoot` + `fsGroup` + cap-drop for the runner pod (currently root).
- [ ] Permission token rotation; stronger permission-id entropy.
- [ ] SBOM / image scanning / provenance in the build workflow.

## Notes / decisions already applied this pass
- `session`-scope permissions are implemented (tool-name-level grant, emits
  `allow-session`). `todo.updated` is emitted (SDK `TodoWrite`) and rendered.
- `cmd/mockup/` removed; `docs/design/**` archived (verification philosophy kept
  at `docs/verification-protocol.md`); `TODO.md` archived to GitHub Issues.
- `golangci-lint` `unused` linter is now enabled and green (the dead-code ratchet
  is on). `staticcheck` + `goimports` remain deferred (see `.golangci.yml`).
- CI image-build workflow (`build-runner-image.yml`) is intentionally untouched
  (self-hosted + Tailscale + zot) — left for your "fix publishing later" pass.
