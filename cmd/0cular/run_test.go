package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // a substring; empty means stdout must be empty
		wantStderr string // a substring; empty means stderr must be empty
	}{
		{name: "version", args: []string{"version"}, wantCode: exitPass, wantStdout: "0cular "},
		{name: "help flag", args: []string{"--help"}, wantCode: exitPass, wantStdout: "Usage:"},
		{name: "help command", args: []string{"help"}, wantCode: exitPass, wantStdout: "Usage:"},
		{name: "no command", args: nil, wantCode: exitUsage, wantStderr: "Usage:"},
		{name: "unknown command", args: []string{"nope"}, wantCode: exitUsage, wantStderr: `unknown command "nope"`},
		{name: "unknown flag", args: []string{"--nope"}, wantCode: exitUsage, wantStderr: "unknown flag: --nope"},
		{name: "extra argument", args: []string{"version", "extra"}, wantCode: exitUsage, wantStderr: `unknown command "extra" for "0cular version"`},
		{name: "contract schema", args: []string{"contract", "schema"}, wantCode: exitPass, wantStdout: `"title": "0cular contract"`},
		{name: "contract without a subcommand", args: []string{"contract"}, wantCode: exitUsage, wantStderr: "Usage:"},
		{name: "unknown contract subcommand", args: []string{"contract", "nope"}, wantCode: exitUsage, wantStderr: `unknown command "nope" for "0cular contract"`},
		{
			name:       "contract schema with an argument",
			args:       []string{"contract", "schema", "extra"},
			wantCode:   exitUsage,
			wantStderr: `unknown command "extra" for "0cular contract schema"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(t.Context(), tt.args, &stdout, &stderr); got != tt.wantCode {
				t.Errorf("run(%q) = %d, want %d", tt.args, got, tt.wantCode)
			}
			checkOutput(t, "stdout", stdout.String(), tt.wantStdout)
			checkOutput(t, "stderr", stderr.String(), tt.wantStderr)
		})
	}
}

// failingWriter is an io.Writer whose writes always fail.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestRunInternalError(t *testing.T) {
	var stderr bytes.Buffer
	if got := run(t.Context(), []string{"version"}, failingWriter{}, &stderr); got != exitInternal {
		t.Errorf("run(version) with a failing stdout = %d, want %d", got, exitInternal)
	}
	checkOutput(t, "stderr", stderr.String(), "0cular: write failed")
}

func TestTagCommandErrors(t *testing.T) {
	plain := errors.New("plain failure")
	verdict := &exitError{code: exitBlock, err: errors.New("blocked")}
	tests := []struct {
		name     string
		body     func(*cobra.Command, []string) error
		wantCode int
		wantErr  error
	}{
		{name: "success", body: func(*cobra.Command, []string) error { return nil }, wantCode: exitPass},
		{name: "plain error", body: func(*cobra.Command, []string) error { return plain }, wantCode: exitInternal, wantErr: plain},
		{name: "error with an exit code", body: func(*cobra.Command, []string) error { return verdict }, wantCode: exitBlock, wantErr: verdict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := &cobra.Command{Use: "root", SilenceErrors: true, SilenceUsage: true}
			root.AddCommand(&cobra.Command{Use: "sub", RunE: tt.body})
			tagCommandErrors(root)
			root.SetArgs([]string{"sub"})
			err := root.Execute()
			if tt.wantCode == exitPass {
				if err != nil {
					t.Fatalf("Execute() error = %v, want nil", err)
				}
				return
			}
			var ee *exitError
			if !errors.As(err, &ee) {
				t.Fatalf("Execute() error = %v, want an *exitError", err)
			}
			if ee.code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", ee.code, tt.wantCode)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Execute() error = %v, want it to wrap %v", err, tt.wantErr)
			}
		})
	}
}

// checkOutput reports an error unless got contains want, or, when want is
// empty, unless got is empty too.
func checkOutput(t *testing.T, name, got, want string) {
	t.Helper()
	if want == "" {
		if got != "" {
			t.Errorf("%s = %q, want nothing", name, got)
		}
		return
	}
	if !strings.Contains(got, want) {
		t.Errorf("%s = %q, want it to contain %q", name, got, want)
	}
}
