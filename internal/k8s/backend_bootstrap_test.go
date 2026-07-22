package k8s

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// volumeByName / mountByName locate a named volume / mount in a slice (nil when
// absent).
func volumeByName(volumes []corev1.Volume, name string) *corev1.Volume {
	for i := range volumes {
		if volumes[i].Name == name {
			return &volumes[i]
		}
	}
	return nil
}

func mountByName(mounts []corev1.VolumeMount, name string) *corev1.VolumeMount {
	for i := range mounts {
		if mounts[i].Name == name {
			return &mounts[i]
		}
	}
	return nil
}

// TestBuildSandboxBootstrapVolume: a session with bootstrap files gets a read-only
// Secret volume + mount projecting the manifest and one file per index, Optional so
// a dropped key can't brick the mount; and the SANDBOX_BOOTSTRAP_DIR env marker is
// emitted on the common path (every backend). No bootstrap files → none of it.
func TestBuildSandboxBootstrapVolume(t *testing.T) {
	spec := session.Spec{
		ID:          "claude-sdk-boot",
		Namespace:   "agent-sessions",
		ProjectPath: "/tmp",
		Backend:     "claude-sdk",
		RunnerImage: "test:latest",
		BootstrapFiles: []session.BootstrapFile{
			{Path: "~/.claude/CLAUDE.md", Content: []byte("# guidance"), Mode: 0o644},
			{Path: "/session/state/tool/config.json", Content: []byte("{}"), Mode: 0o600},
		},
	}
	sb := buildSandbox(spec)
	volumes := sb.Spec.PodTemplate.Spec.Volumes
	mounts := sb.Spec.PodTemplate.Spec.Containers[0].VolumeMounts

	vol := volumeByName(volumes, bootstrapVolumeName)
	if vol == nil || vol.Secret == nil {
		t.Fatalf("expected a %q Secret volume", bootstrapVolumeName)
	}
	if vol.Secret.SecretName != sessionSecretName("claude-sdk-boot") {
		t.Errorf("bootstrap volume secret = %q, want %q", vol.Secret.SecretName, sessionSecretName("claude-sdk-boot"))
	}
	if vol.Secret.Optional == nil || !*vol.Secret.Optional {
		t.Error("bootstrap Secret volume must be Optional (a stripped key must not brick the mount)")
	}
	// Items: manifest + one per file index.
	wantItems := map[string]string{
		secretKeyBootstrapManifest: bootstrapManifestFile,
		"bootstrap-0":              "0",
		"bootstrap-1":              "1",
	}
	if len(vol.Secret.Items) != len(wantItems) {
		t.Fatalf("bootstrap items = %d, want %d", len(vol.Secret.Items), len(wantItems))
	}
	for _, it := range vol.Secret.Items {
		if wantItems[it.Key] != it.Path {
			t.Errorf("item %q -> %q, want %q", it.Key, it.Path, wantItems[it.Key])
		}
	}

	mount := mountByName(mounts, bootstrapVolumeName)
	if mount == nil {
		t.Fatal("expected a bootstrap volume mount")
	}
	if mount.MountPath != bootstrapMountPath || !mount.ReadOnly {
		t.Errorf("bootstrap mount = %+v, want %s read-only", mount, bootstrapMountPath)
	}

	if got := envValue(buildEnv(spec, "claude-sdk-boot"), "SANDBOX_BOOTSTRAP_DIR"); got != bootstrapMountPath {
		t.Errorf("SANDBOX_BOOTSTRAP_DIR = %q, want %q", got, bootstrapMountPath)
	}
}

// TestBuildSandboxNoBootstrap: without bootstrap files, no volume, no mount, no
// env marker — the base pod shape is untouched.
func TestBuildSandboxNoBootstrap(t *testing.T) {
	spec := session.Spec{ID: "claude-sdk-nb", Namespace: "agent-sessions", ProjectPath: "/tmp", Backend: "claude-sdk", RunnerImage: "test:latest"}
	sb := buildSandbox(spec)
	volumes := sb.Spec.PodTemplate.Spec.Volumes
	mounts := sb.Spec.PodTemplate.Spec.Containers[0].VolumeMounts
	if volumeByName(volumes, bootstrapVolumeName) != nil {
		t.Error("no bootstrap volume expected when BootstrapFiles is empty")
	}
	if mountByName(mounts, bootstrapVolumeName) != nil {
		t.Error("no bootstrap mount expected when BootstrapFiles is empty")
	}
	if envVar(buildEnv(spec, "claude-sdk-nb"), "SANDBOX_BOOTSTRAP_DIR") != nil {
		t.Error("SANDBOX_BOOTSTRAP_DIR must be absent when BootstrapFiles is empty")
	}
}

