package main

import "fmt"

// Exit codes, as listed in docs/BUILD_PLAN.md Appendix F. 64 and 70 follow
// sysexits.h (EX_USAGE and EX_SOFTWARE). The Go runtime keeps 2 for an
// unrecovered panic.
const (
	exitPass          = 0  // success, or PASS
	exitBlock         = 1  // BLOCK, or a failed check
	exitNeedsApproval = 3  // NEEDS_APPROVAL
	exitUsage         = 64 // usage or configuration error
	exitInternal      = 70 // internal error
)

// exitError is an error that carries the process exit code. A nil err means
// the failure was already reported, so there's nothing more to print.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit code %d", e.code)
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error {
	return e.err
}
