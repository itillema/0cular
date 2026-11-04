package report

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"github.com/itillema/0cular/diff"
	"github.com/itillema/0cular/eval"
)

// diffResult is a typical diff: a budget that needs approval, one whose
// values are secret, and a change of each kind, with more changes than it
// lists.
func diffResult() *diff.Result {
	return &diff.Result{
		Contract:       "bot-features",
		ContractDigest: "sha256:c0ffee0000000000000000000000000000000000000000000000000000000000",
		Baseline:       eval.Artifact{Key: "config/bot-features.json", Size: 921, Digest: "sha256:aa34000000000000000000000000000000000000000000000000000000000000"},
		Candidate:      eval.Artifact{Key: "config/bot-features.json", Size: 1843, Digest: "sha256:bb12cd34ef56000000000000000000000000000000000000000000000000000"},
		Checks: []eval.Check{
			{Kind: eval.KindEnvelope, Name: "baseline", Status: eval.StatusPass, Evidence: "921 B ≤ 5 MiB; valid JSON", Duration: 800},
			{Kind: eval.KindEnvelope, Name: "candidate", Status: eval.StatusPass, Evidence: "1.8 KiB ≤ 5 MiB; valid JSON", Duration: 1250},
			{
				Kind: eval.KindBudget, Name: "feature-count-stable", Status: eval.StatusNeedsApproval, Evidence: "120 → 240 (+100%); budget ±20%",
				Budget: &eval.BudgetValues{Baseline: 120, Candidate: 240, Breached: []string{"maxChangePct"}}, Cost: 4,
			},
			{
				Kind: eval.KindBudget, Name: "signing-key-stable", Status: eval.StatusPass, Evidence: "[redacted] → [redacted]; budget ±0%",
				Budget: &eval.BudgetValues{Baseline: 1, Candidate: 1, Redacted: true}, Cost: 2,
			},
		},
		Compared: true,
		Changes: []diff.Change{
			{Op: diff.Replace, Path: "/features/0/weight", Old: "0.5", New: "0.75"},
			{Op: diff.Add, Path: "/features/-", New: `{"name": "feature-121", "weight": 0.5}`},
			{Op: diff.Remove, Path: "/legacy", Old: "true"},
			{Op: diff.Replace, Path: "/signing/apiToken", Old: "[redacted]", New: "[redacted]"},
		},
		Added: 120, Removed: 1, Replaced: 2,
	}
}

// notComparedResult is a diff whose candidate doesn't decode.
func notComparedResult() *diff.Result {
	return &diff.Result{
		Contract:       "quota-policies",
		ContractDigest: "sha256:c0ffee0000000000000000000000000000000000000000000000000000000000",
		Baseline:       eval.Artifact{Key: "policies.yaml", Size: 677, Digest: "sha256:0be2000000000000000000000000000000000000000000000000000000000000"},
		Candidate:      eval.Artifact{Key: "policies.yaml", Size: 12, Digest: "sha256:c521000000000000000000000000000000000000000000000000000000000000"},
		Checks: []eval.Check{
			{Kind: eval.KindEnvelope, Name: "baseline", Status: eval.StatusPass, Evidence: "677 B ≤ 1 MiB; valid YAML"},
			{Kind: eval.KindEnvelope, Name: "candidate", Status: eval.StatusFail, Evidence: "line 1, column 11: invalid YAML: did not find expected node content"},
			{Kind: eval.KindBudget, Name: "policy-count-stable", Status: eval.StatusSkipped, Evidence: "the candidate didn't decode"},
		},
	}
}

