package eval

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v3"
	"pgregory.net/rapid"

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/internal/yamlx"
)

// fuzzCaps keeps fuzzed documents small enough to check quickly.
func fuzzCaps(format contract.Format) contract.Artifact {
	return contract.Artifact{Format: format, MaxBytes: 1 << 16, MaxDepth: 24, MaxNodes: 5000}
}

// FuzzDecodeJSON checks the JSON decoder against encoding/json: whatever it
// accepts, json.Unmarshal accepts as the same value, and a syntax error it
// reports, json.Unmarshal reports too.
func FuzzDecodeJSON(f *testing.F) {
	for _, seed := range []string{
		`{"a": [1, 2.5, -0, "x", true, null, {}]}`,
		`{"a": 1, "a": 2}`,
		`[9007199254740991, 9007199254740992]`,
		`"\ud83d\ude00\ud800"`,
		`{"a": 1}{"b": 2}`,
		"\xef\xbb\xbf{}",
		`[[[[[[[[[[[[[[[[[[[[[[[[[[1]]]]]]]]]]]]]]]]]]]]]]]]]]`,
		``,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		a := fuzzCaps(contract.FormatJSON)
		doc, err := Decode(t.Context(), data, a)
		var std any
		stdErr := json.Unmarshal(data, &std)
		if err != nil {
			var de *DecodeError
			if !errors.As(err, &de) {
				t.Fatalf("Decode() error = %v, want a *DecodeError", err)
			}
			if errors.Is(err, ErrSyntax) && stdErr == nil {
				t.Fatalf("Decode() reports %v, but json.Unmarshal accepts the text", err)
			}
			return
		}
		if stdErr != nil {
			t.Fatalf("Decode() accepts what json.Unmarshal rejects with %v", stdErr)
		}
		if diff := cmp.Diff(std, doc.Value); diff != "" {
			t.Fatalf("Decode() disagrees with json.Unmarshal (-std +got):\n%s", diff)
		}
		checkValue(t, doc.Value, a)
	})
}

// FuzzDecodeYAML checks that the YAML decoder never panics, that what it
// accepts stays within the caps, and that the source positions it relies on
// to catch the tag "!" line up with the parser's.
func FuzzDecodeYAML(f *testing.F) {
	for _, seed := range []string{
		"a: [1, 2.5, yes, 0777, ~, {b: c}]\n",
		"base: &b {x: 1}\nuse: *b\n",
		"a: &a [*a]\n",
		"a: ! 12\nb: &x ! 1\n",
		"b: &b {x: 1}\nc:\n  <<: *b\n",
		"a: 1\na: 2\n",
		"\xef\xbb\xbf- \"\xc3\xa9\"\r\n- x: |\n    text\n",
		"",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		a := fuzzCaps(contract.FormatYAML)
		doc, err := Decode(t.Context(), data, a)
		if err != nil {
			var de *DecodeError
			if !errors.As(err, &de) {
				t.Fatalf("Decode() error = %v, want a *DecodeError", err)
			}
			return
		}
		checkValue(t, doc.Value, a)
		checkPositions(t, data)
	})
}

// FuzzValidate validates fuzzed JSON against a schema that uses many kinds
// of keyword, and checks that Validate's output stays bounded and ordered.
func FuzzValidate(f *testing.F) {
	s, err := CompileSchema(schemaContract(featureSchema))
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{
		`{"features": [{"name": "ok"}], "version": 2, "address": "192.0.2.1"}`,
		`{"features": [{"name": "Not-OK"}, {}, 1, null], "version": 0.5, "address": "x", "extra": [true]}`,
		`[]`,
		`"x"`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := Decode(t.Context(), data, fuzzCaps(contract.FormatJSON))
		if err != nil {
			return
		}
		violations, total := s.Validate(doc.Value)
		if len(violations) > MaxViolations || total < len(violations) || (total == 0) != (len(violations) == 0) {
			t.Fatalf("Validate() = %d violations, %d in all", len(violations), total)
		}
		for i, v := range violations {
			if utf8.RuneCountInString(v.Message) > maxMessageRunes {
				t.Fatalf("violation %d has a %d-character message", i, utf8.RuneCountInString(v.Message))
			}
			if i > 0 && comparePointers(violations[i-1].Pointer, v.Pointer) > 0 {
				t.Fatalf("violations aren't in order: %q before %q", violations[i-1].Pointer, v.Pointer)
			}
		}
	})
}

