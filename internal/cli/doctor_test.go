package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/rest"

	"github.com/cullenmcdermott/sandbox/client/cred"
)

// fakeCredStore is a minimal cred.Store for the credentials check: only List and
// Default are exercised by doctor; the rest are unimplemented.
type fakeCredStore struct {
	accounts []cred.Account
	listErr  error
}

func (f fakeCredStore) List() ([]cred.Account, error)  { return f.accounts, f.listErr }
func (f fakeCredStore) Add(cred.Account, []byte) error { return errors.New("unused") }
func (f fakeCredStore) Secret(string) ([]byte, error)  { return nil, errors.New("unused") }
func (f fakeCredStore) Remove(string) error            { return errors.New("unused") }
func (f fakeCredStore) SetDefault(string) error        { return errors.New("unused") }
func (f fakeCredStore) Default() (string, error)       { return "", nil }

// baseDeps returns a doctorDeps whose cluster config fails to load (so the
// cluster checks short-circuit without any network) and whose binaries are all
// present. Tests override individual fields.
func baseDeps() doctorDeps {
	return doctorDeps{
		lookPath:       func(string) (string, error) { return "/usr/bin/x", nil },
		loadKube:       func() (*rest.Config, string, error) { return nil, "", errors.New("no config") },
		credStore:      func() (cred.Store, error) { return fakeCredStore{}, nil },
		namespace:      "agent-sessions",
		runnerImage:    "runner:test",
		reaperImage:    "reaper:test",
		clusterTimeout: 200 * time.Millisecond,
		mutagenTimeout: 200 * time.Millisecond,
	}
}

// runResult finds a check by name and returns its result.
func runResult(t *testing.T, d doctorDeps, name string) doctorResult {
	t.Helper()
	checks, _ := newDoctorChecks(d)
	for _, c := range checks {
		if c.name == name {
			return c.run(context.Background())
		}
	}
	t.Fatalf("no check named %q", name)
	return doctorResult{}
}

func TestDoctor_BadKubeconfigFails(t *testing.T) {
	d := baseDeps() // loadKube already errors
	if res := runResult(t, d, "kubeconfig"); res.level != levelFail {
		t.Fatalf("kubeconfig level = %v, want levelFail", res.level)
	}
	// Downstream cluster checks must not FAIL (would double-count) — they skip
	// as warnings when no kubeconfig loaded.
	for _, name := range []string{"cluster api", "agent-sandbox", "namespace"} {
		if res := runResult(t, d, name); res.level != levelWarn {
			t.Errorf("%s level = %v, want levelWarn (skipped)", name, res.level)
		}
	}
}

func TestDoctor_MissingBinariesWarn(t *testing.T) {
	d := baseDeps()
	d.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	for _, name := range []string{"mutagen", "ssh", "opencode", "claude"} {
		res := runResult(t, d, name)
		if res.level != levelWarn {
			t.Errorf("%s (missing) level = %v, want levelWarn", name, res.level)
		}
		if res.remedy == "" {
			t.Errorf("%s (missing) should carry remediation text", name)
		}
	}
}

func TestDoctor_PresentBinariesPass(t *testing.T) {
	d := baseDeps()
	// ssh/opencode/claude only look up the binary; a found path is PASS. (mutagen
	// additionally execs a daemon probe, so it is covered separately.)
	for _, name := range []string{"ssh", "opencode", "claude"} {
		if res := runResult(t, d, name); res.level != levelPass {
			t.Errorf("%s (present) level = %v, want levelPass", name, res.level)
		}
	}
}

func TestDoctor_CredentialStore(t *testing.T) {
	t.Run("store error", func(t *testing.T) {
		d := baseDeps()
		d.credStore = func() (cred.Store, error) { return nil, errors.New("keychain locked") }
		if res := runResult(t, d, "credentials"); res.level != levelWarn {
			t.Fatalf("level = %v, want levelWarn", res.level)
		}
	})
	t.Run("list error", func(t *testing.T) {
		d := baseDeps()
		d.credStore = func() (cred.Store, error) { return fakeCredStore{listErr: errors.New("boom")}, nil }
		if res := runResult(t, d, "credentials"); res.level != levelWarn {
			t.Fatalf("level = %v, want levelWarn", res.level)
		}
	})
	t.Run("no accounts is info", func(t *testing.T) {
		d := baseDeps()
		if res := runResult(t, d, "credentials"); res.level != levelInfo {
			t.Fatalf("level = %v, want levelInfo", res.level)
		}
	})
	t.Run("accounts present pass", func(t *testing.T) {
		d := baseDeps()
		d.credStore = func() (cred.Store, error) {
			return fakeCredStore{accounts: []cred.Account{{ID: "a"}, {ID: "b"}}}, nil
		}
		res := runResult(t, d, "credentials")
		if res.level != levelPass {
			t.Fatalf("level = %v, want levelPass", res.level)
		}
		if !strings.Contains(res.detail, "2") {
			t.Errorf("detail %q should mention the account count", res.detail)
		}
	})
}

func TestDoctor_ImagesAlwaysInfo(t *testing.T) {
	d := baseDeps()
	res := runResult(t, d, "images")
	if res.level != levelInfo {
		t.Fatalf("level = %v, want levelInfo", res.level)
	}
	if !strings.Contains(res.detail, "runner:test") || !strings.Contains(res.detail, "reaper:test") {
		t.Errorf("detail %q should echo both image refs", res.detail)
	}
}

func TestRunDoctor_ExitCodeOnFail(t *testing.T) {
	var buf bytes.Buffer
	d := baseDeps() // bad kubeconfig => at least one FAIL
	if err := runDoctor(context.Background(), &buf, d); err == nil {
		t.Fatal("runDoctor should return a non-nil error when a check fails")
	}
	if !strings.Contains(buf.String(), "sandbox doctor") {
		t.Errorf("output missing header:\n%s", buf.String())
	}
}

// TestDoctor_NonClusterChecksNeverFail guards the invariant that only the
// cluster-dependent checks (kubeconfig / cluster api / agent-sandbox / namespace)
// can ever reach levelFail — the optional local tooling, credential store, and
// image checks are advisory (warn/info) and must never gate the exit code.
func TestDoctor_NonClusterChecksNeverFail(t *testing.T) {
	// Worst case for the advisory checks: every binary missing, store unreadable.
	d := baseDeps()
	d.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	d.credStore = func() (cred.Store, error) { return nil, errors.New("locked") }

	clusterChecks := map[string]bool{"kubeconfig": true, "cluster api": true, "agent-sandbox": true, "namespace": true}
	checks, _ := newDoctorChecks(d)
	for _, c := range checks {
		if clusterChecks[c.name] {
			continue
		}
		if lvl := c.run(context.Background()).level; lvl == levelFail {
			t.Errorf("advisory check %q returned levelFail; advisory checks must never FAIL", c.name)
		}
	}
}

// TestDoctor_MalformedKubeconfigViaEnv exercises the real loadAmbientKubeconfig
// against a malformed KUBECONFIG temp file (no host state touched).
func TestDoctor_MalformedKubeconfigViaEnv(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "config")
	if err := os.WriteFile(bad, []byte("this: is: not: valid: kubeconfig\n\t- ["), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", bad)
	if _, _, err := loadAmbientKubeconfig(); err == nil {
		t.Fatal("loadAmbientKubeconfig should error on a malformed KUBECONFIG")
	}
}