// diffWith returns a diff with s in every string field a report shows.
func diffWith(s string) *diff.Result {
	return &diff.Result{
		Contract:       s,
		ContractDigest: "sha256:" + s,
		Baseline:       eval.Artifact{Key: s, Size: 1, Digest: "sha256:" + s},
		Candidate:      eval.Artifact{Key: s, Size: 2, Digest: "sha256:" + s},
		Checks: []eval.Check{
			{Kind: eval.KindBudget, Name: s, Status: eval.StatusNeedsApproval, Message: s, Evidence: s, Budget: &eval.BudgetValues{Breached: []string{s}}},
			{Kind: eval.KindEnvelope, Name: s, Status: eval.StatusPass, Evidence: s},
		},
		Compared: true,
		Changes: []diff.Change{
			{Op: diff.Replace, Path: s, Old: s, New: s},
			{Op: diff.Add, Path: s, New: s},
			{Op: diff.Remove, Path: s, Old: s},
		},
		Added: 4, Removed: 1, Replaced: 1,
	}
}

// hostileDiff puts each hostile string in a field of its own.
func hostileDiff() *diff.Result {
	h := func(i int) string { return hostile[i%len(hostile)] }
	r := diffWith(h(0))
	r.Baseline.Key, r.Candidate.Key = h(1), h(2)
	for i := range r.Checks {
		c := &r.Checks[i]
		c.Name, c.Message, c.Evidence = h(3*i+3), h(3*i+4), h(3*i+5)
	}
	for i := range r.Changes {
		c := &r.Changes[i]
		c.Path = h(3*i + 9)
		if c.Old != "" {
			c.Old = h(3*i + 10)
		}
		if c.New != "" {
			c.New = h(3*i + 11)
		}
	}
	return r
}

// TestDiffGolden compares each format's rendering of a few diffs with the
// golden files in testdata. Run go test ./report -update to rewrite them,
// then review the diff.
func TestDiffGolden(t *testing.T) {
	for _, tt := range []struct {
		name   string
		result *diff.Result
	}{
		{"diff", diffResult()},
		{"diff-not-compared", notComparedResult()},
		{"diff-hostile", hostileDiff()},
	} {
		for _, f := range Formats() {
			t.Run(tt.name+"/"+string(f), func(t *testing.T) {
				var b bytes.Buffer
				if err := WriteDiff(&b, tt.result, f); err != nil {
					t.Fatal(err)
				}
				file := filepath.Join("testdata", tt.name+"."+extension(f)+".golden")
				if *update {
					if err := os.WriteFile(file, b.Bytes(), 0o600); err != nil {
						t.Fatal(err)
					}
					return
				}
				want, err := os.ReadFile(file)
				if err != nil {
					t.Fatalf("%v; run go test ./report -update to write it", err)
				}
				if diff := cmp.Diff(string(want), b.String()); diff != "" {
					t.Errorf("%s differs (-want +got); if the change is intended, run go test ./report -update:\n%s", file, diff)
				}
			})
		}
	}
}

func TestDescribeChanges(t *testing.T) {
	for _, tt := range []struct {
		r    diff.Result
		want string
	}{
		{diff.Result{}, "Not compared, since a version doesn't decode."},
		{diff.Result{Compared: true}, "No changes."},
		{diff.Result{Compared: true, Replaced: 1}, "1 change: 1 replaced"},
		{diff.Result{Compared: true, Added: 3, Replaced: 1}, "4 changes: 3 added and 1 replaced"},
		{diff.Result{Compared: true, Added: 2, Removed: 2, Replaced: 1}, "5 changes: 2 added, 2 removed and 1 replaced"},
	} {
		if got := describeChanges(&tt.r); got != tt.want {
			t.Errorf("describeChanges(%+v) = %q, want %q", tt.r, got, tt.want)
		}
	}
}

func TestWriteDiffUnknownFormat(t *testing.T) {
	if err := WriteDiff(&bytes.Buffer{}, diffResult(), "html"); err == nil || !strings.Contains(err.Error(), `unknown report format "html"`) {
		t.Errorf("WriteDiff(html) error = %v, want ErrFormat", err)
	}
}

// TestDiffEscaping checks each format's escaping of a diff, with each
// hostile string in every field.
func TestDiffEscaping(t *testing.T) {
	for _, s := range hostile {
		checkDiffEscaping(t, s)
	}
}

