package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

func runningSession(id string) Session {
	return Session{
		State:            session.State{ID: session.ID(id), Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
	}
}

// TestReconcileDropsPhantomAfterTwoMisses is the T5 fix: a session the cluster no
// longer has (but whose delete the watch missed) is pruned by the periodic
// re-list — after a one-cycle grace so a snapshot can't race a fresh add.
func TestReconcileDropsPhantomAfterTwoMisses(t *testing.T) {
	m := New(&fakeBackend{})
	m.sessions = []Session{runningSession("s1"), runningSession("s2"), runningSession("s3")}

	cluster := []session.State{{ID: "s1"}, {ID: "s2"}} // s3 gone

	m.reconcile(cluster) // first miss → grace
	if len(m.sessions) != 3 {
		t.Fatalf("after first miss, sessions = %d, want 3 (grace cycle)", len(m.sessions))
	}
	if m.reconcileMisses["s3"] != 1 {
		t.Fatalf("s3 miss count = %d, want 1", m.reconcileMisses["s3"])
	}

	m.reconcile(cluster) // second consecutive miss → drop
	if len(m.sessions) != 2 {
		t.Fatalf("after second miss, sessions = %d, want 2 (s3 dropped)", len(m.sessions))
	}
	for _, s := range m.sessions {
		if s.ID() == "s3" {
			t.Fatal("phantom session s3 was not dropped")
		}
	}
	if _, ok := m.reconcileMisses["s3"]; ok {
		t.Error("dropped session left a dangling miss counter")
	}
}

// A session that flickers out of one snapshot (race with a just-created add) and
// back into the next must never be dropped, and its miss counter must reset.
func TestReconcileResetsOnReappearance(t *testing.T) {
	m := New(&fakeBackend{})
	m.sessions = []Session{runningSession("s1")}

	m.reconcile([]session.State{}) // miss 1 (snapshot predates s1)
	if len(m.sessions) != 1 {
		t.Fatalf("grace cycle dropped s1 prematurely: %d", len(m.sessions))
	}
	m.reconcile([]session.State{{ID: "s1"}}) // present again → reset
	if _, ok := m.reconcileMisses["s1"]; ok {
		t.Error("miss counter not reset when session reappeared")
	}
	m.reconcile([]session.State{}) // miss 1 again — a single miss must not drop
	if len(m.sessions) != 1 {
		t.Fatalf("s1 dropped after a single post-reset miss: %d", len(m.sessions))
	}
}

// reconcile only removes; adds are the watch's job. A session present in the
// cluster but absent locally must NOT be inserted by the reconcile.
func TestReconcileDoesNotAddSessions(t *testing.T) {
	m := New(&fakeBackend{})
	m.sessions = nil
	m.reconcile([]session.State{{ID: "s1"}, {ID: "s2"}})
	if len(m.sessions) != 0 {
		t.Errorf("reconcile added %d sessions, want 0 (watch owns adds)", len(m.sessions))
	}
}
