package cloudruntime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientEnsurePostsContract(t *testing.T) {
	var gotPath string
	var gotBody EnsureRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"provisioning"}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL})
	res, err := c.Ensure(context.Background(), EnsureRequest{
		RuntimeID: "rt-1",
		Provider:  "claude",
		Idle:      &IdlePolicy{TimeoutSeconds: 600, OnIdle: "suspend"},
	})
	if err != nil {
		t.Fatalf("Ensure returned error: %v", err)
	}
	if gotPath != "/api/v1/runtimes/ensure" {
		t.Fatalf("path = %q, want /api/v1/runtimes/ensure", gotPath)
	}
	if gotBody.RuntimeID != "rt-1" || gotBody.Provider != "claude" {
		t.Fatalf("body = %+v", gotBody)
	}
	if gotBody.Idle == nil || gotBody.Idle.OnIdle != "suspend" || gotBody.Idle.TimeoutSeconds != 600 {
		t.Fatalf("idle = %+v", gotBody.Idle)
	}
	if res == nil || res.Status != "provisioning" {
		t.Fatalf("result = %+v", res)
	}
}

func TestClientSuspendPostsRuntimeID(t *testing.T) {
	var gotPath string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL})
	if err := c.Suspend(context.Background(), "rt-9"); err != nil {
		t.Fatalf("Suspend returned error: %v", err)
	}
	if gotPath != "/api/v1/runtimes/suspend" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["runtime_id"] != "rt-9" {
		t.Fatalf("body = %+v", gotBody)
	}
}

func TestClientEnsureDisabledClient(t *testing.T) {
	c := NewClient(Config{})
	_, err := c.Ensure(context.Background(), EnsureRequest{RuntimeID: "x"})
	if err == nil {
		t.Fatal("expected error from disabled client")
	}
}
