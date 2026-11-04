package eval

import (
	"context"
	"errors"
	"testing"
)

// measureContract has an invariant, which Measure skips, and a budget.
const measureContract = "apiVersion: 0cular/v1alpha1\nkind: Contract\nmetadata: {name: limits}\nartifact: {format: json, maxBytes: 1KiB}\n" +
	"invariants:\n  - {name: limit-positive, expr: \"self.limit > 0\"}\n" +
	"diffBudgets:\n  - {name: limit-stable, measure: self.limit, maxChangePct: 50}\n"

func TestMeasure(t *testing.T) {
	e := mustEvaluator(t, mustContract(t, measureContract, ""))
	m, err := e.Measure(t.Context(), Input{Data: []byte(`{"limit": 100}`), Key: "a.json"}, Input{Data: []byte(`{"limit": 0}`), Key: "b.json"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Contract != "limits" || m.Baseline.Key != "a.json" || m.Candidate.Key != "b.json" || m.Candidate.Digest != digestOf(`{"limit": 0}`) {
		t.Errorf("Measure() = %+v, want the contract's name and both versions", m)
	}
	var got []string
	for _, c := range m.Checks {
		got = append(got, string(c.Kind)+" "+c.Name+" "+string(c.Status))
	}
	want := []string{"envelope baseline pass", "envelope candidate pass", "budget limit-stable needs-approval"}
	if len(got) != len(want) {
		t.Fatalf("checks = %q, want %q: no schema or invariants", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("checks = %q, want %q", got, want)
			break
		}
	}
	if m.BaselineDoc == nil || m.CandidateDoc == nil || m.CandidateDoc.Value.(map[string]any)["limit"] != 0.0 {
		t.Errorf("documents = %v, %v; want both decoded", m.BaselineDoc, m.CandidateDoc)
	}
}

// TestMeasureUndecodable checks that a version that doesn't decode has no
// document, and that the budgets say which version didn't decode.
func TestMeasureUndecodable(t *testing.T) {
	e := mustEvaluator(t, mustContract(t, measureContract, ""))
	for _, tt := range []struct {
		name, baseline, candidate, skip string
	}{
		{"baseline", `{`, `{"limit": 1}`, "the baseline didn't decode"},
		{"candidate", `{"limit": 1}`, `{`, "the candidate didn't decode"},
		{"both", `{`, `{`, "the candidate didn't decode"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, err := e.Measure(t.Context(), Input{Data: []byte(tt.baseline)}, Input{Data: []byte(tt.candidate)})
			if err != nil {
				t.Fatal(err)
			}
			if (m.BaselineDoc == nil) != (tt.baseline == "{") || (m.CandidateDoc == nil) != (tt.candidate == "{") {
				t.Errorf("documents = %v, %v; want nil only for the version that doesn't decode", m.BaselineDoc, m.CandidateDoc)
			}
			if b := m.Checks[2]; b.Status != StatusSkipped || b.Evidence != tt.skip {
				t.Errorf("budget = %s %q, want skipped: %q", b.Status, b.Evidence, tt.skip)
			}
		})
	}
}

func TestMeasureCanceled(t *testing.T) {
	e := mustEvaluator(t, mustContract(t, measureContract, ""))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.Measure(ctx, Input{Data: []byte(`{}`)}, Input{Data: []byte(`{}`)}); !errors.Is(err, context.Canceled) {
		t.Errorf("Measure with a canceled context error = %v, want context.Canceled", err)
	}
}