// FuzzCheck checks fuzzed candidates against a contract with a schema and
// two kinds of invariant: Check never fails, and its result is consistent.
func FuzzCheck(f *testing.F) {
	c, err := contract.Parse([]byte(ruleContract))
	if err != nil {
		f.Fatal(err)
	}
	c.Schema.Data = []byte(ruleSchema)
	e, err := New(c)
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{
		`{"features": [{"name": "a", "weight": 0.5}]}`,
		`{"features": [{"name": "a", "weight": 2}, {"weight": 1}, {"name": "b"}, {}]}`,
		`{"features": "x"}`,
		`[]`,
		`{"features": [], "features": []}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := e.Check(t.Context(), Input{Data: data, Key: "candidate.json"}, nil)
		if err != nil {
			t.Fatalf("Check() error = %v", err)
		}
		if len(r.Checks) != 4 || r.Verdict != verdict(r.Checks) {
			t.Fatalf("Check() = %s with %d checks", r.Verdict, len(r.Checks))
		}
		decoded := r.Checks[0].Status == StatusPass
		for _, c := range r.Checks {
			switch c.Status {
			case StatusPass, StatusFail, StatusError, StatusSkipped:
			default:
				t.Fatalf("check %s has status %q", c.Name, c.Status)
			}
			if c.Status.Blocks() && c.Evidence == "" {
				t.Fatalf("check %s blocks without evidence", c.Name)
			}
			if c.Kind != KindEnvelope && (c.Status == StatusSkipped) == decoded {
				t.Fatalf("check %s is %s, but the envelope check is %s", c.Name, c.Status, r.Checks[0].Status)
			}
		}
	})
}

// FuzzBudgets checks fuzzed candidates against a fixed baseline, under a
// measure budget and a collection budget, and checks that what the budgets
// measure is consistent.
func FuzzBudgets(f *testing.F) {
	c, err := contract.Parse([]byte(`apiVersion: 0cular/v1alpha1
kind: Contract
metadata:
  name: budgets
artifact:
  format: json
  maxBytes: 64KiB
diffBudgets:
  - {name: count, measure: size(self.items), maxChangePct: 20, maxIncrease: 2}
  - {name: churn, collection: self.items, key: item.id, maxRemoved: 1, maxChangedPct: 25, overridable: false}
  - {name: churn-again, collection: self.items, key: item.id, maxAdded: 0}
  - {name: built, collection: "self.items.map(i, {'id': i.id, 'v': i})", key: item.id, maxRemoved: 1}
`))
	if err != nil {
		f.Fatal(err)
	}
	e, err := New(c)
	if err != nil {
		f.Fatal(err)
	}
	baseline := Input{Data: []byte(`{"items": [{"id": "a", "v": 1}, {"id": "b", "v": 2}, {"id": "c", "v": 3}, {"id": 4, "v": 4}]}`)}
	for _, seed := range []string{
		`{"items": [{"id": "a", "v": 1}, {"id": "b", "v": 2}, {"id": "c", "v": 3}, {"id": 4, "v": 4}]}`,
		`{"items": [{"id": "a", "v": 9}, {"id": "e"}, {"id": 4.0, "v": 4}]}`,
		`{"items": [{"id": "a"}, {"id": "a"}]}`,
		`{"items": [{"v": 1}]}`,
		`{"items": {"id": "a"}}`,
		`{"items": []}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := e.Check(t.Context(), Input{Data: data}, &baseline)
		if err != nil {
			t.Fatalf("Check() error = %v", err)
		}
		for _, check := range r.Checks {
			if check.Kind != KindBudget {
				continue
			}
			v := check.Budget
			switch check.Status {
			case StatusPass, StatusNeedsApproval, StatusFail:
				if v == nil || (len(v.Breached) == 0) != (check.Status == StatusPass) {
					t.Fatalf("budget %s is %s with values %+v", check.Name, check.Status, v)
				}
				if v.Removed > v.BaselineItems || v.Added > v.CandidateItems || v.Changed > min(v.BaselineItems, v.CandidateItems) {
					t.Fatalf("budget %s counts more changes than items: %+v", check.Name, v)
				}
				if check.Status == StatusNeedsApproval && check.Name == "churn" {
					t.Fatalf("budget churn isn't overridable, but needs approval")
				}
			case StatusError, StatusSkipped:
				if v != nil || check.Evidence == "" {
					t.Fatalf("budget %s is %s with values %+v and evidence %q", check.Name, check.Status, v, check.Evidence)
				}
			default:
				t.Fatalf("budget %s has status %q", check.Name, check.Status)
			}
		}
		byName := map[string]Check{}
		for _, check := range r.Checks {
			byName[check.Name] = check
		}
		churn, again, built := byName["churn"], byName["churn-again"], byName["built"]
		// churn-again shares churn's diff, so it finds the same, at no cost.
		if again.Cost != 0 || (churn.Budget == nil) != (again.Budget == nil) ||
			churn.Budget != nil && !sameCounts(churn.Budget, again.Budget) ||
			churn.Budget == nil && again.Evidence != churn.Evidence {
			t.Fatalf("churn-again = %+v; want churn's diff %+v at no cost", again, churn)
		}
		// built wraps every item in a map that CEL builds, which CEL's ==
		// compares; it must count what churn counts.
		if churn.Budget != nil && built.Budget != nil && !sameCounts(churn.Budget, built.Budget) {
			t.Fatalf("built = %+v, churn = %+v; want the same counts", built.Budget, churn.Budget)
		}
	})
}

