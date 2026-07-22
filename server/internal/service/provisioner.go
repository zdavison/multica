package service

import (
	"context"
	"log/slog"

	"github.com/multica-ai/multica/server/internal/cloudruntime"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/internal/util"
)

// OnDemandProvisioner is the backend-neutral seam multica calls to wake compute
// for a runtime on demand. It is satisfied by *cloudruntime.Client. Nil is valid.
type OnDemandProvisioner interface {
	Enabled() bool
	Ensure(ctx context.Context, req cloudruntime.EnsureRequest) (*cloudruntime.EnsureResult, error)
}

// shouldEnsureRuntime reports whether a runtime needs waking: a provisioner-managed
// (cloud) runtime that is not currently online. Local runtimes self-manage.
func shouldEnsureRuntime(rt db.AgentRuntime) bool {
	return rt.RuntimeMode == "cloud" && rt.Status != "online"
}

// maybeEnsureRuntime asks the provisioner to wake a runtime if it needs it. Safe to
// call for any runtime; it no-ops for local/online runtimes and a nil/disabled provisioner.
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
		slog.Warn("provisioner ensure failed", "runtime_id", util.UUIDToString(rt.ID), "error", err)
	}
}
