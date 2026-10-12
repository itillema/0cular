package celenv

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/cel-go/checker"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"pgregory.net/rapid"
)

// candidate is a decoded artifact, shaped as eval.Decode returns it.
var candidate = map[string]any{
	"limit": 6000.0,
	"features": []any{
		map[string]any{"name": "ua-entropy", "weight": 1.5},
		map[string]any{"name": "ja4", "weight": 0.25},
	},
	"policies": []any{
		map[string]any{"id": "compute", "limit": 6000.0},
		map[string]any{"id": "storage", "limit": nil},
	},
}

func vars() map[string]any {
	return map[string]any{
		VarSelf:     candidate,
		VarBaseline: nil,
		VarMeta:     Meta{Bytes: 1234, SHA256: "ab12", Format: "json", Key: "bot-features"}.Value(),
	}
}

func newEnv(t *testing.T, kind Kind, limits Limits) *Env {
	t.Helper()
	env, err := New(kind, limits)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestRules(t *testing.T) {
	env := newEnv(t, Rules, Limits{})
	tests := []struct {
		expr string
		want bool
	}{
		{"size(self.features) <= 200", true},
		// A decoded number is a double; cross-type comparisons let it meet an int.
		{"self.limit > 0 && self.limit == 6000", true},
		{"self.policies.all(p, has(p.limit) && p.limit != null && p.limit > 0)", false},
		{"baseline == null", true},
		{"meta.bytes > 1000 && meta.format == 'json' && meta.key == 'bot-features' && meta.sha256 == 'ab12'", true},
		{"has(meta.key) && size(meta) == 4", true},
		// A loop variable called meta hides the variable, so its fields aren't meta's.
		{"[{'x': 1}].all(meta, meta.x == 1) && [1].map(meta, meta + 1) == [2]", true},
		{"zerocular.unique(self.features.map(f, f.name))", true},
		{"zerocular.unique(self.policies.map(p, p.limit))", true},
		// The extensions, at their pinned versions.
		{"'a,b'.split(',') == ['a', 'b'] && 'x'.upperAscii() == 'X'", true},
		{"[3, 1, 2].sort() == [1, 2, 3] && lists.range(3) == [0, 1, 2] && [1, 1].distinct() == [1]", true},
		{"sets.contains([1, 2], [2]) && sets.intersects([1], [1, 3])", true},
		{"math.greatest(1, 5.5) == 5.5 && math.abs(-2) == 2", true},
	}
	for _, tt := range tests {
		prg, err := env.Compile(tt.expr, Bool)
		if err != nil {
			t.Errorf("Compile(%q) error = %v", tt.expr, err)
			continue
		}
		got, cost, err := prg.Eval(t.Context(), vars())
		if err != nil {
			t.Errorf("Eval(%q) error = %v", tt.expr, err)
			continue
		}
		if got != types.Bool(tt.want) || cost == 0 {
			t.Errorf("Eval(%q) = %v, cost %d; want %v and a cost", tt.expr, got, cost, tt.want)
		}
	}
}

func TestMeasures(t *testing.T) {
	env := newEnv(t, Rules, Limits{})
	for expr, want := range map[string]ref.Val{
		"size(self.features)": types.Int(2),
		"self.limit / 2.0":    types.Double(3000),
		"meta.bytes":          types.Int(1234),
	} {
		prg, err := env.Compile(expr, Number)
		if err != nil {
			t.Fatalf("Compile(%q) error = %v", expr, err)
		}
		if got, _, err := prg.Eval(t.Context(), vars()); err != nil || got.Equal(want) != types.True {
			t.Errorf("Eval(%q) = %v, %v; want %v", expr, got, err, want)
		}
	}
}

func TestKeys(t *testing.T) {
	env := newEnv(t, Keys, Limits{})
	prg, err := env.Compile("item.id", Any)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := prg.Eval(t.Context(), map[string]any{VarItem: map[string]any{"id": "compute"}})
	if err != nil || got != types.String("compute") {
		t.Errorf("Eval(item.id) = %v, %v", got, err)
	}
	if _, err := env.Compile("self.id", Any); err == nil {
		t.Error("a key expression can read self; want only item")
	}
}

func TestCompileErrors(t *testing.T) {
	env := newEnv(t, Rules, Limits{})
	tests := []struct {
		expr string
		want Result
		msg  string
	}{
		{"selff.x > 0", Bool, "1:1: undeclared reference to 'selff'"},
		{"meta.byts > 0", Bool, "1:5: undefined field 'byts'"},
		{"self.features.all(f,\n  f.weight > 0 || meta.formt == 'json')", Bool, "2:23: undefined field 'formt'"},
		{"has(meta.size)", Bool, "1:4: undefined field 'size'"},
		{"size(1) > 0", Bool, "found no matching overload for 'size'"},
		{"self.x >", Bool, "1:9: Syntax error"},
		{"item.id == 'x'", Bool, "undeclared reference to 'item'"},
		{"'abc'", Bool, "the expression returns a string, but it must return a boolean"},
		{"size(self.x) == 1", Number, "the expression returns a boolean, but it must return a number"},
		{"1 + 1", List, "the expression returns a number (int), but it must return a list"},
	}
	for _, tt := range tests {
		_, err := env.Compile(tt.expr, tt.want)
		var ce *CompileError
		if !errors.As(err, &ce) || !strings.Contains(err.Error(), tt.msg) {
			t.Errorf("Compile(%q) error = %v, want a *CompileError containing %q", tt.expr, err, tt.msg)
		}
	}
}

func TestEvalErrors(t *testing.T) {
	tests := []struct {
		name   string
		limits Limits
		expr   string
		want   error
		msg    string
	}{
		{name: "missing field", expr: "self.missing > 0", want: ErrEval, msg: "no such key: missing"},
		{name: "division by zero", expr: "1 / (size(self.features) - 2) == 1", want: ErrEval, msg: "division by zero"},
		{name: "wrong result", expr: "self.limit", want: ErrResult, msg: "it returned a number, not a boolean"},
		{name: "unique on a non-list", expr: "zerocular.unique(self.limit)", want: ErrEval, msg: "no such overload"},
		// A lowered limit keeps these quick under the race detector, where
		// using up the default limit can take longer than the timeout.
		{
			name: "cost limit", limits: Limits{Cost: 20_000}, expr: "lists.range(2000).all(i, lists.range(2000).all(j, i + j >= 0))",
			want: ErrCostLimit, msg: "the expression exceeded its cost limit of 20000",
		},
		{
			name: "small cost limit", limits: Limits{Cost: 100}, expr: "lists.range(1000).all(i, i >= 0)",
			want: ErrCostLimit, msg: "cost limit of 100",
		},
		{
			name: "timeout", limits: Limits{Timeout: time.Microsecond}, expr: "lists.range(100000).all(i, i >= 0)",
			want: ErrTimeout, msg: "the expression timed out after 1µs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := newEnv(t, Rules, tt.limits).Compile(tt.expr, Bool)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = prg.Eval(t.Context(), vars())
			if !errors.Is(err, tt.want) || !strings.Contains(err.Error(), tt.msg) {
				t.Errorf("Eval() error = %v, want one wrapping %v and containing %q", err, tt.want, tt.msg)
			}
		})
	}
}