func sameCounts(a, b *BudgetValues) bool {
	return a.BaselineItems == b.BaselineItems && a.CandidateItems == b.CandidateItems &&
		a.Removed == b.Removed && a.Added == b.Added && a.Changed == b.Changed
}

// FuzzLint lints a contract with a fuzzed JSON Schema and a fuzzed
// invariant. Lint must not fail or panic, and each finding must have a
// severity, a field and a message.
func FuzzLint(f *testing.F) {
	for _, seed := range [][2]string{
		{`{"type": "object", "properties": {"items": {"type": "array", "maxItems": 10, "items": {"$ref": "#/$defs/i"}}}, "$defs": {"i": {"properties": {"name": {"format": "date_time"}}}}}`, "self.items.all(i, i.name.matches('^a'))"},
		{`{"allOf": [{"$ref": "#"}, {"maxProperties": 3}], "propertyNames": {"maxLength": 3}}`, "self.all(k, size(k) < 4)"},
		{`{"$defs": {"a/b c": {"type": "array"}}, "properties": {"x": {"$ref": "#/$defs/a~1b%20c"}}}`, "self.x.all(v, v + 1 > 2.0)"},
		{`{"properties": {"m": {"additionalProperties": {"$id": "inner", "$ref": "#/x"}}}}`, "self.m.a.exists(v, v == meta.key)"},
		{`{"items": [{"type": "string"}], "prefixItems": [true]}`, "self.filter(v, v != null).map(v, v).size() > 0"},
		{`true`, "meta.bytes * 1.5 > baseline.x"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, schema, expr string) {
		src := "apiVersion: 0cular/v1alpha1\nkind: Contract\nmetadata: {name: fuzz}\nartifact: {format: json, maxBytes: 64KiB}\n" +
			"schema: {jsonSchema: fuzz.schema.json}\ninvariants:\n  - name: rule\n    expr: " + strconv.Quote(expr) + "\n"
		c, err := contract.Parse([]byte(src))
		if err != nil {
			return // The expression doesn't fit in a YAML string.
		}
		c.Schema.Data = []byte(schema)
		findings, err := Lint(c)
		if err != nil {
			t.Fatalf("Lint() error = %v", err)
		}
		for _, f := range findings {
			if (f.Severity != SeverityError && f.Severity != SeverityWarning) || f.Path == "" || f.Message == "" {
				t.Fatalf("finding %+v lacks a severity, a field or a message", f)
			}
		}
	})
}

// checkValue checks that v holds only the types Decode produces, with
// finite numbers, valid strings, and nesting and size within a's caps.
func checkValue(t *testing.T, v any, a contract.Artifact) {
	t.Helper()
	nodes := 0
	var walk func(v any, depth int)
	walk = func(v any, depth int) {
		nodes++
		if nodes > a.MaxNodes {
			t.Fatalf("the value holds more than maxNodes (%d) values", a.MaxNodes)
		}
		switch v := v.(type) {
		case nil, bool:
		case float64:
			if math.IsInf(v, 0) || math.IsNaN(v) || math.Signbit(v) && v == 0 {
				t.Fatalf("the value holds the number %v", v)
			}
		case string:
			if !utf8.ValidString(v) {
				t.Fatalf("the value holds the invalid string %q", v)
			}
		case []any:
			if depth+1 > a.MaxDepth {
				t.Fatalf("the value nests deeper than maxDepth (%d)", a.MaxDepth)
			}
			for _, e := range v {
				walk(e, depth+1)
			}
		case map[string]any:
			if depth+1 > a.MaxDepth {
				t.Fatalf("the value nests deeper than maxDepth (%d)", a.MaxDepth)
			}
			for k, e := range v {
				if !utf8.ValidString(k) {
					t.Fatalf("the value holds the invalid key %q", k)
				}
				walk(e, depth+1)
			}
		default:
			t.Fatalf("the value holds a %T", v)
		}
	}
	walk(v, 0)
}

