package cloudruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// IdlePolicy is a backend-neutral declaration of what a backend should do when a
// runtime goes idle. It is enforced by the backend, not by multica.
//
//	OnIdle: "suspend" (state preserved, resumable) | "destroy" | "never" (disable).
type IdlePolicy struct {
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	OnIdle         string `json:"on_idle,omitempty"`
}

// EnsureRequest asks the provisioner to wake or create the compute for a runtime.
// Ensure is idempotent: calling it for an already-running runtime is a no-op.
type EnsureRequest struct {
	RuntimeID string      `json:"runtime_id"`
	Provider  string      `json:"provider,omitempty"`
	Idle      *IdlePolicy `json:"idle,omitempty"`
}

// EnsureResult is the backend's acknowledgement.
//
//	Status example: "provisioning" | "ready".
type EnsureResult struct {
	Status string `json:"status"`
}

// Ensure wakes/creates the compute for a runtime (contract verb; backend-neutral).
func (c *Client) Ensure(ctx context.Context, req EnsureRequest) (*EnsureResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(ctx, Request{
		Method: http.MethodPost,
		Path:   "/api/v1/runtimes/ensure",
		Body:   body,
		Op:     "provision",
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("provisioner ensure: unexpected status %d", resp.StatusCode)
	}
	var out EnsureResult
	if len(resp.Body) > 0 {
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			return nil, fmt.Errorf("provisioner ensure: decode response: %w", err)
		}
	}
	return &out, nil
}

// Suspend proactively suspends a runtime (state preserved, resumable).
func (c *Client) Suspend(ctx context.Context, runtimeID string) error {
	return c.postRuntimeID(ctx, "/api/v1/runtimes/suspend", "status", runtimeID)
}

// Destroy terminally tears a runtime's compute down.
func (c *Client) Destroy(ctx context.Context, runtimeID string) error {
	return c.postRuntimeID(ctx, "/api/v1/runtimes/destroy", "terminate", runtimeID)
}

func (c *Client) postRuntimeID(ctx context.Context, path, op, runtimeID string) error {
	body, err := json.Marshal(map[string]string{"runtime_id": runtimeID})
	if err != nil {
		return err
	}
	resp, err := c.Do(ctx, Request{Method: http.MethodPost, Path: path, Body: body, Op: op})
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("provisioner %s: unexpected status %d", path, resp.StatusCode)
	}
	return nil
}