func TestEvalStopsWithTheContext(t *testing.T) {
	prg, err := newEnv(t, Rules, Limits{}).Compile("lists.range(100000).all(i, i >= 0)", Bool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := prg.Eval(ctx, vars()); !errors.Is(err, context.Canceled) {
		t.Errorf("Eval() with a canceled context = %v, want context.Canceled", err)
	}
}

func TestLimitsOnlyGoDown(t *testing.T) {
	for _, limits := range []Limits{{Cost: DefaultCostLimit + 1}, {Timeout: DefaultTimeout + time.Millisecond}, {Timeout: -time.Second}} {
		if _, err := New(Rules, limits); err == nil {
			t.Errorf("New(%+v) error = nil, want an error", limits)
		}
	}
	env := newEnv(t, Rules, Limits{})
	if env.limits != (Limits{Cost: DefaultCostLimit, Timeout: DefaultTimeout}) {
		t.Errorf("default limits = %+v", env.limits)
	}
}

func TestUnique(t *testing.T) {
	env := newEnv(t, Rules, Limits{})
	tests := []struct {
		list string
		want bool
	}{
		{"[]", true},
		{"[1, 2, 3]", true},
		{"[1, 1.0]", false},
		{"[1, 1u]", false},
		{"[1, '1']", true},
		{"['a', 'b', 'a']", false},
		{"[null, null]", false},
		{"[true, false]", true},
		{"[b'a', b'a']", false},
		{"[[1, 2], [1, 2.0]]", false},
		{"[[1, 2], [2, 1]]", true},
		{"[{'a': 1, 'b': 2}, {'b': 2, 'a': 1.0}]", false},
		{"[{'a': 1}, {'a': 2}]", true},
		{"[0.0 / 0.0, 0.0 / 0.0]", true},
		{"[[0.0 / 0.0], [0.0 / 0.0]]", true},
		{"[1.0 / 0.0, 2.0 / 0.0]", false},
		{"[-0.0, 0]", false},
		{"[0.5, 0.25, 0.5]", false},
		{"[duration('1s'), duration('1000ms')]", false},
		{"[timestamp('2026-10-11T00:00:00Z'), timestamp('2026-10-11T02:00:00+02:00')]", false},
	}
	for _, tt := range tests {
		prg, err := env.Compile("zerocular.unique("+tt.list+")", Bool)
		if err != nil {
			t.Fatalf("Compile(%s) error = %v", tt.list, err)
		}
		if got, _, err := prg.Eval(t.Context(), vars()); err != nil || got != types.Bool(tt.want) {
			t.Errorf("zerocular.unique(%s) = %v, %v; want %v", tt.list, got, err, tt.want)
		}
	}
}

// TestUniqueMatchesEquality checks zerocular.unique against a quadratic
// check that uses CEL's own ==, on random lists drawn from a small set of
// values so that duplicates are common.
func TestUniqueMatchesEquality(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var elems []any
		for range rapid.IntRange(0, 8).Draw(t, "length") {
			elems = append(elems, genElement(t, 0))
		}
		list := types.DefaultTypeAdapter.NativeToValue(elems).(traits.Lister)
		want := true
		for i := range elems {
			for j := range i {
				if list.Get(types.Int(i)).Equal(list.Get(types.Int(j))) == types.True {
					want = false
				}
			}
		}
		if got := unique(list); got != types.Bool(want) {
			t.Fatalf("unique(%v) = %v, want %v", elems, got, want)
		}
	})
}

