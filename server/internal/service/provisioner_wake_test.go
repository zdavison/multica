package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func newProvisionerWakePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set; skipping DB-backed wake test")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Skipf("cannot connect to DB: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedOfflineCloudRuntime inserts a workspace + offline cloud runtime, returns its id.
func seedOfflineCloudRuntime(t *testing.T, pool *pgxpool.Pool) db.AgentRuntime {
	t.Helper()
	ctx := context.Background()
	wsID := testUUID(21)
	rtID := testUUID(22)
	_, err := pool.Exec(ctx,
		`INSERT INTO workspace (id, name, slug) VALUES ($1, 'wake-test', 'wake-test-'||substr($1::text,1,8))
		 ON CONFLICT (id) DO NOTHING`, util.UUIDToString(wsID))
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO agent_runtime (id, workspace_id, name, runtime_mode, provider, status)
		 VALUES ($1, $2, 'wake-rt', 'cloud', 'claude', 'offline')
		 ON CONFLICT (id) DO UPDATE SET status='offline', runtime_mode='cloud'`,
		util.UUIDToString(rtID), util.UUIDToString(wsID))
	if err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	return db.AgentRuntime{ID: rtID, WorkspaceID: wsID, RuntimeMode: "cloud", Provider: "claude", Status: "offline"}
}

func TestWakeProvisionedRuntime_TriggersEnsure(t *testing.T) {
	pool := newProvisionerWakePool(t)
	rt := seedOfflineCloudRuntime(t, pool)

	prov := &fakeProvisioner{enabled: true}
	svc := &TaskService{
		Queries:     db.New(pool),
		Provisioner: prov,
	}

	svc.wakeProvisionedRuntime(rt.ID)

	// wake is dispatched asynchronously; poll briefly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(prov.callSnapshot()) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	calls := prov.callSnapshot()
	if len(calls) != 1 {
		t.Fatalf("Ensure calls = %d, want 1", len(calls))
	}
	if calls[0].RuntimeID != util.UUIDToString(rt.ID) {
		t.Fatalf("ensure runtime_id = %q", calls[0].RuntimeID)
	}
}
