package main

import (
	"bytes"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/itillema/0cular/eval"
	"github.com/itillema/0cular/replay"
)

func TestReplayRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout []string // substrings; none means stdout must be empty
		wantStderr string   // a substring; empty means stderr must be empty
	}{
		{
			name: "every scenario", args: []string{"replay", "run"}, wantCode: exitPass,
			wantStdout: []string{
				"blank-policy-fields: Google Cloud",
				"doubled-feature-file: Cloudflare outage on November 18, 2025\nhttps://blog.cloudflare.com/18-november-2025-outage/\n\n0cular check  bot-features",
				"As expected: BLOCK, with feature-cap failing and feature-count-stable needing approval.",
				"emptied-record: Summary of the Amazon DynamoDB service disruption",
				"3 of 3 scenarios went as expected.",
			},
		},
		{
			name: "one scenario", args: []string{"replay", "run", "emptied-record"}, wantCode: exitPass,
			wantStdout: []string{"As expected: BLOCK, with addresses-never-empty failing.", "1 of 1 scenarios went as expected."},
		},
		{
			name: "unknown scenario", args: []string{"replay", "run", "nope"}, wantCode: exitUsage,
			wantStderr: `there's no scenario "nope"; the scenarios are blank-policy-fields, doubled-feature-file, emptied-record`,
		},
		{name: "no subcommand", args: []string{"replay"}, wantCode: exitUsage, wantStderr: "Usage:"},
		{name: "unknown subcommand", args: []string{"replay", "nope"}, wantCode: exitUsage, wantStderr: `unknown command "nope" for "0cular replay"`},
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

// TestReplayRunUnexpected checks the output for a scenario whose outcome
// isn't the one it expects.
func TestReplayRunUnexpected(t *testing.T) {
	expected := `schemaVersion: 1
scenario: zero-limit
postmortem: {title: A synthetic outage, url: "https://postmortem.example/zero-limit"}
artifact: {format: json, baseline: baseline.json, candidate: candidate.json}
stages:
  check: {verdict: PASS}
`
	scenarios, err := replay.Load(fstest.MapFS{
		"zero-limit/expected.yaml":  {Data: []byte(expected)},
		"zero-limit/contract.yaml":  {Data: []byte(checkContract)},
		"zero-limit/baseline.json":  {Data: []byte(`{"limit": 100}`)},
		"zero-limit/candidate.json": {Data: []byte(`{"limit": 0}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = replayRun(t.Context(), &out, scenarios, nil)
	if exitErr := (*exitError)(nil); !errors.As(err, &exitErr) || exitErr.code != exitBlock {
		t.Errorf("replayRun() error = %v, want exit code %d", err, exitBlock)
	}
	for _, want := range []string{
		"zero-limit: A synthetic outage\nhttps://postmortem.example/zero-limit\n\n0cular check  limits",
		"\nNot as expected:\n  - the verdict is BLOCK, not PASS\n" +
			"  - the checks that don't pass are [invariant limit-positive fail; budget limit-stable needs-approval], not none\n",
		"\n1 of 1 scenarios didn't go as expected.\n",
	} {
		checkOutput(t, "output", out.String(), want)
	}
}

func TestDescribeStage(t *testing.T) {
	tests := []struct {
		stage replay.CheckStage
		want  string
	}{
		{stage: replay.CheckStage{Verdict: eval.Pass}, want: "PASS"},
		{
			stage: replay.CheckStage{Verdict: eval.Block, Checks: []replay.CheckOutcome{{Name: "cap", Status: eval.StatusFail}}},
			want:  "BLOCK, with cap failing",
		},
		{
			stage: replay.CheckStage{Verdict: eval.Block, Checks: []replay.CheckOutcome{
				{Name: "decode", Status: eval.StatusError},
				{Name: "cap", Status: eval.StatusFail},
				{Name: "count", Status: eval.StatusNeedsApproval},
			}},
			want: "BLOCK, with decode (error), cap failing and count needing approval",
		},
	}
	for _, tt := range tests {
		if got := describeStage(tt.stage); got != tt.want {
			t.Errorf("describeStage(%+v) = %q, want %q", tt.stage, got, tt.want)
		}
	}
}
