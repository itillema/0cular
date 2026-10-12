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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			//nolint:gosec // G204: binary is this test's own build, and the arguments come from the table above.
			out, err := exec.CommandContext(ctx, binary, tt.args...).CombinedOutput()
			code := 0
			if err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatalf("running 0cular: %v", err)
				}
				code = exitErr.ExitCode()
			}
			if code != tt.wantCode {
				t.Errorf("0cular %s exited with %d, want %d; output:\n%s", strings.Join(tt.args, " "), code, tt.wantCode, out)
			}
		})
	}
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
