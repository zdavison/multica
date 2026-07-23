package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/cloudruntime"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/internal/util"
)

// defaultEnsureCoalesceTTL is how long a runtime's provisioner Ensure is
// suppressed after one fires, so a burst of enqueues (or terminal transitions)
// for the same offline runtime does not spawn N goroutines / N DB reads / N
// identical (idempotent) Ensure calls while the runtime is coming up.
const defaultEnsureCoalesceTTL = 30 * time.Second

// ensureCoalescer collapses repeated Ensure attempts per runtime within a TTL
// window. It is a best-effort in-memory dedup, not a correctness mechanism:
// Ensure is idempotent, so a missed suppression only costs a redundant call.
type ensureCoalescer struct {
	mu   sync.Mutex
	seen map[string]time.Time
	ttl  time.Duration
}

func newEnsureCoalescer(ttl time.Duration) *ensureCoalescer {
	return &ensureCoalescer{seen: make(map[string]time.Time), ttl: ttl}
}

// shouldFire reports whether an Ensure for key should fire now, recording the
// attempt when it returns true. Repeated calls within ttl return false. Expired
// entries are pruned opportunistically to bound the map to live runtimes.
func (c *ensureCoalescer) shouldFire(key string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if last, ok := c.seen[key]; ok && now.Sub(last) < c.ttl {
		return false
	}
	for k, t := range c.seen {
		if now.Sub(t) >= c.ttl {
			delete(c.seen, k)
		}
	}
	c.seen[key] = now
	return true
}

// OnDemandProvisioner is the backend-neutral seam multica calls to make on-demand
// compute available for a runtime. It is satisfied by *cloudruntime.Client. Nil is valid.
type OnDemandProvisioner interface {
	Enabled() bool
	Ensure(ctx context.Context, req cloudruntime.EnsureRequest) (*cloudruntime.EnsureResult, error)
}

// shouldEnsureRuntime reports whether a runtime needs provisioning: a provisioner-managed
// (cloud) runtime that is not currently online. Local runtimes self-manage.
func shouldEnsureRuntime(rt db.AgentRuntime) bool {
	return rt.RuntimeMode == "cloud" && rt.Status != "online"
}

// maybeEnsureRuntime asks the provisioner to make a runtime's compute available if it
// needs it. Safe to call for any runtime; it no-ops for local/online runtimes and a
// nil/disabled provisioner.
func (s *TaskService) maybeEnsureRuntime(ctx context.Context, rt db.AgentRuntime) {
	if s.Provisioner == nil || !s.Provisioner.Enabled() {
		return
	}
	if !shouldEnsureRuntime(rt) {
		return
	}
	if _, err := s.Provisioner.Ensure(ctx, cloudruntime.EnsureRequest{
		RuntimeID: util.UUIDToString(rt.ID),
		Provider:  rt.Provider,
	}); err != nil {
		slog.Warn("provisioner ensure failed", "runtime_id", util.UUIDToString(rt.ID), "provider", rt.Provider, "error", err)
	}
}
