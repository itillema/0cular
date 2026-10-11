// Command 0cular gives config and data changes the safety net that code
// already has: it checks a change before it's published, keeps the last known
// good copy when loading, and rolls changes out in rings with automatic
// revert.
//
// 0cular is pre-alpha. So far it has only the version command.
//
// Exit codes:
//
//	0   success, or PASS
//	1   BLOCK, or a failed check
//	3   NEEDS_APPROVAL
//	64  usage or configuration error
//	70  internal error
//
// The Go runtime uses 2 for an unrecovered panic.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
