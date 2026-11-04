package diff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/wI2L/jsondiff"
	"pgregory.net/rapid"

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/eval"
)

// plainContract has no rules, so that only the patch matters.
const plainContract = `apiVersion: 0cular/v1alpha1
kind: Contract
metadata: {name: plain}
artifact: {format: json, maxBytes: 1MiB}
`

// tester is what the helpers need of a test: a *testing.T, a *testing.F or
// a *rapid.T.
type tester interface {
	Helper()
	Fatal(args ...any)
}

// mustDiffer returns a Differ for the contract in text.
func mustDiffer(t tester, text string) *Differ {
	t.Helper()
	c, err := contract.Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// compare compares two versions given as text.
func compare(t tester, d *Differ, baseline, candidate string) *Result {
	t.Helper()
	r, err := d.Compare(context.Background(), eval.Input{Data: []byte(baseline), Key: "baseline.json"}, eval.Input{Data: []byte(candidate), Key: "candidate.json"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCompare(t *testing.T) {
	d := mustDiffer(t, plainContract)
	tests := []struct {
		name                string
		baseline, candidate string
		want                []Change
	}{
		{name: "no change", baseline: `{"a": [1, {"b": null}]}`, candidate: `{"a": [1, {"b": null}]}`},
		{name: "replace", baseline: `{"limit": 100}`, candidate: `{"limit": 0}`, want: []Change{{Op: Replace, Path: "/limit", Old: "100", New: "0"}}},
		{name: "add", baseline: `{"a": 1}`, candidate: `{"a": 1, "b": "x"}`, want: []Change{{Op: Add, Path: "/b", New: `"x"`}}},
		{name: "remove", baseline: `{"a": 1, "b": [true]}`, candidate: `{"a": 1}`, want: []Change{{Op: Remove, Path: "/b", Old: "[true]"}}},
		{
			name: "append", baseline: `{"xs": [1]}`, candidate: `{"xs": [1, 2.5, "3"]}`,
			want: []Change{{Op: Add, Path: "/xs/-", New: "2.5"}, {Op: Add, Path: "/xs/-", New: `"3"`}},
		},
		{
			// Each remove takes out the element after the last one kept.
			name: "shrink", baseline: `[1, 2, 3]`, candidate: `[1]`,
			want: []Change{{Op: Remove, Path: "/1", Old: "2"}, {Op: Remove, Path: "/1", Old: "3"}},
		},
		{
			name: "nested", baseline: `{"policies": [{"id": "a", "limit": 5}]}`, candidate: `{"policies": [{"id": "a", "limit": null}]}`,
			want: []Change{{Op: Replace, Path: "/policies/0/limit", Old: "5", New: "null"}},
		},
		{
			name: "keys that need escaping", baseline: `{"a/b": 1, "c~d": 2}`, candidate: `{"a/b": 3, "c~d": 4}`,
			want: []Change{{Op: Replace, Path: "/a~1b", Old: "1", New: "3"}, {Op: Replace, Path: "/c~0d", Old: "2", New: "4"}},
		},
		{
			name: "another type", baseline: `{"a": 1}`, candidate: `["a", 1]`,
			want: []Change{{Op: Replace, Path: "", Old: `{"a": 1}`, New: `["a", 1]`}},
		},
		{
			name: "long value", baseline: `{"a": ""}`, candidate: `{"a": "` + strings.Repeat("x", 200) + `"}`,
			want: []Change{{Op: Replace, Path: "/a", Old: `""`, New: `"` + strings.Repeat("x", 78) + "…"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := compare(t, d, tt.baseline, tt.candidate)
			if !r.Compared {
				t.Fatalf("Compared = false; checks: %+v", r.Checks)
			}
			if diff := cmp.Diff(tt.want, r.Changes); diff != "" {
				t.Errorf("Changes differ (-want +got):\n%s", diff)
			}
			if r.Count() != len(tt.want) {
				t.Errorf("Count() = %d, want %d", r.Count(), len(tt.want))
			}
		})
	}
}

func TestCompareCounts(t *testing.T) {
	d := mustDiffer(t, plainContract)
	r := compare(t, d, `{"a": 1, "b": 2, "c": [1, 2]}`, `{"a": 9, "c": [1], "d": 4, "e": 5}`)
	if r.Added != 2 || r.Removed != 2 || r.Replaced != 1 || r.Count() != 5 {
		t.Errorf("counts = %d added, %d removed, %d replaced, %d in all; want 2, 2, 1 and 5", r.Added, r.Removed, r.Replaced, r.Count())
	}
}

// TestCompareMaxChanges checks that a result lists at most MaxChanges
// changes, and counts all of them.
func TestCompareMaxChanges(t *testing.T) {
	d := mustDiffer(t, plainContract)
	var base, cand []string
	for i := range 3 * MaxChanges {
		base = append(base, strconv.Itoa(i))
		cand = append(cand, strconv.Itoa(-i-1))
	}
	r := compare(t, d, "["+strings.Join(base, ",")+"]", "["+strings.Join(cand, ",")+"]")
	if len(r.Changes) != MaxChanges || r.Replaced != 3*MaxChanges || r.Count() != 3*MaxChanges {
		t.Errorf("got %d changes, %d replaced; want %d changes and %d replaced", len(r.Changes), r.Replaced, MaxChanges, 3*MaxChanges)
	}
	if last := r.Changes[MaxChanges-1]; last.Path != "/"+strconv.Itoa(MaxChanges-1) {
		t.Errorf("the last change listed is at %s, want the patch's first %d in order", last.Path, MaxChanges)
	}
}

// TestCompareRedaction checks that no value read through a field that holds
// a secret shows (ADR-0015): neither at a path through such a field, nor in
// a value that holds one.
func TestCompareRedaction(t *testing.T) {
	d := mustDiffer(t, plainContract+"report: {redact: [pin]}\n")
	tests := []struct {
		name                string
		baseline, candidate string
		want                []Change
	}{
		{
			name: "a path through a secret", baseline: `{"auth": {"apiToken": "old-secret"}}`, candidate: `{"auth": {"apiToken": "new-secret"}}`,
			want: []Change{{Op: Replace, Path: "/auth/apiToken", Old: "[redacted]", New: "[redacted]"}},
		},
		{
			name: "a value that holds a secret", baseline: `{}`, candidate: `{"db": {"host": "db.example", "password": "hunter2"}}`,
			want: []Change{{Op: Add, Path: "/db", New: `{"host": "db.example", "password": [redacted]}`}},
		},
		{
			name: "deeper", baseline: `{"users": []}`, candidate: `{"users": [{"name": "a", "keys": {"private_key": "k-secret"}}]}`,
			want: []Change{{Op: Add, Path: "/users/-", New: `{"keys": {"private_key": [redacted]}, "name": "a"}`}},
		},
		{
			name: "an array under a secret", baseline: `{"tokens": ["t-secret"]}`, candidate: `{"tokens": []}`,
			want: []Change{{Op: Remove, Path: "/tokens/0", Old: "[redacted]"}},
		},
		{
			name: "the contract's pattern", baseline: `{"pin": "1234"}`, candidate: `{"pin": "5678"}`,
			want: []Change{{Op: Replace, Path: "/pin", Old: "[redacted]", New: "[redacted]"}},
		},
		{
			name: "a secret replaced at the root", baseline: `{"secret": "s-secret"}`, candidate: `[]`,
			want: []Change{{Op: Replace, Path: "", Old: `{"secret": [redacted]}`, New: "[]"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := compare(t, d, tt.baseline, tt.candidate)
			if diff := cmp.Diff(tt.want, r.Changes); diff != "" {
				t.Errorf("Changes differ (-want +got):\n%s", diff)
			}
			for _, c := range r.Changes {
				for _, secret := range []string{"-secret", "hunter2", "1234", "5678"} {
					if strings.Contains(c.Old+c.New, secret) {
						t.Errorf("%+v shows %s", c, secret)
					}
				}
			}
		})
	}
}

// TestCompareMeasures checks that a result has the contract's diff budgets,
// measured from the baseline to the candidate.
func TestCompareMeasures(t *testing.T) {
	d := mustDiffer(t, plainContract+"diffBudgets:\n  - {name: limit-stable, measure: self.limit, maxChangePct: 50}\n")
	r := compare(t, d, `{"limit": 100}`, `{"limit": 200}`)
	if len(r.Checks) != 3 {
		t.Fatalf("got %d checks, want the two envelopes and the budget: %+v", len(r.Checks), r.Checks)
	}
	if b := r.Checks[0]; b.Kind != eval.KindEnvelope || b.Name != "baseline" || b.Status != eval.StatusPass {
		t.Errorf("Checks[0] = %+v, want the baseline's envelope, passed", b)
	}
	budget := r.Checks[2]
	if budget.Kind != eval.KindBudget || budget.Status != eval.StatusNeedsApproval || budget.Budget == nil ||
		budget.Budget.Baseline != 100 || budget.Budget.Candidate != 200 {
		t.Errorf("Checks[2] = %+v, want limit-stable needing approval, 100 → 200", budget)
	}
}

// TestCompareUndecodable checks that versions that don't decode aren't
// compared, and that the envelope checks say why.
func TestCompareUndecodable(t *testing.T) {
	d := mustDiffer(t, plainContract+"diffBudgets:\n  - {name: limit-stable, measure: self.limit, maxChangePct: 50}\n")
	for _, tt := range []struct {
		name, baseline, candidate string
		failing                   int // the check that fails
		evidence                  string
	}{
		{name: "candidate", baseline: `{"limit": 1}`, candidate: `{"limit": `, failing: 1, evidence: "the text ends before the value does"},
		{name: "baseline", baseline: `[1,]`, candidate: `{"limit": 1}`, failing: 0, evidence: "the baseline doesn't decode under this contract: "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := compare(t, d, tt.baseline, tt.candidate)
			if r.Compared || len(r.Changes) != 0 || r.Count() != 0 {
				t.Errorf("Compared = %v with %d changes, want neither", r.Compared, r.Count())
			}
			if c := r.Checks[tt.failing]; c.Status != eval.StatusFail || !strings.Contains(c.Evidence, tt.evidence) {
				t.Errorf("Checks[%d] = %s %q, want fail with %q", tt.failing, c.Status, c.Evidence, tt.evidence)
			}
			if c := r.Checks[2]; c.Status != eval.StatusSkipped {
				t.Errorf("the budget is %s, want skipped", c.Status)
			}
		})
	}

	// A version too large to decode is read only in part.
	big := `{"pad": "` + strings.Repeat("x", 2<<20) + `"}`
	in, err := eval.ReadInput(t.Context(), strings.NewReader(big), "big.json", contract.Artifact{MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	r, err := d.Compare(t.Context(), eval.Input{Data: []byte(`{}`)}, in)
	if err != nil {
		t.Fatal(err)
	}
	if c := r.Checks[1]; r.Compared || c.Status != eval.StatusFail || !strings.Contains(c.Evidence, "more than maxBytes") {
		t.Errorf("Compare(too large) = %v, %s %q; want it not compared, as too large", r.Compared, c.Status, c.Evidence)
	}
}

func TestCompareYAML(t *testing.T) {
	d := mustDiffer(t, strings.Replace(plainContract, "format: json", "format: yaml", 1))
	r := compare(t, d, "replicas: 3\nregions: [a, b]\n", "replicas: 5\nregions: [a]\n")
	want := []Change{{Op: Remove, Path: "/regions/1", Old: `"b"`}, {Op: Replace, Path: "/replicas", Old: "3", New: "5"}}
	if diff := cmp.Diff(want, r.Changes); diff != "" {
		t.Errorf("Changes differ (-want +got):\n%s", diff)
	}
}

func TestCompareCanceled(t *testing.T) {
	d := mustDiffer(t, plainContract)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := d.Compare(ctx, eval.Input{Data: []byte(`{}`)}, eval.Input{Data: []byte(`[]`)}); !errors.Is(err, context.Canceled) {
		t.Errorf("Compare with a canceled context error = %v, want context.Canceled", err)
	}
}

// TestCompareConcurrent checks that one Differ compares from many
// goroutines at once; go test -race checks for data races.
func TestCompareConcurrent(t *testing.T) {
	d := mustDiffer(t, plainContract+"diffBudgets:\n  - {name: count, measure: size(self.xs), maxChangePct: 50}\n")
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			cand := fmt.Sprintf(`{"xs": [%d, 2], "token": "t%d"}`, i, i)
			r, err := d.Compare(t.Context(), eval.Input{Data: []byte(`{"xs": [-1]}`)}, eval.Input{Data: []byte(cand)})
			if err != nil || r.Count() != 3 {
				t.Errorf("Compare() = %v, %v; want 3 changes", r, err)
			}
		})
	}
	wg.Wait()
}

func TestNew(t *testing.T) {
	c, err := contract.Parse([]byte(plainContract + "invariants:\n  - {name: bad, expr: \"selff.a > 0\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	var ce *contract.Error
	if _, err := New(c); !errors.As(err, &ce) {
		t.Errorf("New(an expression that doesn't compile) error = %v, want a *contract.Error", err)
	}
}

// TestMakePatchInvalid checks that a value of a type JSON doesn't have is an
// error, not a panic. eval never decodes one.
func TestMakePatchInvalid(t *testing.T) {
	if _, err := makePatch(map[string]any{"a": 1}, map[string]any{"a": make(chan int)}); err == nil {
		t.Error("makePatch(a channel) = nil error, want an error")
	}
}

// TestCompareProperties checks Compare on random versions: the raw patch
// turns the baseline into the candidate, the result counts and lists its
// operations, and no secret shows.
func TestCompareProperties(t *testing.T) {
	d := mustDiffer(t, plainContract)
	rapid.Check(t, func(rt *rapid.T) {
		base := hideSecrets(jsonValue(3).Draw(rt, "baseline"))
		cand := hideSecrets(jsonValue(3).Draw(rt, "candidate"))
		baseJSON, candJSON := mustJSON(rt, base), mustJSON(rt, cand)

		patch, err := makePatch(decoded(rt, baseJSON), decoded(rt, candJSON))
		if err != nil {
			rt.Fatal(err)
		}
		applied, err := apply(decoded(rt, baseJSON), patch)
		if err != nil {
			rt.Fatalf("applying the patch: %v\npatch: %v", err, patch)
		}
		if want := decoded(rt, candJSON); !reflect.DeepEqual(applied, want) {
			rt.Fatalf("the patch makes %v, not the candidate %v\npatch: %v", applied, want, patch)
		}

		r := compare(rt, d, string(baseJSON), string(candJSON))
		if !r.Compared || r.Count() != len(patch) || len(r.Changes) != min(len(patch), MaxChanges) {
			rt.Fatalf("Compared %v, Count() %d, %d changes; want true, %d and %d", r.Compared, r.Count(), len(r.Changes), len(patch), min(len(patch), MaxChanges))
		}
		for _, c := range r.Changes {
			if strings.Contains(c.Old+c.New, secretValue) {
				rt.Fatalf("%+v shows a secret", c)
			}
			if c.Path != "" && !strings.HasPrefix(c.Path, "/") {
				rt.Fatalf("%+v has a path that isn't a JSON Pointer", c)
			}
		}
	})
}

// FuzzCompare checks that Compare handles any pair of versions: it returns
// no error, and its result is consistent.
func FuzzCompare(f *testing.F) {
	f.Add(`{"a": 1}`, `{"a": 2, "b": [1, 2]}`)
	f.Add(`[1, 2, 3]`, `[3, 2]`)
	f.Add(`{"token": "x", "a/b~c": {"d": null}}`, `{"token": {"y": 1}, "a/b~c": []}`)
	f.Add(`"text"`, `{`)
	d := mustDiffer(f, plainContract)
	f.Fuzz(func(t *testing.T, baseline, candidate string) {
		r := compare(t, d, baseline, candidate)
		if len(r.Changes) > MaxChanges || len(r.Changes) > r.Count() || (!r.Compared && r.Count() != 0) {
			t.Fatalf("Compared %v, Count() %d, %d changes", r.Compared, r.Count(), len(r.Changes))
		}
	})
}

// secretValue is what hideSecrets puts under every field that holds a
// secret.
const secretValue = "s3cret-value"

// jsonValue generates JSON values nested up to depth deep, whose keys need
// escaping in JSON Pointers and include fields that hold secrets.
func jsonValue(depth int) *rapid.Generator[any] {
	scalar := rapid.OneOf(
		rapid.Just[any](nil),
		rapid.Map(rapid.Bool(), func(b bool) any { return b }),
		rapid.Map(rapid.IntRange(-2, 2), func(i int) any { return float64(i) }),
		rapid.Map(rapid.SampledFrom([]string{"", "a", "b"}), func(s string) any { return s }),
	)
	if depth == 0 {
		return scalar
	}
	keys := rapid.SampledFrom([]string{"a", "b", "a/b", "c~d", "token", "Password"})
	return rapid.OneOf(
		scalar,
		rapid.Map(rapid.SliceOfN(jsonValue(depth-1), 0, 4), func(v []any) any { return v }),
		rapid.Map(rapid.MapOfN(keys, jsonValue(depth-1), 0, 4), func(m map[string]any) any { return m }),
	)
}

// hideSecrets replaces the value of every field that holds a secret with
// secretValue, so that a test can look for it.
func hideSecrets(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, child := range v {
			if k == "token" || k == "Password" {
				v[k] = secretValue
			} else {
				v[k] = hideSecrets(child)
			}
		}
	case []any:
		for i, child := range v {
			v[i] = hideSecrets(child)
		}
	}
	return v
}

func mustJSON(t *rapid.T, v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// decoded returns a fresh copy of the value that data encodes.
func decoded(t *rapid.T, data []byte) any {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// apply applies a patch's add, remove and replace operations to doc, in
// order, as RFC 6902 describes them.
func apply(doc any, patch jsondiff.Patch) (any, error) {
	for _, op := range patch {
		if op.Path == "" {
			if op.Type != jsondiff.OperationAdd && op.Type != jsondiff.OperationReplace {
				return nil, fmt.Errorf("%s at the root", op.Type)
			}
			doc = op.Value
			continue
		}
		var tokens []string
		for _, token := range strings.Split(op.Path[1:], "/") {
			tokens = append(tokens, strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~"))
		}
		var err error
		if doc, err = applyAt(doc, tokens, op); err != nil {
			return nil, fmt.Errorf("%s %s: %w", op.Type, op.Path, err)
		}
	}
	return doc, nil
}

// applyAt applies op at the place that tokens lead to from v, and returns
// v as it then is.
func applyAt(v any, tokens []string, op jsondiff.Operation) (any, error) {
	token := tokens[0]
	switch c := v.(type) {
	case map[string]any:
		_, exists := c[token]
		switch {
		case len(tokens) > 1:
			child, err := applyAt(c[token], tokens[1:], op)
			c[token] = child
			return c, err
		case op.Type == jsondiff.OperationAdd:
			c[token] = op.Value
		case !exists:
			return nil, fmt.Errorf("no member %q", token)
		case op.Type == jsondiff.OperationRemove:
			delete(c, token)
		case op.Type == jsondiff.OperationReplace:
			c[token] = op.Value
		default:
			return nil, fmt.Errorf("unexpected operation %s", op.Type)
		}
		return c, nil
	case []any:
		if token == "-" && len(tokens) == 1 && op.Type == jsondiff.OperationAdd {
			return append(c, op.Value), nil
		}
		i, err := strconv.Atoi(token)
		if err != nil || i < 0 || i > len(c) || (i == len(c) && op.Type != jsondiff.OperationAdd) {
			return nil, fmt.Errorf("no index %q in an array of %d", token, len(c))
		}
		switch {
		case len(tokens) > 1:
			child, err := applyAt(c[i], tokens[1:], op)
			c[i] = child
			return c, err
		case op.Type == jsondiff.OperationAdd:
			return append(c[:i], append([]any{op.Value}, c[i:]...)...), nil
		case op.Type == jsondiff.OperationRemove:
			return append(c[:i], c[i+1:]...), nil
		case op.Type == jsondiff.OperationReplace:
			c[i] = op.Value
			return c, nil
		}
		return nil, fmt.Errorf("unexpected operation %s", op.Type)
	}
	return nil, fmt.Errorf("%q is in a %T, not an object or array", token, v)
}
