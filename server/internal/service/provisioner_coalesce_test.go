package service

import (
	"testing"
	"time"
)

func TestEnsureCoalescer_ShouldFire(t *testing.T) {
	c := newEnsureCoalescer(30 * time.Second)
	base := time.Unix(1700000000, 0)

	if !c.shouldFire("rt-1", base) {
		t.Fatal("first call for a runtime should fire")
	}
	if c.shouldFire("rt-1", base.Add(10*time.Second)) {
		t.Fatal("repeat call within TTL should be suppressed")
	}
	if !c.shouldFire("rt-2", base.Add(10*time.Second)) {
		t.Fatal("a different runtime should fire independently")
	}
	if !c.shouldFire("rt-1", base.Add(31*time.Second)) {
		t.Fatal("call after TTL should fire again")
	}
}

func TestEnsureCoalescer_PrunesExpired(t *testing.T) {
	c := newEnsureCoalescer(30 * time.Second)
	base := time.Unix(1700000000, 0)
	c.shouldFire("rt-1", base)
	c.shouldFire("rt-2", base)

	// A fire past the TTL prunes the two now-expired entries before recording.
	c.shouldFire("rt-3", base.Add(60*time.Second))

	c.mu.Lock()
	n := len(c.seen)
	c.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected only the live entry to remain after prune, got %d", n)
	}
}
