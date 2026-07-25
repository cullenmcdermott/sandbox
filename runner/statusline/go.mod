// Nested module (like sdktest/) so the runner image's Go builder stage can
// build it from the `runner` Docker context alone. Stdlib-only — no go.sum, and
// the build stage needs no network.
module github.com/cullenmcdermott/sandbox/runner/statusline

go 1.26.0
