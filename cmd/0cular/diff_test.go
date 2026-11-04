package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// diffTestFiles writes the check tests' files, and a version that doesn't
// decode, into a temporary directory, and returns its path.
func diffTestFiles(t *testing.T) string {
	t.Helper()
	dir := checkTestFiles(t)
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{"limit": `), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDiff(t *testing.T) {
	dir := diffTestFiles(t)
	file := func(name string) string { return filepath.Join(dir, name) }
	diff := func(contract, baseline, candidate string, rest ...string) []string {
		return append([]string{"diff", "--contract", file(contract), "--baseline", file(baseline), "--candidate", file(candidate)}, rest...)
	}
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout []string // substrings; none means stdout must be empty
		wantStderr string   // a substring; empty means stderr must be empty
	}{
		{
			name: "over budget", args: diff("contract.yaml", "baseline.json", "over-budget.json"), wantCode: exitPass,
			wantStdout: []string{
				"0cular diff  limits  baseline ",
				"! budget     limit-stable  100 → 200 (+100%); budget ±50% (needs approval)",
				"\n1 change: 1 replaced\n  replace  /limit  100 → 200\n",
			},
		},
		{
			// diff shows the change; it's check that blocks it.
			name: "a change check blocks", args: diff("contract.yaml", "baseline.json", "zero-limit.json"), wantCode: exitPass,
			wantStdout: []string{"  replace  /limit  100 → 0\n"},
		},
		{name: "no change", args: diff("contract.yaml", "baseline.json", "baseline.json"), wantCode: exitPass, wantStdout: []string{"\nNo changes.\n"}},
		{
			name: "markdown", args: diff("contract.yaml", "baseline.json", "within-budget.json", "--format", "md"), wantCode: exitPass,
			wantStdout: []string{"### 0cular diff: 1 change\n", "| replace | /limit | 100 | 120 |\n"},
		},
		{
			name: "too large", args: diff("contract.yaml", "baseline.json", "too-large.json"), wantCode: exitBlock,
			wantStdout: []string{fmt.Sprintf("the artifact is %d bytes, more than maxBytes (1024)", len(bigCandidate)), "\nNot compared, since a version doesn't decode.\n"},
		},
		{
			name: "a baseline that doesn't decode", args: diff("contract.yaml", "broken.json", "baseline.json"), wantCode: exitBlock,
			wantStdout: []string{"✗ envelope   baseline      the baseline doesn't decode under this contract: "},
		},
		{name: "unknown format", args: diff("contract.yaml", "baseline.json", "baseline.json", "--format", "html"), wantCode: exitUsage, wantStderr: `unknown report format "html"`},
		{name: "missing baseline", args: diff("contract.yaml", "missing.json", "baseline.json"), wantCode: exitUsage, wantStderr: "0cular: baseline: open "},
		{name: "missing candidate", args: diff("contract.yaml", "baseline.json", "missing.json"), wantCode: exitUsage, wantStderr: "0cular: candidate: open "},
		{name: "missing contract", args: diff("missing.yaml", "baseline.json", "baseline.json"), wantCode: exitUsage, wantStderr: "0cular: read contract: open "},
		{name: "invalid contract", args: diff("invalid.yaml", "baseline.json", "baseline.json"), wantCode: exitUsage, wantStderr: "owner: unknown field"},
		{name: "expression that doesn't compile", args: diff("badexpr.yaml", "baseline.json", "baseline.json"), wantCode: exitUsage, wantStderr: "undeclared reference to 'selff'"},
		{name: "no versions", args: []string{"diff", "--contract", file("contract.yaml")}, wantCode: exitUsage, wantStderr: `required flag(s) "baseline", "candidate" not set`},
		{name: "an argument", args: diff("contract.yaml", "baseline.json", "baseline.json", "extra"), wantCode: exitUsage, wantStderr: `unknown command "extra" for "0cular diff"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(t.Context(), tt.args, &stdout, &stderr); got != tt.wantCode {
				t.Errorf("run(%q) = %d, want %d; stderr:\n%s", tt.args, got, tt.wantCode, stderr.String())
			}
			for _, want := range tt.wantStdout {
				checkOutput(t, "stdout", stdout.String(), want)
			}
			if len(tt.wantStdout) == 0 {
				checkOutput(t, "stdout", stdout.String(), "")
			}
			checkOutput(t, "stderr", stderr.String(), tt.wantStderr)
		})
	}
}

func TestDiffJSON(t *testing.T) {
	dir := diffTestFiles(t)
	var stdout, stderr bytes.Buffer
	args := []string{"diff", "--contract", filepath.Join(dir, "contract.yaml"), "--baseline", filepath.Join(dir, "baseline.json"), "--candidate", filepath.Join(dir, "over-budget.json"), "--format", "json"}
	if code := run(t.Context(), args, &stdout, &stderr); code != exitPass {
		t.Fatalf("run(%q) = %d, want %d; stderr:\n%s", args, code, exitPass, stderr.String())
	}
	var report struct {
		SchemaVersion int  `json:"schemaVersion"`
		Compared      bool `json:"compared"`
		Changes       struct {
			Count      int `json:"count"`
			Operations []struct {
				Op, Path, Old, New string
			} `json:"operations"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("the report isn't JSON: %v\n%s", err, stdout.String())
	}
	ops := report.Changes.Operations
	if report.SchemaVersion != 1 || !report.Compared || report.Changes.Count != 1 || len(ops) != 1 ||
		ops[0].Op != "replace" || ops[0].Path != "/limit" || ops[0].Old != "100" || ops[0].New != "200" {
		t.Errorf("report = %+v, want one replace of /limit, 100 → 200", report)
	}
}
