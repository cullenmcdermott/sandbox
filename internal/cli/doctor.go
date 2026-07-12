package cli

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	agentv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/client/cred"
	"github.com/cullenmcdermott/sandbox/internal/k8s"
)

// doctorLevel ranks a check outcome for rendering and exit-code purposes. Only
// levelFail drives a non-zero exit — a fresh host with sync tooling or the
// optional host binaries missing is still perfectly usable, so those are warns.
type doctorLevel int

const (
	levelPass doctorLevel = iota // green: works
	levelInfo                    // neutral: informational (effective config, no accounts yet)
	levelWarn                    // yellow: optional/degraded, sessions still work
	levelFail                    // red: sessions can't be created until fixed
)

// doctorResult is one check's outcome: a level, a one-line detail, and optional
// remediation text printed under the line when the level is warn/fail/info.
type doctorResult struct {
	level  doctorLevel
	detail string
	remedy string
}

// doctorCheck is a named, bounded-time probe. run must respect ctx and never
// block longer than a few seconds so the aggregate finishes well under 10s even
// against a dead-network cluster.
type doctorCheck struct {
	name string
	run  func(ctx context.Context) doctorResult
}

// doctorDeps are the injectable seams the check table closes over. Production
// wires real implementations (defaultDoctorDeps); tests substitute fakes or lean
// on PATH / KUBECONFIG manipulation so no check touches the host's real cluster
// or credential store.
type doctorDeps struct {
	// lookPath resolves a host binary on PATH (exec.LookPath in production).
	lookPath func(string) (string, error)
	// loadKube resolves the ambient kubeconfig into a rest.Config plus the
	// selected current-context name (for display). A non-nil error means no
	// usable kubeconfig — the cluster checks then short-circuit.
	loadKube func() (cfg *rest.Config, currentContext string, err error)
	// credStore opens the multi-account Anthropic store (newCredStore in
	// production).
	credStore func() (cred.Store, error)
	// namespace is the session namespace to probe (root --namespace, or the
	// default agent-sessions).
	namespace string
	// runnerImage / reaperImage are the effective default image refs to report.
	runnerImage string
	reaperImage string
	// clusterTimeout bounds each individual cluster API call.
	clusterTimeout time.Duration
	// mutagenTimeout bounds the mutagen daemon-responsiveness probe.
	mutagenTimeout time.Duration
}

// defaultDoctorNamespace mirrors internal/k8s' default session namespace; the
// doctor probes it when --namespace is unset.
const defaultDoctorNamespace = "agent-sessions"

// defaultDoctorDeps builds the production dependency set for `sandbox doctor`.
func defaultDoctorDeps() doctorDeps {
	ns := namespaceFlag
	if ns == "" {
		ns = defaultDoctorNamespace
	}
	return doctorDeps{
		lookPath:       exec.LookPath,
		loadKube:       loadAmbientKubeconfig,
		credStore:      newCredStore,
		namespace:      ns,
		runnerImage:    client.DefaultRunnerImage,
		reaperImage:    k8s.DefaultReaperImage,
		clusterTimeout: 5 * time.Second,
		mutagenTimeout: 4 * time.Second,
	}
}

// loadAmbientKubeconfig resolves the standard kubeconfig (honoring KUBECONFIG and
// ~/.kube/config) into a rest.Config and reports the selected current-context.
// It deliberately does NOT probe in-cluster config: doctor is a host-side tool.
func loadAmbientKubeconfig() (*rest.Config, string, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	cfg, err := cc.ClientConfig()
	if err != nil {
		return nil, "", err
	}
	curCtx := ""
	if raw, rerr := cc.RawConfig(); rerr == nil {
		curCtx = raw.CurrentContext
	}
	return cfg, curCtx, nil
}

// newDoctorCmd builds `sandbox doctor`: a first-run host check that verifies the
// CLI can reach a cluster, that cluster runs the agent-sandbox controller, and
// that the optional local tooling (mutagen/ssh/opencode/claude) and credential
// store are in shape. It exits non-zero only when a levelFail check is present.
func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check this host is ready to run remote sandbox sessions",
		Long: "Run a set of quick, offline-friendly checks so the CLI \"just works\" on a\n" +
			"fresh host: kubeconfig + context, the agent-sandbox controller, the session\n" +
			"namespace, optional sync tooling (mutagen/ssh), the host agent binaries\n" +
			"(opencode/claude), the credential store, and the effective image refs.\n\n" +
			"Each check prints pass / warn / fail with remediation. Only a FAIL (the CLI\n" +
			"cannot create sessions) makes doctor exit non-zero; WARN/INFO are advisory.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd.Context(), cmd.OutOrStdout(), defaultDoctorDeps())
		},
	}
}

