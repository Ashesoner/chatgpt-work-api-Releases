package executiondiag

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestTraceFailedInnerBoundaryAndSnapshotIsolation(t *testing.T) {
	ctx, trace := New(context.Background(), "safe")
	outer := Start(ctx, "start_command")
	inner := Start(ctx, "sandbox_readiness")
	inner(errors.New("timeout"))
	outer(errors.New("timeout"))
	first := trace.Snapshot()
	if first.Boundary() != "sandbox_readiness" {
		t.Fatal(first)
	}
	first.Phases[1].State = "completed"
	if trace.Snapshot().Boundary() != "sandbox_readiness" {
		t.Fatal("snapshot mutates trace")
	}
}

func TestTraceConcurrentFinishAndSnapshots(t *testing.T) {
	ctx, trace := New(nil, "full")
	end := Start(ctx, "command_exec")
	if trace.Snapshot().Boundary() != "command_exec" {
		t.Fatal("pending request boundary missing")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); end(nil); _ = trace.Snapshot() }()
	}
	wg.Wait()
	if len(trace.Snapshot().Phases) != 1 {
		t.Fatal("finish appended phases")
	}
}

func TestLargeACLWarningDoesNotInventExecutionFailure(t *testing.T) {
	ctx, trace := New(nil, "safe")
	WorkspaceACL(ctx, 563)
	snapshot := trace.Snapshot()
	if snapshot.WorkspaceACLEntries != 563 || len(snapshot.Warnings) != 1 || snapshot.Boundary() != "unknown" {
		t.Fatal(snapshot)
	}
	snapshot.Warnings[0] = "mutated"
	if trace.Snapshot().Warnings[0] != "WINDOWS_WORKSPACE_ACL_LARGE" {
		t.Fatal("warning snapshot is mutable")
	}
}