func genElement(t *rapid.T, depth int) any {
	kinds := []string{"int", "uint", "double", "string", "bool", "null"}
	if depth < 2 {
		kinds = append(kinds, "list", "map")
	}
	switch rapid.SampledFrom(kinds).Draw(t, "kind") {
	case "int":
		return rapid.Int64Range(-2, 2).Draw(t, "int")
	case "uint":
		return rapid.Uint64Range(0, 2).Draw(t, "uint")
	case "double":
		return rapid.SampledFrom([]float64{-1, 0, 0.5, 1, 2, 1e300}).Draw(t, "double")
	case "string":
		return rapid.SampledFrom([]string{"", "a", "1", "ab"}).Draw(t, "string")
	case "bool":
		return rapid.Bool().Draw(t, "bool")
	case "list":
		var l []any
		for range rapid.IntRange(0, 2).Draw(t, "list length") {
			l = append(l, genElement(t, depth+1))
		}
		return l
	case "map":
		m := map[string]any{}
		for _, k := range rapid.SliceOfNDistinct(rapid.SampledFrom([]string{"a", "b"}), 0, 2, rapid.ID[string]).Draw(t, "keys") {
			m[k] = genElement(t, depth+1)
		}
		return m
	default:
		return nil
	}
}

// TestUniqueIsLinear checks that zerocular.unique handles a large list well
// within its limits, and that its cost grows with the list.
func TestUniqueIsLinear(t *testing.T) {
	env := newEnv(t, Rules, Limits{})
	prg, err := env.Compile("zerocular.unique(self.ids)", Bool)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]any, 200_000)
	for i := range ids {
		ids[i] = float64(i)
	}
	start := time.Now()
	got, cost, err := prg.Eval(t.Context(), map[string]any{VarSelf: map[string]any{"ids": ids}, VarBaseline: nil, VarMeta: Meta{}.Value()})
	if err != nil || got != types.True {
		t.Fatalf("Eval() = %v, %v", got, err)
	}
	if cost < 200_000 || time.Since(start) > 10*time.Second {
		t.Errorf("Eval() cost %d in %v; want at least 200000, in seconds rather than the minutes a quadratic check takes", cost, time.Since(start))
	}
	ids = append(ids, make([]any, 900_000)...)
	if _, _, err := prg.Eval(t.Context(), map[string]any{VarSelf: map[string]any{"ids": ids}, VarBaseline: nil, VarMeta: Meta{}.Value()}); !errors.Is(err, ErrCostLimit) {
		t.Errorf("Eval() on 1.1 million ids = %v, want ErrCostLimit", err)
	}
}

