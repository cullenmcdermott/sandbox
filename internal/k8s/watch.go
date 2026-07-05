package k8s

import (
	"context"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	agentv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	agentinformers "sigs.k8s.io/agent-sandbox/clients/k8s/informers/externalversions"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// StateEvent wraps a session.State with a deletion flag for the cluster watch.
type StateEvent struct {
	State   session.State
	Deleted bool
}

// coalescingBuffer holds the latest StateEvent per session id with a wake
// signal. The informer's event handler MUST NOT block — client-go delivers to a
// shared processor goroutine, so a slow handler stalls the whole watch (C7).
// push() therefore never blocks: it upserts the latest event for a session id
// (preserving first-seen order for fair delivery) and signals the sender.
// Superseding an older event for the same id is safe because the consumer keeps
// a latest-state-per-session read-model — only the most recent state matters,
// and a terminal Deleted event is itself the latest, so it is never lost. This
// bounds memory by the number of live sessions no matter how far behind the
// consumer falls.
type coalescingBuffer struct {
	mu     sync.Mutex
	latest map[session.ID]StateEvent
	order  []session.ID
	wake   chan struct{}
}

func newCoalescingBuffer() *coalescingBuffer {
	return &coalescingBuffer{latest: map[session.ID]StateEvent{}, wake: make(chan struct{}, 1)}
}

// push upserts ev for its session id and signals a waiting drainer. Never blocks.
func (c *coalescingBuffer) push(ev StateEvent) {
	id := ev.State.ID
	c.mu.Lock()
	if _, ok := c.latest[id]; !ok {
		c.order = append(c.order, id)
	}
	c.latest[id] = ev
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default: // a wake is already pending; one drain handles all buffered events
	}
}

// drain returns all buffered events in first-seen order and empties the buffer.
func (c *coalescingBuffer) drain() []StateEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.order) == 0 {
		return nil
	}
	evs := make([]StateEvent, 0, len(c.order))
	for _, id := range c.order {
		if ev, ok := c.latest[id]; ok {
			evs = append(evs, ev)
			delete(c.latest, id)
		}
	}
	c.order = c.order[:0]
	return evs
}

// Watch starts a SharedInformerFactory over the Sandbox CRD in the configured
// namespace and returns a buffered channel of StateEvents. Each Added,
// Modified, or Deleted event on a Sandbox object is converted to a
// session.State (using the same derivation as Status) and sent on the channel.
//
// The caller must seed its read-model with Backend.List before starting the
// watch, because the informer's initial list+watch is asynchronous. The
// channel is closed when ctx is cancelled.
//
// Watch returns an error only if the informer fails to start. Individual
// state-derivation failures are silently dropped (the informer keeps running).
func (b *Backend) Watch(ctx context.Context) (<-chan StateEvent, error) {
	factory := agentinformers.NewSharedInformerFactoryWithOptions(
		b.agents,
		0, // no periodic resync; the watch delivers deltas continuously
		agentinformers.WithNamespace(b.namespace),
	)

	informer := factory.Agents().V1alpha1().Sandboxes().Informer()

	// buf coalesces events per session id so the informer callbacks never block
	// on a slow consumer (C7). The sender goroutine (below) drains it into out.
	buf := newCoalescingBuffer()

	// queueState derives and enqueues a state event. The informer goroutines
	// call this; push() never blocks, so a stalled consumer can never wedge the
	// informer's shared processor.
	queueState := func(sb *agentv1alpha1.Sandbox, deleted bool) {
		if deleted {
			buf.push(StateEvent{State: session.State{ID: session.ID(sb.Name), Status: session.StatusGone}, Deleted: true})
			return
		}
		buf.push(StateEvent{State: sandboxToState(sb)})
	}

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if sb, ok := obj.(*agentv1alpha1.Sandbox); ok {
				queueState(sb, false)
			}
		},
		UpdateFunc: func(_, obj any) {
			if sb, ok := obj.(*agentv1alpha1.Sandbox); ok {
				queueState(sb, false)
			}
		},
		DeleteFunc: func(obj any) {
			sb, ok := obj.(*agentv1alpha1.Sandbox)
			if !ok {
				// Handle tombstone objects that the informer may deliver.
				if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
					sb, ok = tombstone.Obj.(*agentv1alpha1.Sandbox)
					if !ok {
						return
					}
				} else {
					return
				}
			}
			queueState(sb, true)
		},
	})
	if err != nil {
		return nil, err
	}

	factory.Start(ctx.Done())

	// Wait for initial cache sync with a generous timeout so the first
	// paint isn't based on a stale snapshot. Failures here are non-fatal:
	// the channel still works; we just may miss the initial burst.
	go func() {
		syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		cache.WaitForCacheSync(syncCtx.Done(), informer.HasSynced)
	}()

	// out is the channel returned to the caller. The caller's Update loop drains
	// it promptly; a small buffer (32) smooths normal delivery. Every distinct
	// session's latest state is delivered even under a slow consumer — the
	// coalescing buffer supersedes only stale same-session events (B6: no
	// distinct session is silently dropped; C7: the informer is never blocked).
	out := make(chan StateEvent, 32)

	// The sender goroutine is the sole writer to out. It drains the coalescing
	// buffer and forwards each event to out, blocking only itself on a slow
	// consumer — never the informer. It closes out when ctx is cancelled.
	go func() {
		defer close(out)
		for {
			evs := buf.drain()
			for _, ev := range evs {
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
			if len(evs) == 0 {
				// Buffer empty; wait for the next push or cancellation.
				select {
				case <-buf.wake:
				case <-ctx.Done():
					return
				}
			} else {
				// Drained a batch; honor cancellation before looping to pick up
				// anything that arrived while we were forwarding.
				select {
				case <-ctx.Done():
					return
				default:
				}
			}
		}
	}()

	return out, nil
}

