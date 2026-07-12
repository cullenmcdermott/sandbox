package session

// Autopilot driver kinds — which autonomous flavour a runner-owned driver runs.
// Mirrors AutopilotSpec.kind in runner/src/types.ts.
const (
	// AutopilotKindLoop re-runs a prompt every interval until stopped (or its
	// sentinel/budget fires). The `/loop` command.
	AutopilotKindLoop = "loop"
	// AutopilotKindGoal keeps working turn-after-turn toward a stated condition
	// until the sentinel appears (or the iteration/token budget is hit). The
	// `/goal` command.
	AutopilotKindGoal = "goal"
)

// AutopilotOverrides are the per-turn model/effort/mode overrides applied to
// every self-submitted turn of a runner-owned autopilot driver. Mirrors
// AutopilotRequestBody.overrides in runner/src/types.ts; each field is optional
// (empty => the runner's session/turn default).
type AutopilotOverrides struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	Mode   string `json:"mode,omitempty"`
}

// AutopilotRequest is the PUT /sessions/:id/autopilot request body — the
// client-supplied portion of an arm/replace (see docs/runner-api.md and
// docs/server-side-loop-adr.md §1). It mirrors the runner's AutopilotRequestBody
// field-for-field (camelCase wire names): the runner fills the rest of the spec
// (state, gen, iterations, armed_at, last_completed_at, stopped_reason).
//
// Kind (AutopilotKindLoop|AutopilotKindGoal) and Prompt are required; the rest
// are optional. Sentinel is a completion marker scanned in each completed
// assistant text (empty disables it). IntervalMs is the delay between iterations
// in ms (0 = immediate). MaxIterations is a hard iteration ceiling — the runner
// always enforces it and defaults an omitted (0) value to 50. TokenBudget is an
// optional hard token ceiling (nil = no cap).
type AutopilotRequest struct {
	Kind          string             `json:"kind"`
	Prompt        string             `json:"prompt"`
	Sentinel      string             `json:"sentinel,omitempty"`
	IntervalMs    int64              `json:"intervalMs,omitempty"`
	Overrides     AutopilotOverrides `json:"overrides,omitempty"`
	MaxIterations int                `json:"maxIterations,omitempty"`
	TokenBudget   *int64             `json:"tokenBudget,omitempty"`
}