// sizeEstimator tells the static cost estimator that every list holds
// size elements.
type sizeEstimator struct{ size uint64 }

func (s sizeEstimator) EstimateSize(checker.AstNode) *checker.SizeEstimate {
	return &checker.SizeEstimate{Min: 0, Max: s.size}
}

func (sizeEstimator) EstimateCallCost(string, string, *checker.AstNode, []checker.AstNode) *checker.CallEstimate {
	return nil
}

func TestUniqueStaticCost(t *testing.T) {
	env := newEnv(t, Rules, Limits{})
	prg, err := env.Compile("zerocular.unique(self.ids)", Bool)
	if err != nil {
		t.Fatal(err)
	}
	estimate, err := env.env.EstimateCost(prg.ast, sizeEstimator{size: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Max < 5000 || estimate.Max > 10_000 {
		t.Errorf("estimated cost = %+v, want it linear in a 5000-element list", estimate)
	}
}

// FuzzCompileAndEval compiles fuzzed expressions and evaluates the ones
// that compile, checking that neither step panics and that every failure
// is one Eval documents.
func FuzzCompileAndEval(f *testing.F) {
	for _, seed := range []string{
		"size(self.features) <= 200",
		"self.policies.all(p, has(p.limit) && p.limit > 0)",
		"zerocular.unique(self.features.map(f, f.name))",
		"lists.range(10).map(i, i * 2).sort()[0] == 0",
		"'%s and %d'.format(['a', 1]) == 'a and 1'",
		"math.greatest([1, 2.5, -3])",
		"self.missing",
	} {
		f.Add(seed)
	}
	env, err := New(Rules, Limits{Timeout: 50 * time.Millisecond})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, expr string) {
		prg, err := env.Compile(expr, Any)
		if err != nil {
			var ce *CompileError
			if !errors.As(err, &ce) || len(ce.Problems) == 0 {
				t.Fatalf("Compile(%q) error = %v, want a *CompileError", expr, err)
			}
			return
		}
		_, _, err = prg.Eval(t.Context(), vars())
		if err != nil && !errors.Is(err, ErrEval) && !errors.Is(err, ErrCostLimit) && !errors.Is(err, ErrTimeout) {
			t.Fatalf("Eval(%q) error = %v, want one wrapping a documented error", expr, err)
		}
	})
}

