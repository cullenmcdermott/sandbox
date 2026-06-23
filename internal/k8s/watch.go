package k8s

import (
	"context"
	"sync"
	"time"

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

// sandboxToState derives a session.State from a Sandbox object without
// making additional API calls. Pod readiness is unknown at this level (false
// by default); the caller may enrich it via Backend.Status if needed.
func sandboxToState(sb *agentv1alpha1.Sandbox) session.State {
	st := session.State{
		ID:          session.ID(sb.Name),
		SandboxName: sb.Name,
		CreatedAt:   sb.CreationTimestamp.Time,
	}

	replicas := int32(1)
	if sb.Spec.Replicas != nil {
		replicas = *sb.Spec.Replicas
	}

	if replicas == 0 {
		st.Status = session.StatusSuspended
	} else {
		// Without a pod list call we default to CREATING; the TUI will refine
		// to RUNNING once an early Backend.List or a pod event updates the entry.
		st.Status = session.StatusCreating
	}

	// Detect failure from the Finished condition.
	for _, cond := range sb.Status.Conditions {
		if cond.Type == string(agentv1alpha1.SandboxConditionFinished) {
			if cond.Reason == agentv1alpha1.SandboxReasonPodFailed {
				st.Status = session.StatusFailed
			}
		}
	}

	// Copy labels: project path and backend may be stored as annotations
	// or environment variables. For now we leave them zero — they'll be
	// filled in by the seed Backend.List call (which calls Status, which
	// knows the pod env).
	_ = sb.Labels // reserved for future label-based extraction

	return st
}
