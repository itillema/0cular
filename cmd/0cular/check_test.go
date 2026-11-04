package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// bigCandidate is a candidate larger than checkContract's maxBytes.
var bigCandidate = `{"limit": 1, "pad": "` + strings.Repeat("x", 2000) + `"}`

// checkContract has an invariant and a measure budget on one number.
const checkContract = `apiVersion: 0cular/v1alpha1
kind: Contract
metadata: {name: limits}
artifact: {format: json, maxBytes: 1KiB}
invariants:
  - {name: limit-positive, expr: "self.limit > 0", message: the consumer divides by the limit}
diffBudgets:
  - {name: limit-stable, measure: self.limit, maxChangePct: 50}
`

// checkTestFiles writes a contract and versions for the check tests into a
// temporary directory, and returns its path.
func checkTestFiles(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"contract.yaml":      checkContract,
		"invalid.yaml":       strings.Replace(checkContract, "metadata:", "owner: me\nmetadata:", 1),
		"badexpr.yaml":       strings.Replace(checkContract, "self.limit > 0", "selff.limit > 0", 1),
		"baseline.json":      `{"limit": 100}`,
		"within-budget.json": `{"limit": 120}`,
		"over-budget.json":   `{"limit": 200}`,
		"zero-limit.json":    `{"limit": 0}`,
		"too-large.json":     bigCandidate,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCheck(t *testing.T) {
	dir := checkTestFiles(t)
	file := func(name string) string { return filepath.Join(dir, name) }
	check := func(contract, candidate string, rest ...string) []string {
		return append([]string{"check", "--contract", file(contract), "--candidate", file(candidate)}, rest...)
	}
	baseline := []string{"--baseline", file("baseline.json")}
	approve := slices.Concat(baseline, []string{"--approve", "--approver", "alice", "--reason", "the limit doubles on purpose"})
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // a substring; empty means stdout must be empty
		wantStderr string // a substring; empty means stderr must be empty
	}{
		{name: "pass", args: check("contract.yaml", "within-budget.json", baseline...), wantCode: exitPass, wantStdout: "\nPASS\n"},
		{name: "needs approval", args: check("contract.yaml", "over-budget.json", baseline...), wantCode: exitNeedsApproval, wantStdout: "! budget     limit-stable    100 → 200 (+100%)"},
		{name: "block", args: check("contract.yaml", "zero-limit.json", baseline...), wantCode: exitBlock, wantStdout: "✗ invariant  limit-positive  self.limit = 0, not > 0"},
		{name: "no baseline", args: check("contract.yaml", "over-budget.json", "--no-baseline"), wantCode: exitPass, wantStdout: "there's no baseline to compare with (skipped)"},
		{name: "markdown", args: check("contract.yaml", "within-budget.json", append(baseline, "--format", "md")...), wantCode: exitPass, wantStdout: "### 0cular check: ✅ PASS"},
		{
			name: "too large", args: check("contract.yaml", "too-large.json", baseline...), wantCode: exitBlock,
			wantStdout: fmt.Sprintf("the artifact is %d bytes, more than maxBytes (1024)", len(bigCandidate)),
		},
		{name: "unknown format", args: check("contract.yaml", "within-budget.json", append(baseline, "--format", "html")...), wantCode: exitUsage, wantStderr: `unknown report format "html"`},
		{name: "missing candidate", args: check("contract.yaml", "missing.json", baseline...), wantCode: exitUsage, wantStderr: "0cular: candidate: open "},
		{name: "missing baseline", args: check("contract.yaml", "within-budget.json", "--baseline", file("missing.json")), wantCode: exitUsage, wantStderr: "0cular: baseline: open "},
		{name: "missing contract", args: check("missing.yaml", "within-budget.json", baseline...), wantCode: exitUsage, wantStderr: "0cular: read contract: open "},
		{name: "invalid contract", args: check("invalid.yaml", "within-budget.json", baseline...), wantCode: exitUsage, wantStderr: "owner: unknown field"},
		{name: "expression that doesn't compile", args: check("badexpr.yaml", "within-budget.json", baseline...), wantCode: exitUsage, wantStderr: "undeclared reference to 'selff'; did you mean self?"},
		{name: "no contract flag", args: []string{"check", "--candidate", file("within-budget.json"), "--no-baseline"}, wantCode: exitUsage, wantStderr: `required flag(s) "contract" not set`},
		{name: "no baseline flag", args: check("contract.yaml", "within-budget.json"), wantCode: exitUsage, wantStderr: "at least one of the flags in the group [baseline no-baseline] is required"},
		{name: "both baseline flags", args: check("contract.yaml", "within-budget.json", append(baseline, "--no-baseline")...), wantCode: exitUsage, wantStderr: "[baseline no-baseline] were all set"},
		{name: "an argument", args: check("contract.yaml", "within-budget.json", append(baseline, "extra")...), wantCode: exitUsage, wantStderr: `unknown command "extra" for "0cular check"`},
		{name: "approved", args: check("contract.yaml", "over-budget.json", approve...), wantCode: exitPass, wantStdout: "PASS\nApproved by alice until "},
		{name: "approved budget", args: check("contract.yaml", "over-budget.json", approve...), wantCode: exitPass, wantStdout: "! budget     limit-stable    100 → 200 (+100%); budget ±50% (approved)"},
		{
			name: "approving a block", args: check("contract.yaml", "zero-limit.json", approve...), wantCode: exitBlock,
			wantStdout: "\nBLOCK\n", wantStderr: "0cular: nothing was approved: the candidate is blocked, which no approval can accept",
		},
		{
			name: "approving a pass", args: check("contract.yaml", "within-budget.json", approve...), wantCode: exitPass,
			wantStdout: "\nPASS\n", wantStderr: "0cular: nothing was approved: nothing needs approval",
		},
		{
			name: "approving without a reason", args: check("contract.yaml", "over-budget.json", append(baseline, "--approve", "--approver", "alice")...),
			wantCode: exitUsage, wantStderr: "if any flags in the group [approve approver reason] are set they must all be set; missing [reason]",
		},
		{
			name: "a blank approver", args: check("contract.yaml", "over-budget.json", append(baseline, "--approve", "--approver", " ", "--reason", "why")...),
			wantCode: exitUsage, wantStderr: "0cular: --approve needs who approves, in --approver, and why, in --reason",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(t.Context(), tt.args, &stdout, &stderr); got != tt.wantCode {
				t.Errorf("run(%q) = %d, want %d; stderr:\n%s", tt.args, got, tt.wantCode, stderr.String())
			}
			checkOutput(t, "stdout", stdout.String(), tt.wantStdout)
			checkOutput(t, "stderr", stderr.String(), tt.wantStderr)
		})
	}
}

