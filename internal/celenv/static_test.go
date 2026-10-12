package celenv

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestHints(t *testing.T) {
	tests := []struct {
		kind Kind
		expr string
		want string
	}{
		{Rules, "selff.x > 0", "1:1: undeclared reference to 'selff'; did you mean self?"},
		{Rules, "baselin == null", "1:1: undeclared reference to 'baselin'; did you mean baseline?"},
		{Rules, "sizee(self.x) > 0", "1:6: undeclared reference to 'sizee'; did you mean size?"},
		{Rules, "zerocular.uniqe(self.x)", "1:1: undeclared reference to 'zerocular.uniqe'; did you mean zerocular.unique?"},
		{Rules, "math.greatst(1, 2) > 0", "1:1: undeclared reference to 'math.greatst'; math has no function by that name"},
		{Rules, "math > 1", "1:1: undeclared reference to 'math'; did you mean meta?"},
		{Rules, "nothing_like_it > 0", "1:1: undeclared reference to 'nothing_like_it'"},
		{Keys, "self.id", "1:1: undeclared reference to 'self'; a key reads only item, the element it identifies"},
		{Keys, "metaa.key", "1:1: undeclared reference to 'metaa'; a key reads only item, the element it identifies"},
		{Keys, "items.id", "1:1: undeclared reference to 'items'; did you mean item?"},
	}
	for _, tt := range tests {
		_, err := newEnv(t, tt.kind, Limits{}).Compile(tt.expr, Any)
		if err == nil || err.Error() != tt.want {
			t.Errorf("Compile(%q) error = %v, want %q", tt.expr, err, tt.want)
		}
	}
}

func TestMixedArithmetic(t *testing.T) {
	tests := []struct {
		expr string
		want []string
	}{
		{"self.x + 1 > 2.0", []string{"1:8: self.x + 1 fails when it runs: numbers from the artifact are doubles, and 1 is an int; write 1.0"}},
		{"2 * self.x > 2.0", []string{"1:3: 2 * self.x fails when it runs: numbers from the artifact are doubles, and 2 is an int; write 2.0"}},
		{"self.n % 2 == 0", []string{"1:8: self.n % 2 fails when it runs: numbers from the artifact are doubles, and % takes only ints; write int(self.n)"}},
		{"meta.bytes / 2.0 > 1.0", []string{"1:12: meta.bytes / 2.0 fails when it runs: meta.bytes is an int, and 2.0 is a double; write double(meta.bytes)"}},
		{"meta.bytes - self.x > 0.0", []string{"1:12: meta.bytes - self.x fails when it runs: numbers from the artifact are doubles, and meta.bytes is an int; write double(meta.bytes)"}},
		{
			"self.items.all(i,\n  i.price * 100 > i.cost - 1)",
			[]string{
				"2:11: i.price * 100 fails when it runs: numbers from the artifact are doubles, and 100 is an int; write 100.0",
				"2:26: i.cost - 1 fails when it runs: numbers from the artifact are doubles, and 1 is an int; write 1.0",
			},
		},
		// None of these mix an int with a double when they run.
		{"self.x + 1.0 > 2.0 && self.x * self.y > 0.0 && -self.x < 0.0", nil},
		{"meta.bytes + 1 > 2 && size(self.items) * 2 > 4 && int(self.n) % 2 == 0", nil},
		{"[1, 2].all(x, x + 1 > 0) && lists.range(3).map(i, i * 2).size() == 3", nil},
		{"[{'bytes': 1.5}].all(meta, meta.bytes * 2.0 > 0.0)", nil},
		{"self.name + 'x' == 'yx' && meta.key + 'x' != ''", nil},
	}
	env := newEnv(t, Rules, Limits{})
	for _, tt := range tests {
		p, err := env.Compile(tt.expr, Any)
		if err != nil {
			t.Fatalf("Compile(%q) error = %v", tt.expr, err)
		}
		var got []string
		for _, problem := range p.MixedArithmetic() {
			got = append(got, (&CompileError{Problems: []Problem{problem}}).Error())
		}
		if diff := cmp.Diff(tt.want, got); diff != "" {
			t.Errorf("MixedArithmetic(%q) mismatch (-want +got):\n%s", tt.expr, diff)
		}
	}
}

func TestReads(t *testing.T) {
	env := newEnv(t, Rules, Limits{})
	for expr, want := range map[string]bool{
		"size(baseline.items)":                        true,
		"baseline == null":                            true,
		"self.items.all(i, i.v > baseline.v)":         true,
		"size(self.items)":                            false,
		"[1].map(baseline, baseline + 1).size() == 1": false,
	} {
		p, err := env.Compile(expr, Any)
		if err != nil {
			t.Fatalf("Compile(%q) error = %v", expr, err)
		}
		if got := p.Reads(VarBaseline); got != want {
			t.Errorf("Reads(%q) = %v, want %v", expr, got, want)
		}
	}
}

func TestListSource(t *testing.T) {
	tests := []struct {
		expr     string
		path     []string
		same, ok bool
	}{
		{"self.policies", []string{"self", "policies"}, true, true},
		{"self.a.b", []string{"self", "a", "b"}, true, true},
		{"self.items.filter(i, i.on)", []string{"self", "items"}, true, true},
		{"self.items.map(i, i.id)", []string{"self", "items"}, false, true},
		{"self.items.filter(i, i.on).map(i, i.id)", []string{"self", "items"}, false, true},
		{"self.items.map(i, i.id, i.on)", []string{"self", "items"}, false, true},
		{"self.a + self.b", nil, false, false},
		{"[1, 2]", nil, false, false},
		{"self.items.filter(i, i.on)[0].tags", nil, false, false},
	}
	env := newEnv(t, Rules, Limits{})
	for _, tt := range tests {
		p, err := env.Compile(tt.expr, Any)
		if err != nil {
			t.Fatalf("Compile(%q) error = %v", tt.expr, err)
		}
		path, same, ok := p.ListSource()
		if !slices.Equal(path, tt.path) || same != tt.same || ok != tt.ok {
			t.Errorf("ListSource(%q) = %q, %v, %v; want %q, %v, %v", tt.expr, path, same, ok, tt.path, tt.same, tt.ok)
		}
	}
}

// TestEstimateCostValues checks what EstimateCost asks about: the values
// with sizes, with their paths, whether they're looped over, and their
// source; and that the estimate grows with the sizes.
func TestEstimateCostValues(t *testing.T) {
	p, err := newEnv(t, Rules, Limits{}).Compile("self.items.all(i, i.name.matches('^a')) && meta.sha256 != ''", Bool)
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	size := func(v Value) uint64 {
		asked = append(asked, strings.Join(v.Path, ".")+" "+v.Source+map[bool]string{true: " (iterated)"}[v.Iterated])
		return 10
	}
	small, err := p.EstimateCost(size)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"self.items self.items (iterated)", "self.items.@keys.name i.name", "meta.sha256 meta.sha256"} {
		if !slices.Contains(asked, want) {
			t.Errorf("EstimateCost asked about %q; want it to ask about %q", asked, want)
		}
	}
	large, err := p.EstimateCost(func(Value) uint64 { return 1000 })
	if err != nil {
		t.Fatal(err)
	}
	if small == 0 || large <= small*10 {
		t.Errorf("estimates = %d with sizes of 10, %d with sizes of 1000; want it to grow faster than linearly", small, large)
	}
}
