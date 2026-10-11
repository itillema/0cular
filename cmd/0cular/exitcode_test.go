package main

import (
	"errors"
	"testing"
)

// TestExitCodes pins the exit codes to docs/BUILD_PLAN.md Appendix F.
func TestExitCodes(t *testing.T) {
	tests := []struct {
		meaning string
		got     int
		want    int
	}{
		{"PASS", exitPass, 0},
		{"BLOCK", exitBlock, 1},
		{"NEEDS_APPROVAL", exitNeedsApproval, 3},
		{"usage or configuration error", exitUsage, 64},
		{"internal error", exitInternal, 70},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("exit code for %s = %d, want %d", tt.meaning, tt.got, tt.want)
		}
	}
}

func TestExitError(t *testing.T) {
	if got, want := (&exitError{code: exitUsage}).Error(), "exit code 64"; got != want {
		t.Errorf("Error() without a cause = %q, want %q", got, want)
	}
	cause := errors.New("disk full")
	err := &exitError{code: exitInternal, err: cause}
	if got := err.Error(); got != "disk full" {
		t.Errorf("Error() = %q, want %q", got, "disk full")
	}
	if !errors.Is(err, cause) {
		t.Error("errors.Is(err, cause) = false, want true")
	}
}
