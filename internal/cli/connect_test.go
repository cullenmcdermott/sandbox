package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// T1: waitOpencodeReady treats any HTTP response (even non-2xx) as ready,
// because a response at all proves the pod-side server answered.
func TestWaitOpencodeReadyAnyStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // 401 still means "listening"
	}))
	defer srv.Close()

	if err := waitOpencodeReady(context.Background(), srv.URL+"/"); err != nil {
		t.Fatalf("ready probe failed against a live server: %v", err)
	}
}

// T1: a transport error (nothing listening) is retried until the context
// expires, rather than false-passing the way a bare TCP dial did.
func TestWaitOpencodeReadyTransportError(t *testing.T) {
	// Bind then immediately close to get a port nothing is listening on.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL + "/"
	srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := waitOpencodeReady(ctx, url); err == nil {
		t.Fatal("ready probe should not pass when nothing is listening")
	}
}
