package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/itillema/0cular/contract"
)

// binary is the path of the 0cular binary that TestMain builds, so the tests
// can check the exit codes of the real process.
var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "0cular-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating a build directory:", err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "0cular")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	// -buildvcs=false keeps the build independent of the repository's state.
	build := exec.Command("go", "build", "-buildvcs=false", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building 0cular: %v\n%s", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestBinaryExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{name: "version", args: []string{"version"}, wantCode: exitPass},
		{name: "help", args: []string{"--help"}, wantCode: exitPass},
		{name: "no command", args: nil, wantCode: exitUsage},
		{name: "unknown command", args: []string{"nope"}, wantCode: exitUsage},
		{name: "unknown flag", args: []string{"--nope"}, wantCode: exitUsage},
		{name: "extra argument", args: []string{"version", "extra"}, wantCode: exitUsage},
		{name: "contract schema", args: []string{"contract", "schema"}, wantCode: exitPass},
		{name: "contract without a subcommand", args: []string{"contract"}, wantCode: exitUsage},
		{name: "check without flags", args: []string{"check"}, wantCode: exitUsage},
		{name: "diff without flags", args: []string{"diff"}, wantCode: exitUsage},
		{name: "replay run", args: []string{"replay", "run"}, wantCode: exitPass},
		{name: "replay run with an unknown scenario", args: []string{"replay", "run", "nope"}, wantCode: exitUsage},
		{name: "replay without a subcommand", args: []string{"replay"}, wantCode: exitUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, out := runBinary(t, tt.args...); code != tt.wantCode {
				t.Errorf("0cular %s exited with %d, want %d; output:\n%s", strings.Join(tt.args, " "), code, tt.wantCode, out)
			}
		})
	}
}

// runBinary runs the binary with args, and returns its exit code and its
// combined output.
func runBinary(t *testing.T, args ...string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("running 0cular: %v", err)
	}
	return exitErr.ExitCode(), string(out)
}

func TestBinaryVersion(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "version").Output()
	if err != nil {
		t.Fatalf("0cular version: %v", err)
	}
	if !strings.HasPrefix(string(out), "0cular ") || !strings.Contains(string(out), "go:       go") {
		t.Errorf("0cular version printed %q, want the version, Go version and platform", out)
	}
}

func TestBinaryContractLint(t *testing.T) {
	dir := lintTestFiles(t)
	for name, wantCode := range map[string]int{
		"clean.yaml":   exitPass,
		"warning.yaml": exitPass,
		"error.yaml":   exitBlock,
		"invalid.yaml": exitBlock,
		"missing.yaml": exitUsage,
	} {
		if code, out := runBinary(t, "contract", "lint", filepath.Join(dir, name)); code != wantCode {
			t.Errorf("0cular contract lint %s exited with %d, want %d; output:\n%s", name, code, wantCode, out)
		}
	}
}

func TestBinaryCheck(t *testing.T) {
	dir := checkTestFiles(t)
	for candidate, wantCode := range map[string]int{
		"within-budget.json": exitPass,
		"over-budget.json":   exitNeedsApproval,
		"zero-limit.json":    exitBlock,
		"too-large.json":     exitBlock,
		"missing.json":       exitUsage,
	} {
		code, out := runBinary(t, "check", "--contract", filepath.Join(dir, "contract.yaml"),
			"--candidate", filepath.Join(dir, candidate), "--baseline", filepath.Join(dir, "baseline.json"))
		if code != wantCode {
			t.Errorf("0cular check on %s exited with %d, want %d; output:\n%s", candidate, code, wantCode, out)
		}
	}
}

func TestBinaryDiff(t *testing.T) {
	dir := diffTestFiles(t)
	for candidate, wantCode := range map[string]int{
		"over-budget.json": exitPass,
		"zero-limit.json":  exitPass,
		"broken.json":      exitBlock,
		"too-large.json":   exitBlock,
		"missing.json":     exitUsage,
	} {
		code, out := runBinary(t, "diff", "--contract", filepath.Join(dir, "contract.yaml"),
			"--baseline", filepath.Join(dir, "baseline.json"), "--candidate", filepath.Join(dir, candidate))
		if code != wantCode {
			t.Errorf("0cular diff to %s exited with %d, want %d; output:\n%s", candidate, code, wantCode, out)
		}
	}
}

func TestBinaryContractSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "contract", "schema").Output()
	if err != nil {
		t.Fatalf("0cular contract schema: %v", err)
	}
	if !bytes.Equal(out, contract.JSONSchema()) {
		t.Errorf("0cular contract schema printed %d bytes that differ from the embedded schema (%d bytes)", len(out), len(contract.JSONSchema()))
	}
}
