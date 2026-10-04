//go:build !windows

package codex

import "context"

func prepareCommandIdentity(_, _ string, _ CommandSpec) error { return nil }
func observeCommandACL(context.Context, CommandSpec)          {}
