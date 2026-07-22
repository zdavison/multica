package service

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/cloudruntime"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type fakeProvisioner struct {
	enabled bool
	calls   []cloudruntime.EnsureRequest
}

func (f *fakeProvisioner) Enabled() bool { return f.enabled }
func (f *fakeProvisioner) Ensure(ctx context.Context, req cloudruntime.EnsureRequest) (*cloudruntime.EnsureResult, error) {
	f.calls = append(f.calls, req)
	return &cloudruntime.EnsureResult{Status: "provisioning"}, nil
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

	if len(prov.calls) != 1 {
		t.Fatalf("Ensure calls = %d, want 1", len(prov.calls))
	}
	if prov.calls[0].Provider != "claude" {
		t.Fatalf("provider = %q", prov.calls[0].Provider)
	}
}

func TestMaybeEnsureRuntime_SkipsWhenOnlineOrNilOrDisabled(t *testing.T) {
	rtOnline := db.AgentRuntime{ID: testUUID(4), RuntimeMode: "cloud", Status: "online"}
	rtOffline := db.AgentRuntime{ID: testUUID(5), RuntimeMode: "cloud", Status: "offline"}

	// online cloud runtime -> no call
	p1 := &fakeProvisioner{enabled: true}
	(&TaskService{Provisioner: p1}).maybeEnsureRuntime(context.Background(), rtOnline)
	if len(p1.calls) != 0 {
		t.Fatalf("online: Ensure calls = %d, want 0", len(p1.calls))
	}

	// disabled provisioner -> no call
	p2 := &fakeProvisioner{enabled: false}
	(&TaskService{Provisioner: p2}).maybeEnsureRuntime(context.Background(), rtOffline)
	if len(p2.calls) != 0 {
		t.Fatalf("disabled: Ensure calls = %d, want 0", len(p2.calls))
	}

	// nil provisioner -> no panic, no call
	(&TaskService{Provisioner: nil}).maybeEnsureRuntime(context.Background(), rtOffline)
}
