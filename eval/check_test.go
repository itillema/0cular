package eval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.yaml.in/yaml/v3"

	"github.com/itillema/0cular/contract"
)

// mustContract parses a contract and sets its schema, as LoadFile would.
func mustContract(t *testing.T, src, schema string) *contract.Contract {
	t.Helper()
	c, err := contract.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if c.Schema != nil {
		c.Schema.Data = []byte(schema)
	}
	return c
}

func mustEvaluator(t *testing.T, c *contract.Contract) *Evaluator {
	t.Helper()
	e, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func mustCheck(t *testing.T, e *Evaluator, candidate string, baseline *string) *Result {
	t.Helper()
	var base *Input
	if baseline != nil {
		base = &Input{Data: []byte(*baseline), Key: "baseline.json"}
	}
	r, err := e.Check(t.Context(), Input{Data: []byte(candidate), Key: "candidate.json"}, base)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// summary keeps the fields of each check that tests compare.
type summary struct {
	Kind     Kind
	Name     string
	Status   Status
	Evidence string
}

func summarize(r *Result) []summary {
	s := make([]summary, len(r.Checks))
	for i, c := range r.Checks {
		s[i] = summary{c.Kind, c.Name, c.Status, c.Evidence}
	}
	return s
}

const ruleContract = `apiVersion: 0cular/v1alpha1
kind: Contract
metadata:
  name: bot-features
artifact:
  format: json
  maxBytes: 1KiB
schema:
  jsonSchema: schemas/bot-features.schema.json
invariants:
  - name: feature-cap
    expr: size(self.features) <= 3
    message: the proxy preallocates 3 features
  - name: weights-in-range
    expr: self.features.all(f, f.weight >= 0.0 && f.weight <= 1.0)
`

const ruleSchema = `{"type": "object", "required": ["features"], "properties": {"features": {"type": "array", "items": {"required": ["name"]}}}}`

func TestCheck(t *testing.T) {
	e := mustEvaluator(t, mustContract(t, ruleContract, ruleSchema))
	tests := []struct {
		name      string
		candidate string
		verdict   Verdict
		want      []summary
	}{
		{
			name:      "pass",
			candidate: `{"features": [{"name": "a", "weight": 0.5}]}`,
			verdict:   Pass,
			want: []summary{
				{KindEnvelope, "candidate", StatusPass, "44 B ≤ 1 KiB; valid JSON"},
				{KindSchema, "schemas/bot-features.schema.json", StatusPass, ""},
				{KindInvariant, "feature-cap", StatusPass, ""},
				{KindInvariant, "weights-in-range", StatusPass, ""},
			},
		},
		{
			name:      "invariants fail",
			candidate: `{"features": [{"name": "a", "weight": 0.5}, {"name": "b", "weight": 2.5}, {"name": "c", "weight": 0}, {"name": "d", "weight": 1}]}`,
			verdict:   Block,
			want: []summary{
				{KindEnvelope, "candidate", StatusPass, "130 B ≤ 1 KiB; valid JSON"},
				{KindSchema, "schemas/bot-features.schema.json", StatusPass, ""},
				{KindInvariant, "feature-cap", StatusFail, "size(self.features) = 4, not <= 3"},
				{KindInvariant, "weights-in-range", StatusFail, "fails for self.features[1]: f.weight = 2.5, not <= 1.0"},
			},
		},
		{
			name:      "schema fails and an invariant can't run",
			candidate: `{"features": [{"weight": 0.5}, {"weight": 0.2}]}`,
			verdict:   Block,
			want: []summary{
				{KindEnvelope, "candidate", StatusPass, "48 B ≤ 1 KiB; valid JSON"},
				{KindSchema, "schemas/bot-features.schema.json", StatusFail, "at /features/0: missing property 'name'; and 1 more"},
				{KindInvariant, "feature-cap", StatusPass, ""},
				{KindInvariant, "weights-in-range", StatusPass, ""},
			},
		},
		{
			name:      "envelope fails",
			candidate: `{"features": [], "features": []}`,
			verdict:   Block,
			want: []summary{
				{KindEnvelope, "candidate", StatusFail, `line 1, column 18: duplicate key "features" in the top level`},
				{KindSchema, "schemas/bot-features.schema.json", StatusSkipped, "the candidate didn't decode"},
				{KindInvariant, "feature-cap", StatusSkipped, "the candidate didn't decode"},
				{KindInvariant, "weights-in-range", StatusSkipped, "the candidate didn't decode"},
			},
		},
		{
			name:      "expression error",
			candidate: `{"features": [{"name": "a"}]}`,
			verdict:   Block,
			want: []summary{
				{KindEnvelope, "candidate", StatusPass, "29 B ≤ 1 KiB; valid JSON"},
				{KindSchema, "schemas/bot-features.schema.json", StatusPass, ""},
				{KindInvariant, "feature-cap", StatusPass, ""},
				{KindInvariant, "weights-in-range", StatusError, "the expression failed: no such key: weight"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := mustCheck(t, e, tt.candidate, nil)
			if r.Verdict != tt.verdict {
				t.Errorf("Verdict = %s, want %s", r.Verdict, tt.verdict)
			}
			if diff := cmp.Diff(tt.want, summarize(r)); diff != "" {
				t.Errorf("checks mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCheckResultDetails(t *testing.T) {
	e := mustEvaluator(t, mustContract(t, ruleContract, ruleSchema))
	baseline := `{"features": []}`
	r := mustCheck(t, e, `{"features": [{"name": "a", "weight": 0.5}]}`, &baseline)
	if r.Contract != "bot-features" || !strings.HasPrefix(r.ContractDigest, "sha256:") {
		t.Errorf("Contract = %q, ContractDigest = %q", r.Contract, r.ContractDigest)
	}
	if r.Candidate.Key != "candidate.json" || r.Candidate.Size != 44 || !strings.HasPrefix(r.Candidate.Digest, "sha256:") {
		t.Errorf("Candidate = %+v", r.Candidate)
	}
	if r.Baseline == nil || r.Baseline.Key != "baseline.json" || r.Baseline.Size != 16 {
		t.Errorf("Baseline = %+v", r.Baseline)
	}
	if got := r.Checks[1]; got.Kind != KindEnvelope || got.Name != "baseline" || got.Status != StatusPass {
		t.Errorf("baseline envelope = %+v", got)
	}
	for _, c := range r.Checks {
		if c.Kind == KindInvariant && c.Cost == 0 {
			t.Errorf("invariant %s reports no cost", c.Name)
		}
	}
}

func TestCheckBadBaseline(t *testing.T) {
	e := mustEvaluator(t, mustContract(t, ruleContract, ruleSchema))
	baseline := `{"features": [}`
	r := mustCheck(t, e, `{"features": []}`, &baseline)
	got := r.Checks[1]
	// encoding/json's error column differs by one between Go releases.
	if r.Verdict != Block || got.Name != "baseline" || got.Status != StatusFail ||
		!strings.HasPrefix(got.Evidence, "the baseline doesn't decode under this contract: line 1, column ") ||
		!strings.Contains(got.Evidence, ": invalid JSON: ") {
		t.Errorf("Verdict = %s, baseline check = %+v", r.Verdict, got)
	}
}

func TestCheckMetaAndBaseline(t *testing.T) {
	c := mustContract(t, `apiVersion: 0cular/v1alpha1
kind: Contract
metadata:
  name: bot-features
artifact:
  format: json
  maxBytes: 1KiB
invariants:
  - name: meta
    expr: meta.bytes == 16 && meta.format == 'json' && meta.key == 'candidate.json' && size(meta.sha256) == 64
  - name: no-shrinking
    expr: baseline == null || size(self.features) >= size(baseline.features)
`, "")
	e := mustEvaluator(t, c)
	if r := mustCheck(t, e, `{"features": []}`, nil); r.Verdict != Pass {
		t.Errorf("without a baseline: %+v", summarize(r))
	}
	baseline := `{"features": [1, 2]}`
	r := mustCheck(t, e, `{"features": []}`, &baseline)
	if want := (summary{KindInvariant, "no-shrinking", StatusFail, "size(self.features) = 0, not >= size(baseline.features) (2)"}); summarize(r)[3] != want {
		t.Errorf("with a baseline: %+v", summarize(r))
	}
}

func TestCheckCostLimit(t *testing.T) {
	c := mustContract(t, `apiVersion: 0cular/v1alpha1
kind: Contract
metadata:
  name: heavy
artifact:
  format: json
  maxBytes: 1KiB
invariants:
  - name: quadratic
    expr: lists.range(5000).all(i, lists.range(5000).all(j, i + j >= 0))
`, "")
	r := mustCheck(t, mustEvaluator(t, c), `{}`, nil)
	got := r.Checks[1]
	if r.Verdict != Block || got.Status != StatusError || !strings.Contains(got.Evidence, "the expression") {
		t.Errorf("Verdict = %s, check = %+v", r.Verdict, got)
	}
}

func TestNewReportsContractProblems(t *testing.T) {
	c := mustContract(t, `apiVersion: 0cular/v1alpha1
kind: Contract
metadata:
  name: broken
artifact:
  format: json
  maxBytes: 1KiB
schema:
  jsonSchema: s.json
invariants:
  - name: typo
    expr: size(selff.features) <= 3
  - name: not-a-boolean
    expr: size(self.features)
`, `{"type": 5}`)
	c.Path = "contracts/broken.yaml"
	_, err := New(c)
	var cerr *contract.Error
	if !errors.As(err, &cerr) || !errors.Is(err, contract.ErrInvalid) {
		t.Fatalf("New() error = %v, want a *contract.Error", err)
	}
	lines := strings.Split(err.Error(), "\n")
	want := []string{
		"contracts/broken.yaml:9:15: schema.jsonSchema: invalid JSON Schema: s.json: at /type: ",
		"contracts/broken.yaml:12:11: invariants[0].expr: at 1:6 in the expression: undeclared reference to 'selff'",
		"contracts/broken.yaml:14:11: invariants[1].expr: the expression returns a number (int), but it must return a boolean",
	}
	if len(lines) != len(want) {
		t.Fatalf("New() error has %d lines, want %d:\n%v", len(lines), len(want), err)
	}
	for i := range want {
		if !strings.HasPrefix(lines[i], want[i]) {
			t.Errorf("line %d = %q, want it to start with %q", i, lines[i], want[i])
		}
	}
}

func TestCheckStopsWithTheContext(t *testing.T) {
	e := mustEvaluator(t, mustContract(t, ruleContract, ruleSchema))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.Check(ctx, Input{Data: []byte(`{"features": []}`)}, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("Check() with a canceled context = %v, want context.Canceled", err)
	}
}

func TestCheckConcurrently(t *testing.T) {
	e := mustEvaluator(t, mustContract(t, ruleContract, ruleSchema))
	want := summarize(mustCheck(t, e, `{"features": [{"name": "a", "weight": 2}]}`, nil))
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				r, err := e.Check(t.Context(), Input{Data: []byte(`{"features": [{"name": "a", "weight": 2}]}`), Key: "candidate.json"}, nil)
				if err != nil {
					t.Error(err)
					return
				}
				if diff := cmp.Diff(want, summarize(r)); diff != "" {
					t.Errorf("concurrent checks disagree (-want +got):\n%s", diff)
					return
				}
			}
		})
	}
	wg.Wait()
}

// replayExpectation is the check stage of a replay scenario's
// expected.yaml.
type replayExpectation struct {
	Stages struct {
		Check struct {
			Verdict Verdict `yaml:"verdict"`
			Checks  []struct {
				Kind   Kind   `yaml:"kind"`
				Name   string `yaml:"name"`
				Status Status `yaml:"status"`
			} `yaml:"checks"`
		} `yaml:"check"`
	} `yaml:"stages"`
}

// TestCheckReplay checks the replay suite's scenarios against the check
// stage of their expected.yaml: the verdict, and every check that doesn't
// pass, with evidence that shows why. Each baseline passes on its own.
func TestCheckReplay(t *testing.T) {
	evidence := map[string]string{
		"every-policy-has-a-limit": "fails for self.policies[8]: p.limit = null, not != null",
		"feature-cap":              "size(self.features) = 240, not <= 200",
		"feature-count-stable":     "120 → 240 (+100%); budget ±20%",
		"addresses-never-empty":    "size(self.addresses) = 0, not >= 1",
	}
	files, err := filepath.Glob("../replay/*/contract.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		dir := filepath.Dir(file)
		t.Run(filepath.Base(dir), func(t *testing.T) {
			c, err := contract.LoadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "expected.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var expect replayExpectation
			if err := yaml.Unmarshal(raw, &expect); err != nil {
				t.Fatal(err)
			}
			e := mustEvaluator(t, c)
			read := func(version string) Input {
				path := filepath.Join(dir, version+"."+string(c.Artifact.Format))
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				return Input{Data: data, Key: path}
			}
			candidate, baseline := read("candidate"), read("baseline")
			r, err := e.Check(t.Context(), baseline, nil)
			if err != nil || r.Verdict != Pass {
				t.Fatalf("the baseline alone: %v, %+v", err, r)
			}
			if r, err = e.Check(t.Context(), candidate, &baseline); err != nil {
				t.Fatal(err)
			}
			if r.Verdict != expect.Stages.Check.Verdict {
				t.Errorf("Verdict = %s, want %s", r.Verdict, expect.Stages.Check.Verdict)
			}
			var want, got []summary
			for _, c := range expect.Stages.Check.Checks {
				want = append(want, summary{c.Kind, c.Name, c.Status, evidence[c.Name]})
			}
			for _, s := range summarize(r) {
				if s.Status != StatusPass {
					got = append(got, s)
				}
			}
			if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("checks that don't pass (-expected.yaml +got):\n%s", diff)
			}
		})
	}
}

func TestVerdict(t *testing.T) {
	for _, tt := range []struct {
		statuses []Status
		want     Verdict
	}{
		{nil, Pass},
		{[]Status{StatusPass, StatusSkipped}, Pass},
		{[]Status{StatusPass, StatusNeedsApproval}, NeedsApproval},
		{[]Status{StatusNeedsApproval, StatusFail}, Block},
		{[]Status{StatusError, StatusPass}, Block},
	} {
		checks := make([]Check, len(tt.statuses))
		for i, s := range tt.statuses {
			checks[i].Status = s
		}
		if got := verdict(checks); got != tt.want {
			t.Errorf("verdict(%v) = %s, want %s", tt.statuses, got, tt.want)
		}
	}
}

func TestFormatSize(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0 B", 512: "512 B", 1023: "1023 B", 1024: "1 KiB", 1536: "1.5 KiB",
		1887436: "1.7 MiB", 5 << 20: "5 MiB", 5<<20 - 1: "4.9 MiB", 3 << 30: "3 GiB",
	} {
		if got := FormatSize(n); got != want {
			t.Errorf("FormatSize(%d) = %q, want %q", n, got, want)
		}
	}
}