// sandboxToState derives a session.State from a Sandbox object without making
// additional API calls. Everything it needs is on the Sandbox the informer
// already holds: lifecycle status comes from .spec.replicas + the Ready/Finished
// status conditions, and the identity fields are read back from the runner
// container's env (write-once, see buildEnv). This lets the cluster watch emit
// fully-formed RUNNING transitions in realtime — no per-event pod Get.
func sandboxToState(sb *agentv1alpha1.Sandbox) session.State {
	st := session.State{
		ID:          session.ID(sb.Name),
		SandboxName: sb.Name,
		CreatedAt:   sb.CreationTimestamp.Time,
		// Recover the identity fields the pod was created with so a session that
		// first appears via the watch (created in another terminal while the
		// dashboard is open) carries its real ProjectPath (needed to open the SSE
		// stream / Mutagen sync) and Backend (claude vs opencode). Pure functions
		// of sb — no API call. Mirrors statusFromSandbox.
		ProjectPath: sandboxEnv(sb, "PROJECT_PATH"),
		Backend:     sandboxEnv(sb, "SANDBOX_BACKEND"),
	}

	replicas := int32(1)
	if sb.Spec.Replicas != nil {
		replicas = *sb.Spec.Replicas
	}

	switch {
	case replicas == 0:
		st.Status = session.StatusSuspended
	case sandboxStale(sb, time.Now()):
		// Staleness cross-check (§1d): the Sandbox is being deleted, or its Ready
		// condition has been not-True long enough that we no longer trust the
		// controller's reporting (node-eviction lag). Report UNKNOWN rather than a
		// confident RUNNING/CREATING. Mirrors statusFromSandbox's pod cross-check;
		// the pod-level NodeLost signal isn't visible on the watch (no pod Get by
		// design), so the backend Status path covers that window.
		st.Status = session.StatusUnknown
	case sandboxReady(sb):
		// The controller sets Ready=True only once the backing pod is Running,
		// its PodReady condition is true, and it has pod IPs (and any required
		// Service is ready) — see agent-sandbox controllers/sandbox_controller.go
		// computeReadyCondition. For a non-suspended Sandbox that is exactly "the
		// runner is up and serving", so map it straight to RUNNING. This is what
		// lets a newly-created or just-resumed session flip to running (and start
		// its SSE stream) from a watch event alone, with no pod-list call.
		st.Status = session.StatusRunning
		st.PodReady = true
	default:
		// Replicas want a pod but it isn't Ready yet (pulling image, starting).
		st.Status = session.StatusCreating
	}

	// Detect failure from the Finished condition (overrides the above).
	for _, cond := range sb.Status.Conditions {
		if cond.Type == string(agentv1alpha1.SandboxConditionFinished) {
			if cond.Reason == agentv1alpha1.SandboxReasonPodFailed {
				st.Status = session.StatusFailed
			}
		}
	}

	return st
}

// sandboxReady reports whether the Sandbox's Ready status condition is True. The
// agent-sandbox controller owns this condition and only sets it True when the
// backing pod is genuinely serving (Running + PodReady + has pod IPs), so for a
// non-suspended Sandbox it is equivalent to the dashboard's RUNNING state.
func sandboxReady(sb *agentv1alpha1.Sandbox) bool {
	for _, cond := range sb.Status.Conditions {
		if cond.Type == string(agentv1alpha1.SandboxConditionReady) {
			return cond.Status == metav1.ConditionTrue
		}
	}
	return false
}

// sandboxStale mirrors podStale for the watch path, which holds only the Sandbox
// (no pod Get by design). It flags a Sandbox being deleted, or one whose Ready
// condition has been not-True past stalenessThreshold, so the watch doesn't emit
// a confident RUNNING/CREATING for a session the controller can no longer vouch
// for (§1d). The pod-level NodeLost signal isn't observable here; the backend
// Status path (which does hold the pod) covers that.
func sandboxStale(sb *agentv1alpha1.Sandbox, now time.Time) bool {
	if sb.DeletionTimestamp != nil {
		return true
	}
	for _, cond := range sb.Status.Conditions {
		if cond.Type == string(agentv1alpha1.SandboxConditionReady) {
			return readyStale(string(cond.Status), cond.LastTransitionTime, now)
		}
	}
	return false
}