// runDoctor assembles the check table, renders it, and returns a non-nil error
// (non-zero exit) iff any check failed at levelFail.
func runDoctor(ctx context.Context, w io.Writer, d doctorDeps) error {
	checks, kube := newDoctorChecks(d)
	fails := renderDoctor(ctx, w, checks, kube)
	if fails > 0 {
		return fmt.Errorf("doctor: %d check(s) failed — see remediation above", fails)
	}
	return nil
}

// doctorKube is the once-resolved cluster connection shared across the
// cluster-dependent checks: building the clients once avoids re-loading
// kubeconfig per check, and reachable lets later checks short-circuit so a
// dead-network host spends one timeout, not one per cluster check.
type doctorKube struct {
	core      kubernetes.Interface
	disco     discovery.DiscoveryInterface
	curCtx    string
	host      string
	configErr error
	reachable bool // set by the cluster-api check; read by the ones after it
}

// newDoctorChecks resolves the kubeconfig once, then returns the ordered check
// table plus the shared cluster handle (also returned so a caller/test can
// inspect it). Cluster checks close over the shared handle so they can short
// circuit once the API is known unreachable.
func newDoctorChecks(d doctorDeps) ([]doctorCheck, *doctorKube) {
	kube := &doctorKube{}
	cfg, curCtx, cfgErr := d.loadKube()
	kube.curCtx, kube.configErr = curCtx, cfgErr
	if cfgErr == nil && cfg != nil {
		kube.host = cfg.Host
		cfg.Timeout = d.clusterTimeout
		if c, err := kubernetes.NewForConfig(cfg); err == nil {
			kube.core = c
		}
		if dc, err := discovery.NewDiscoveryClientForConfig(cfg); err == nil {
			kube.disco = dc
		}
	}

	checks := []doctorCheck{
		{name: "kubeconfig", run: func(context.Context) doctorResult {
			if kube.configErr != nil {
				return doctorResult{levelFail, "no usable kubeconfig", "install kubectl and configure ~/.kube/config, or set KUBECONFIG: " + truncate(kube.configErr.Error(), 120)}
			}
			detail := "context: " + orNone(kube.curCtx)
			if kube.host != "" {
				detail += "  ·  " + kube.host
			}
			return doctorResult{levelPass, detail, ""}
		}},
		{name: "cluster api", run: func(ctx context.Context) doctorResult {
			if kube.core == nil {
				return doctorResult{levelWarn, "skipped — no kubeconfig", ""}
			}
			cctx, cancel := context.WithTimeout(ctx, d.clusterTimeout)
			defer cancel()
			if _, err := kube.core.Discovery().RESTClient().Get().AbsPath("/healthz").DoRaw(cctx); err != nil {
				return doctorResult{levelFail, "unreachable", "check the cluster is up and context " + orNone(kube.curCtx) + " is correct: `kubectl cluster-info` — " + truncate(err.Error(), 120)}
			}
			kube.reachable = true
			return doctorResult{levelPass, "reachable", ""}
		}},
		{name: "agent-sandbox", run: func(context.Context) doctorResult {
			if kube.disco == nil {
				return doctorResult{levelWarn, "skipped — no kubeconfig", ""}
			}
			if !kube.reachable {
				return doctorResult{levelWarn, "skipped — cluster unreachable", ""}
			}
			groups, err := kube.disco.ServerGroups()
			if err != nil {
				return doctorResult{levelWarn, "could not list API groups", truncate(err.Error(), 120)}
			}
			for _, g := range groups.Groups {
				if g.Name == agentv1alpha1.GroupVersion.Group {
					return doctorResult{levelPass, agentv1alpha1.GroupVersion.Group + "/" + agentv1alpha1.GroupVersion.Version, ""}
				}
			}
			return doctorResult{levelFail, "controller API not found (" + agentv1alpha1.GroupVersion.Group + ")", "install the agent-sandbox controller on this cluster (see k8s/ manifests / the controller's install docs)"}
		}},
		{name: "namespace", run: func(ctx context.Context) doctorResult {
			if kube.core == nil {
				return doctorResult{levelWarn, "skipped — no kubeconfig", ""}
			}
			if !kube.reachable {
				return doctorResult{levelWarn, "skipped — cluster unreachable", ""}
			}
			cctx, cancel := context.WithTimeout(ctx, d.clusterTimeout)
			defer cancel()
			_, err := kube.core.CoreV1().Namespaces().Get(cctx, d.namespace, metav1.GetOptions{})
			switch {
			case err == nil:
				return doctorResult{levelPass, d.namespace + " accessible", ""}
			case k8serrors.IsNotFound(err):
				return doctorResult{levelFail, d.namespace + " does not exist", "create it: `kubectl create namespace " + d.namespace + "`"}
			case k8serrors.IsForbidden(err):
				return doctorResult{levelWarn, "cannot read " + d.namespace + " (RBAC)", "sessions may still work if your role is namespace-scoped; otherwise grant get on namespaces"}
			default:
				return doctorResult{levelWarn, "could not check " + d.namespace, truncate(err.Error(), 120)}
			}
		}},
		{name: "mutagen", run: func(ctx context.Context) doctorResult {
			path, err := d.lookPath("mutagen")
			if err != nil {
				return doctorResult{levelWarn, "not found on PATH", "file sync is optional — sessions run without it; install mutagen (via flox) to enable host↔pod sync"}
			}
			mctx, cancel := context.WithTimeout(ctx, d.mutagenTimeout)
			defer cancel()
			if out, derr := exec.CommandContext(mctx, path, "daemon", "start").CombinedOutput(); derr != nil {
				return doctorResult{levelWarn, "daemon not responsive", "start it manually: `mutagen daemon start` — " + truncate(string(out)+derr.Error(), 120)}
			}
			return doctorResult{levelPass, "daemon responsive (" + path + ")", ""}
		}},
		{name: "ssh", run: func(context.Context) doctorResult {
			path, err := d.lookPath("ssh")
			if err != nil {
				return doctorResult{levelWarn, "not found on PATH", "sync uses ssh as mutagen's transport; install openssh to enable sync (sessions run without it)"}
			}
			return doctorResult{levelPass, path, ""}
		}},
		{name: "opencode", run: func(context.Context) doctorResult {
			path, err := d.lookPath("opencode")
			if err != nil {
				return doctorResult{levelWarn, "not found on PATH", "needed for `sandbox opencode` external panes (`opencode attach`); install via flox (pinned in .flox/env/manifest.toml)"}
			}
			return doctorResult{levelPass, path, ""}
		}},
		{name: "claude", run: func(context.Context) doctorResult {
			path, err := d.lookPath("claude")
			if err != nil {
				return doctorResult{levelWarn, "not found on PATH", "needed for `claude setup-token` subscription auth; install the Claude CLI (host-side only)"}
			}
			return doctorResult{levelPass, path, ""}
		}},
		{name: "credentials", run: func(context.Context) doctorResult {
			store, err := d.credStore()
			if err != nil {
				return doctorResult{levelWarn, "account store unreadable", truncate(err.Error(), 120)}
			}
			accounts, err := store.List()
			if err != nil {
				return doctorResult{levelWarn, "account store unreadable", truncate(err.Error(), 120)}
			}
			if len(accounts) == 0 {
				return doctorResult{levelInfo, "no Anthropic accounts stored", "add one with `sandbox auth login`, or rely on the shared cluster Secret"}
			}
			return doctorResult{levelPass, fmt.Sprintf("%d account(s) stored", len(accounts)), ""}
		}},
		{name: "images", run: func(context.Context) doctorResult {
			return doctorResult{levelInfo, "runner: " + d.runnerImage + "  ·  reaper: " + d.reaperImage, "override per session with --runner-image / --reaper-image"}
		}},
	}
	return checks, kube
}

