// Package executiondiag records bounded command phase timings without command,
// path, environment, or output contents. A phase is an observation boundary,
// not proof that the target process has started.
package executiondiag

import (
	"context"
	"sync"
	"time"
)

type Phase struct {
	Name      string `json:"name"`
	ElapsedMS int64  `json:"elapsed_ms"`
	State     string `json:"state"`
}

type Snapshot struct {
	AccessProfile       string   `json:"access_profile"`
	ElapsedMS           int64    `json:"elapsed_ms"`
	Phases              []Phase  `json:"phases"`
	WorkspaceACLEntries int      `json:"workspace_acl_entries,omitempty"`
	Warnings            []string `json:"warnings,omitempty"`
}

// Boundary identifies the innermost failed or unfinished observed phase.
// command_exec includes sandbox startup AND target execution: this protocol
// does not report a target-process-start event on Windows.
func (s *Snapshot) Boundary() string {
	if s == nil {
		return "unknown"
	}
	for i := len(s.Phases) - 1; i >= 0; i-- {
		if s.Phases[i].State == "failed" {
			return s.Phases[i].Name
		}
	}
	for i := len(s.Phases) - 1; i >= 0; i-- {
		if s.Phases[i].State == "pending" {
			return s.Phases[i].Name
		}
	}
	return "unknown"
}

type Outcome struct {
	State       string    `json:"state"`
	At          string    `json:"at,omitempty"`
	Error       string    `json:"error,omitempty"`
	Diagnostics *Snapshot `json:"diagnostics,omitempty"`
}

type Trace struct {
	mu                  sync.Mutex
	started             time.Time
	profile             string
	phases              []Phase
	starts              []time.Time
	workspaceACLEntries int
	warnings            []string
}

// WorkspaceACL is a constant-cost root metadata observation. The threshold is
// a diagnostic heuristic, never a support limit or authorization decision.
func WorkspaceACL(ctx context.Context, entries int) {
	if ctx == nil {
		return
	}
	t, _ := ctx.Value(contextKey{}).(*Trace)
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.workspaceACLEntries = entries
	if entries >= 256 {
		t.warnings = []string{"WINDOWS_WORKSPACE_ACL_LARGE"}
	}
}

type contextKey struct{}

func New(ctx context.Context, profile string) (context.Context, *Trace) {
	if ctx == nil {
		ctx = context.Background()
	}
	t := &Trace{started: time.Now(), profile: profile}
	return context.WithValue(ctx, contextKey{}, t), t
}

func Start(ctx context.Context, name string) func(error) {
	if ctx == nil {
		return func(error) {}
	}
	t, _ := ctx.Value(contextKey{}).(*Trace)
	if t == nil {
		return func(error) {}
	}
	t.mu.Lock()
	i := len(t.phases)
	t.phases = append(t.phases, Phase{Name: name, State: "pending"})
	t.starts = append(t.starts, time.Now())
	t.mu.Unlock()
	var once sync.Once
	return func(err error) {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.phases[i].ElapsedMS = time.Since(t.starts[i]).Milliseconds()
			t.phases[i].State = "completed"
			if err != nil {
				t.phases[i].State = "failed"
			}
		})
	}
}

func (t *Trace) Snapshot() *Snapshot {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	phases := append([]Phase(nil), t.phases...)
	for i := range phases {
		if phases[i].State == "pending" {
			phases[i].ElapsedMS = time.Since(t.starts[i]).Milliseconds()
		}
	}
	return &Snapshot{AccessProfile: t.profile, ElapsedMS: time.Since(t.started).Milliseconds(), Phases: phases, WorkspaceACLEntries: t.workspaceACLEntries, Warnings: append([]string(nil), t.warnings...)}
}