// checkPositions checks that, for every plain scalar in a YAML document,
// the offset the decoder computes from the node's line and column points
// at the scalar's first character, or at its anchor.
func checkPositions(t *testing.T, data []byte) {
	t.Helper()
	src := bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	root, err := yamlx.ParseDocument(src)
	if err != nil {
		t.Fatalf("ParseDocument() failed on a document Decode accepted: %v", err)
	}
	d := &yamlDecoder{src: src, lines: lineStarts(src)}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode && n.Style == 0 && n.Value != "" {
			off, ok := d.offset(n.Line, n.Column)
			want := n.Value[0]
			if n.Anchor != "" {
				want = '&'
			}
			if !ok || src[off] != want {
				t.Fatalf("plain scalar %q at %d:%d: offset %d, ok %v; want it to point at %q", n.Value, n.Line, n.Column, off, ok, want)
			}
		}
		if n.Kind != yaml.AliasNode {
			for _, c := range n.Content {
				walk(c)
			}
		}
	}
	walk(root)
}

// TestDecodeFormatsAgree writes random values as JSON, and checks that the
// JSON and YAML decoders both read back exactly what was written. YAML 1.2
// is a superset of JSON, so the same text must mean the same thing.
func TestDecodeFormatsAgree(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		want := genValue(t, 0)
		data, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []contract.Format{contract.FormatJSON, contract.FormatYAML} {
			src := data
			if format == contract.FormatYAML {
				src = yamlPrintable(data)
			}
			doc, err := Decode(t.Context(), src, caps(format))
			if err != nil {
				t.Fatalf("Decode(%s) error: %v\nsource: %s", format, err, src)
			}
			if diff := cmp.Diff(want, doc.Value); diff != "" {
				t.Fatalf("Decode(%s) mismatch (-want +got):\n%s\nsource: %s", format, diff, src)
			}
		}
	})
}

// genValue draws a JSON-shaped value that Decode accepts.
func genValue(t *rapid.T, depth int) any {
	kinds := []string{"null", "bool", "number", "string"}
	if depth < 4 {
		kinds = append(kinds, "array", "object")
	}
	switch rapid.SampledFrom(kinds).Draw(t, "kind") {
	case "bool":
		return rapid.Bool().Draw(t, "bool")
	case "number":
		// json.Marshal writes an integral float below 1e21 as an integer
		// literal, which must stay within ±MaxSafeInteger.
		return rapid.OneOf(
			rapid.Float64Range(-MaxSafeInteger, MaxSafeInteger),
			rapid.Float64Range(1e21, math.MaxFloat64),
			rapid.Float64Range(-math.MaxFloat64, -1e21),
		).Draw(t, "number") + 0
	case "string":
		return rapid.String().Draw(t, "string")
	case "array":
		s := []any{}
		for range rapid.IntRange(0, 4).Draw(t, "length") {
			s = append(s, genValue(t, depth+1))
		}
		return s
	case "object":
		m := map[string]any{}
		for _, k := range rapid.SliceOfNDistinct(rapid.String(), 0, 4, rapid.ID[string]).Draw(t, "keys") {
			m[k] = genValue(t, depth+1)
		}
		return m
	default:
		return nil
	}
}

// yamlPrintable escapes the characters a YAML stream can't hold raw, which
// json.Marshal leaves raw in strings: DEL, the C1 controls, U+FEFF, U+FFFE
// and U+FFFF. JSON text has no other place for them, so the text means the
// same afterward.
func yamlPrintable(data []byte) []byte {
	var b strings.Builder
	for _, r := range string(data) {
		if r >= 0x7f && r <= 0x9f || r == 0xfeff || r == 0xfffe || r == 0xffff {
			fmt.Fprintf(&b, `\u%04x`, r)
			continue
		}
		b.WriteRune(r)
	}
	return []byte(b.String())
}