var (
	doctorGlyphPass = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("●")
	doctorGlyphInfo = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("○")
	doctorGlyphWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render("●")
	doctorGlyphFail = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("✗")
)

func doctorGlyph(l doctorLevel) string {
	switch l {
	case levelPass:
		return doctorGlyphPass
	case levelInfo:
		return doctorGlyphInfo
	case levelWarn:
		return doctorGlyphWarn
	default:
		return doctorGlyphFail
	}
}

// renderDoctor runs each check in order, prints its line (+ remedy), a summary,
// and returns the number of levelFail results.
func renderDoctor(ctx context.Context, w io.Writer, checks []doctorCheck, _ *doctorKube) int {
	fmt.Fprintf(w, "\n  sandbox doctor\n\n")
	fails, warns := 0, 0
	for _, c := range checks {
		res := c.run(ctx)
		switch res.level {
		case levelFail:
			fails++
		case levelWarn:
			warns++
		}
		fmt.Fprintf(w, "  %s %-14s %s\n", doctorGlyph(res.level), c.name, res.detail)
		if res.remedy != "" && res.level != levelPass {
			fmt.Fprintf(w, "      %s %s\n", dimText.Render("→"), dimText.Render(res.remedy))
		}
	}
	fmt.Fprintf(w, "\n  %s\n\n", doctorSummary(fails, warns))
	return fails
}

// doctorSummary renders the closing tally line.
func doctorSummary(fails, warns int) string {
	if fails == 0 && warns == 0 {
		return dotOK.Render("all checks passed")
	}
	msg := fmt.Sprintf("%s, %s", plural(fails, "failure", "failures"), plural(warns, "warning", "warnings"))
	if fails > 0 {
		return dotBad.Render(msg)
	}
	return dotWarn.Render(msg)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