func TestCheckJSON(t *testing.T) {
	dir := checkTestFiles(t)
	var stdout, stderr bytes.Buffer
	args := []string{"check", "--contract", filepath.Join(dir, "contract.yaml"), "--candidate", filepath.Join(dir, "within-budget.json"), "--no-baseline", "--format", "json"}
	if code := run(t.Context(), args, &stdout, &stderr); code != exitPass {
		t.Fatalf("run(%q) = %d, want %d; stderr:\n%s", args, code, exitPass, stderr.String())
	}
	var report struct {
		SchemaVersion int    `json:"schemaVersion"`
		Verdict       string `json:"verdict"`
		Candidate     struct {
			Key string `json:"key"`
		} `json:"candidate"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("the report isn't JSON: %v\n%s", err, stdout.String())
	}
	if report.SchemaVersion != 1 || report.Verdict != "PASS" || !strings.HasSuffix(report.Candidate.Key, "/within-budget.json") {
		t.Errorf("report = %+v, want schemaVersion 1, PASS and the candidate's path with forward slashes", report)
	}
}

// TestCheckApproveJSON checks the approval that --approve records: who,
// why, which budgets, and an expiry an hour after the check.
func TestCheckApproveJSON(t *testing.T) {
	dir := checkTestFiles(t)
	var stdout, stderr bytes.Buffer
	args := []string{
		"check", "--contract", filepath.Join(dir, "contract.yaml"), "--candidate", filepath.Join(dir, "over-budget.json"),
		"--baseline", filepath.Join(dir, "baseline.json"), "--approve", "--approver", "alice", "--reason", "the limit doubles on purpose", "--format", "json",
	}
	start := time.Now()
	if code := run(t.Context(), args, &stdout, &stderr); code != exitPass {
		t.Fatalf("run(%q) = %d, want %d; stderr:\n%s", args, code, exitPass, stderr.String())
	}
	var report struct {
		Verdict   string `json:"verdict"`
		Approvals []struct {
			By      string    `json:"by"`
			Reason  string    `json:"reason"`
			Expires time.Time `json:"expires"`
			Budgets []string  `json:"budgets"`
			Applied bool      `json:"applied"`
		} `json:"approvals"`
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("the report isn't JSON: %v\n%s", err, stdout.String())
	}
	if report.Verdict != "PASS" || len(report.Approvals) != 1 {
		t.Fatalf("report = %+v, want PASS with one approval", report)
	}
	a := report.Approvals[0]
	if a.By != "alice" || a.Reason != "the limit doubles on purpose" || !a.Applied || len(a.Budgets) != 1 || a.Budgets[0] != "limit-stable" {
		t.Errorf("approval = %+v, want alice's, applied, of limit-stable", a)
	}
	if earliest, latest := start.Add(time.Hour), time.Now().Add(time.Hour); a.Expires.Before(earliest) || a.Expires.After(latest) {
		t.Errorf("expires = %s, want an hour after the check, between %s and %s", a.Expires, earliest, latest)
	}
	if last := report.Checks[len(report.Checks)-1]; last.Name != "limit-stable" || last.Status != "approved" {
		t.Errorf("last check = %+v, want limit-stable approved", last)
	}
}