// TestCreateSessionBootstrapFiles: content lands in the per-session Secret under
// bootstrap-<n>, the manifest maps index→{path,mode}, and the marshaled Sandbox
// never inlines the file content (it rides the mounted Secret only).
func TestCreateSessionBootstrapFiles(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)

	const body0 = "# SEEDED-CLAUDE-MD-BODY"
	spec := session.Spec{
		ID:           "claude-sdk-bootc",
		ProjectPath:  "/tmp",
		Backend:      "claude-sdk",
		RunnerImage:  "test:latest",
		SSHPublicKey: "ssh-ed25519 AAAAKEY user@host",
		BootstrapFiles: []session.BootstrapFile{
			{Path: "~/.claude/CLAUDE.md", Content: []byte(body0), Mode: 0o644},
			{Path: "/session/state/tool.cfg", Content: []byte("k=v"), Mode: 0},
		},
	}
	if _, err := b.CreateSession(ctx, spec); err != nil {
		t.Fatalf("create: %v", err)
	}

	secret, err := b.core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("claude-sdk-bootc"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if string(secret.Data["bootstrap-0"]) != body0 {
		t.Errorf("bootstrap-0 = %q, want %q", secret.Data["bootstrap-0"], body0)
	}
	if string(secret.Data["bootstrap-1"]) != "k=v" {
		t.Errorf("bootstrap-1 = %q, want k=v", secret.Data["bootstrap-1"])
	}
	var manifest []bootstrapManifestEntry
	if err := json.Unmarshal(secret.Data[secretKeyBootstrapManifest], &manifest); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	if len(manifest) != 2 || manifest[0].Path != "~/.claude/CLAUDE.md" || manifest[0].Mode != 0o644 {
		t.Errorf("manifest[0] = %+v, want CLAUDE.md mode 0644", manifest[0])
	}
	// Mode 0 omitted from JSON (default 0644 applied by the runner).
	if manifest[1].Path != "/session/state/tool.cfg" || manifest[1].Mode != 0 {
		t.Errorf("manifest[1] = %+v, want tool.cfg mode 0 (default)", manifest[1])
	}
	if len(secret.Data[secretKeyRunnerToken]) == 0 {
		t.Error("runner token should still be set alongside the bootstrap files")
	}

	// Anti-regression: the marshaled Sandbox must never contain the literal file
	// content (it is a mounted Secret key, never an inline pod-spec value).
	sb, err := b.agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, "claude-sdk-bootc", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	raw, err := json.Marshal(sb)
	if err != nil {
		t.Fatalf("marshal sandbox: %v", err)
	}
	if strings.Contains(string(raw), body0) {
		t.Fatal("bootstrap file content leaked into the Sandbox object (must be a mounted Secret key only)")
	}
}

// TestCreateSessionReconcilesBootstrapFiles: a re-create patches changed content,
// refreshes the manifest, and strips keys for files/indices the new spec no longer
// carries (here: two files → one).
func TestCreateSessionReconcilesBootstrapFiles(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)

	first := session.Spec{
		ID:          "claude-sdk-bootr",
		ProjectPath: "/tmp",
		Backend:     "claude-sdk",
		RunnerImage: "test:latest",
		BootstrapFiles: []session.BootstrapFile{
			{Path: "~/.claude/CLAUDE.md", Content: []byte("OLD"), Mode: 0o644},
			{Path: "/session/state/drop.me", Content: []byte("stale"), Mode: 0o600},
		},
	}
	if _, err := b.CreateSession(ctx, first); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Re-create: change file 0's content, drop file 1 entirely.
	second := first
	second.BootstrapFiles = []session.BootstrapFile{{Path: "~/.claude/CLAUDE.md", Content: []byte("NEW"), Mode: 0o644}}
	if _, err := b.CreateSession(ctx, second); err != nil {
		t.Fatalf("re-create must succeed (Optional volume, no shape flip): %v", err)
	}

	secret, err := b.core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("claude-sdk-bootr"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if string(secret.Data["bootstrap-0"]) != "NEW" {
		t.Errorf("changed content not patched: got %q, want NEW", secret.Data["bootstrap-0"])
	}
	// The former index 1 (a file the re-create dropped) must be stripped.
	if _, ok := secret.Data["bootstrap-1"]; ok {
		t.Error("dropped bootstrap index must be stripped on re-create")
	}
	// Manifest must now describe a single file.
	var manifest []bootstrapManifestEntry
	if err := json.Unmarshal(secret.Data[secretKeyBootstrapManifest], &manifest); err != nil {
		t.Fatalf("manifest unmarshal: %v", err)
	}
	if len(manifest) != 1 {
		t.Errorf("manifest len = %d, want 1 after drop", len(manifest))
	}
}
