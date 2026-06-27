package dashboard

import "testing"

// Fix E: the dimmed dashboard backdrop behind the transcript modal is cached, so
// repeated renders (every keystroke triggers a View) reuse it instead of
// re-rendering the whole dashboard + per-line dim pass. It rebuilds only when the
// dashboard changed (modalBackdropValid cleared) or the size changed.
func TestModalBackdropCaching(t *testing.T) {
	app := NewApp(nil, nil, nil)
	app.width, app.height = 80, 24

	d1 := app.opaqueBackdrop(80, 24)
	if !app.modalBackdropValid {
		t.Fatal("backdrop should be valid after first build")
	}
	if app.bdBuilds != 1 {
		t.Fatalf("first build count = %d, want 1", app.bdBuilds)
	}

	// Repeated render at the same size with no dashboard change → cache hit.
	d2 := app.opaqueBackdrop(80, 24)
	if app.bdBuilds != 1 {
		t.Errorf("expected cache hit (no rebuild), got %d builds", app.bdBuilds)
	}
	if d1 != d2 {
		t.Error("cached backdrop content changed unexpectedly")
	}

	// A dashboard delegation invalidates the cache → next render rebuilds.
	app.modalBackdropValid = false
	app.opaqueBackdrop(80, 24)
	if app.bdBuilds != 2 {
		t.Errorf("invalidation should force a rebuild, got %d builds", app.bdBuilds)
	}

	// A size change rebuilds even while valid.
	app.opaqueBackdrop(100, 30)
	if app.bdBuilds != 3 {
		t.Errorf("size change should rebuild, got %d builds", app.bdBuilds)
	}
}