// TestDiffEscapingProperties checks the same for random strings, of any
// characters and of any bytes.
func TestDiffEscapingProperties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.OneOf(rapid.String(), rapid.StringOf(rapid.RuneFrom(nil, invisibleRanges...)), rapid.Map(rapid.SliceOf(rapid.Byte()), func(b []byte) string { return string(b) })).Draw(t, "s")
		checkDiffEscaping(t, s)
	})
}

// FuzzDiffEscaping checks each format's escaping of a diff with fuzzed text
// in every field.
func FuzzDiffEscaping(f *testing.F) {
	for _, s := range hostile {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		checkDiffEscaping(t, s)
	})
}

// checkDiffEscaping renders a diff with s in every field, in every format,
// and checks that no field escapes its place.
func checkDiffEscaping(t tester, s string) {
	t.Helper()
	r := diffWith(s)
	render := func(f Format) string {
		var b bytes.Buffer
		if err := WriteDiff(&b, r, f); err != nil {
			t.Fatalf("WriteDiff(%s) error = %v", f, err)
		}
		return b.String()
	}

	// Text: the header, a line for each check, the summary, a line for each
	// change, and the line that counts the rest.
	text := render(Text)
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if want := 1 + len(r.Checks) + 1 + len(r.Changes) + 1; len(lines) != want {
		t.Fatalf("text has %d lines, want %d, for %q:\n%s", len(lines), want, s, text)
	}
	for _, line := range lines {
		if i := strings.IndexFunc(line, invisible); i >= 0 {
			t.Fatalf("text shows %U as itself, for %q:\n%s", []rune(line[i:])[0], s, line)
		}
	}

	// Markdown: each row of both tables keeps its five cell borders; HTML,
	// mentions and references are escaped; nothing is invisible.
	md := render(Markdown)
	rows := 0
	for _, line := range strings.Split(md, "\n") {
		if i := strings.IndexFunc(line, invisible); i >= 0 {
			t.Fatalf("Markdown shows %U as itself, for %q:\n%s", []rune(line[i:])[0], s, line)
		}
		for _, ch := range []byte{'<', '>', '`', '[', ']', '*', '_', '&'} {
			if n := unescaped(line, ch); n > 0 && (ch != '&' || strings.Count(line, zeroWidthSpace) < n) {
				t.Fatalf("Markdown has %d unescaped %q, for %q:\n%s", n, ch, s, line)
			}
		}
		if strings.Count(line, "@") != strings.Count(line, "@"+zeroWidthSpace) {
			t.Fatalf("Markdown has a mention that isn't broken up, for %q:\n%s", s, line)
		}
		if strings.HasPrefix(line, "| ") {
			rows++
			if n := unescaped(line, '|'); n != 5 {
				t.Fatalf("Markdown row has %d cell borders, want 5, for %q:\n%s", n, s, line)
			}
		}
	}
	if want := 2 + len(r.Checks) + 2 + len(r.Changes); rows != want {
		t.Fatalf("Markdown has %d table rows, want %d, for %q:\n%s", rows, want, s, md)
	}

	// JSON: valid, safe to show and to embed, and it keeps s exactly.
	data := render(JSON)
	if i := strings.IndexFunc(data, func(r rune) bool { return invisible(r) && r != '\n' || r == '<' || r == '>' || r == '&' }); i >= 0 {
		t.Fatalf("JSON shows %U as itself, for %q", []rune(data[i:])[0], s)
	}
	var got jsonDiff
	if err := json.Unmarshal([]byte(data), &got); err != nil {
		t.Fatalf("JSON doesn't decode: %v\n%s", err, data)
	}
	want := replaceInvalid(s)
	ops := got.Changes.Operations
	if got.Contract.Name != want || got.Baseline.Key != want || got.Checks[0].Evidence != want || len(ops) != len(r.Changes) ||
		ops[0].Path != want || ops[0].Old != want || ops[1].New != want || got.Changes.Count != r.Count() {
		t.Fatalf("JSON changed %q into %+v", s, got)
	}
}