func TestExplain(t *testing.T) {
	env := newEnv(t, Rules, Limits{})
	tests := []struct{ expr, want string }{
		{"size(self.features) <= 1", "size(self.features) = 2, not <= 1"},
		{"1 >= size(self.features)", "size(self.features) = 2, not <= 1"},
		{"self.limit > 0 && size(self.features) >= 3", "size(self.features) = 2, not >= 3"},
		{"self.policies.all(p, has(p.limit) && p.limit != null && p.limit > 0)", "fails for self.policies[1]: p.limit = null, not != null"},
		{"self.features.all(f, f.weight >= 1.0)", "fails for self.features[1]: f.weight = 0.25, not >= 1.0"},
		{"meta.format == 'yaml'", `meta.format = "json", not == "yaml"`},
		{"self.limit == size(self.features)", "self.limit = 6000, not == size(self.features) (2)"},
		{"!(self.limit > 0)", "!(self.limit > 0) is false"},
		{"self.features.exists(f, f.weight > 5.0)", "self.features.exists(f, f.weight > 5.0) is false"},
		{"size(self.features) == 2", ""},
	}
	for _, tt := range tests {
		prg, err := env.Compile(tt.expr, Bool)
		if err != nil {
			t.Fatalf("Compile(%q) error = %v", tt.expr, err)
		}
		if got := prg.Explain(t.Context(), vars()); got != tt.want {
			t.Errorf("Explain(%q) = %q, want %q", tt.expr, got, tt.want)
		}
	}
}

func TestShow(t *testing.T) {
	long := strings.Repeat("x", 200)
	for v, want := range map[ref.Val]string{
		types.Double(6000):   "6000",
		types.Double(0.25):   "0.25",
		types.Double(1e300):  "1e+300",
		types.Int(-3):        "-3",
		types.String("a\"b"): `"a\"b"`,
		types.NullValue:      "null",
		types.DefaultTypeAdapter.NativeToValue(map[string]any{"b": []any{1.0, "x"}, "a": nil}): `{"a": null, "b": [1, "x"]}`,
	} {
		if got := Show(v); got != want {
			t.Errorf("Show(%v) = %q, want %q", v, got, want)
		}
	}
	got := Show(types.String(long))
	if utf8.RuneCountInString(got) != maxShownRunes || !strings.HasSuffix(got, "\xe2\x80\xa6") {
		t.Errorf("Show(a long string) = %q, want %d characters ending in an ellipsis", got, maxShownRunes)
	}
}

// TestCostIsLinear guards against the quadratic cost tracking in cel-go
// v0.30.0 that ADR-0014 works around: a comprehension over a long list
// costs a few units per element, and takes milliseconds, not seconds.
func TestCostIsLinear(t *testing.T) {
	features := make([]any, 100_000)
	for i := range features {
		features[i] = map[string]any{"name": "f", "weight": 1.0}
	}
	vars := map[string]any{VarSelf: map[string]any{"features": features}, VarBaseline: nil, VarMeta: Meta{}.Value()}
	env := newEnv(t, Rules, Limits{})
	prg, err := env.Compile("self.features.all(f, f.weight >= 0.0)", Bool)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got, cost, err := prg.Eval(t.Context(), vars)
	if err != nil || got != types.True {
		t.Fatalf("Eval() = %v, %v", got, err)
	}
	// Each element costs its comparison, the && that accumulates the
	// result, and the loop condition; the comprehension itself costs 1.
	// The time bound is loose for the race detector; quadratic tracking
	// takes minutes at this size.
	if want := uint64(3*len(features) + 1); cost != want || time.Since(start) > 10*time.Second {
		t.Errorf("Eval() cost %d in %v; want %d, in seconds at most", cost, time.Since(start), want)
	}
	// A quadratic expression runs into the cost limit before the timeout,
	// without building its 10^10-element result.
	prg, err = env.Compile("self.features.map(a, self.features.map(b, a.name)).size() > 0", Bool)
	if err != nil {
		t.Fatal(err)
	}
	if _, cost, err := prg.Eval(t.Context(), vars); !errors.Is(err, ErrCostLimit) || cost <= DefaultCostLimit {
		t.Errorf("Eval() of a quadratic expression = cost %d, %v; want ErrCostLimit", cost, err)
	}
}
