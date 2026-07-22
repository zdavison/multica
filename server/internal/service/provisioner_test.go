package service

import (
	"context"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/cloudruntime"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type fakeProvisioner struct {
	enabled bool
	mu      sync.Mutex
	calls   []cloudruntime.EnsureRequest
}

func (f *fakeProvisioner) Enabled() bool { return f.enabled }
func (f *fakeProvisioner) Ensure(ctx context.Context, req cloudruntime.EnsureRequest) (*cloudruntime.EnsureResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.mu.Unlock()
	return &cloudruntime.EnsureResult{Status: "provisioning"}, nil
}

// callSnapshot returns a race-safe copy of the recorded Ensure calls.
func (f *fakeProvisioner) callSnapshot() []cloudruntime.EnsureRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]cloudruntime.EnsureRequest(nil), f.calls...)
}

func TestShouldEnsureRuntime(t *testing.T) {
	cases := []struct {
		name string
		rt   db.AgentRuntime
		want bool
	}{
		{"cloud offline -> ensure", db.AgentRuntime{RuntimeMode: "cloud", Status: "offline"}, true},
		{"cloud online -> skip", db.AgentRuntime{RuntimeMode: "cloud", Status: "online"}, false},
		{"local offline -> skip", db.AgentRuntime{RuntimeMode: "local", Status: "offline"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldEnsureRuntime(tc.rt); got != tc.want {
				t.Fatalf("shouldEnsureRuntime = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMaybeEnsureRuntime_CallsEnsureForCloudOffline(t *testing.T) {
	prov := &fakeProvisioner{enabled: true}
	svc := &TaskService{Provisioner: prov}
	rt := db.AgentRuntime{ID: testUUID(3), Provider: "claude", RuntimeMode: "cloud", Status: "offline"}

	svc.maybeEnsureRuntime(context.Background(), rt)

	calls := prov.callSnapshot()
	if len(calls) != 1 {
		t.Fatalf("Ensure calls = %d, want 1", len(calls))
	}
	if calls[0].Provider != "claude" {
		t.Fatalf("provider = %q", calls[0].Provider)
	}
}

func TestMaybeEnsureRuntime_SkipsWhenOnlineOrNilOrDisabled(t *testing.T) {
	rtOnline := db.AgentRuntime{ID: testUUID(4), RuntimeMode: "cloud", Status: "online"}
	rtOffline := db.AgentRuntime{ID: testUUID(5), RuntimeMode: "cloud", Status: "offline"}

	// online cloud runtime -> no call
	p1 := &fakeProvisioner{enabled: true}
	(&TaskService{Provisioner: p1}).maybeEnsureRuntime(context.Background(), rtOnline)
	if len(p1.callSnapshot()) != 0 {
		t.Fatalf("online: Ensure calls = %d, want 0", len(p1.callSnapshot()))
	}

	// disabled provisioner -> no call
	p2 := &fakeProvisioner{enabled: false}
	(&TaskService{Provisioner: p2}).maybeEnsureRuntime(context.Background(), rtOffline)
	if len(p2.callSnapshot()) != 0 {
		t.Fatalf("disabled: Ensure calls = %d, want 0", len(p2.callSnapshot()))
	}

	// nil provisioner -> no panic, no call
	(&TaskService{Provisioner: nil}).maybeEnsureRuntime(context.Background(), rtOffline)
}
