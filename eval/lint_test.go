package eval

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/itillema/0cular/contract"
)

// lintContract returns a contract file with extra artifact fields, such as
// ", maxNodes: 1000", a schema named test.schema.json if hasSchema, and the
// rest of the file.
func lintContract(artifact string, hasSchema bool, rest string) string {
	src := "apiVersion: 0cular/v1alpha1\nkind: Contract\nmetadata: {name: lint}\nartifact: {format: json, maxBytes: 1MiB" + artifact + "}\n"
	if hasSchema {
		src += "schema: {jsonSchema: test.schema.json}\n"
	}
	return src + rest
}

// lintFindings lints a contract and returns its findings, without their
// positions.
func lintFindings(t *testing.T, src, schema string) []string {
	t.Helper()
	findings, err := Lint(mustContract(t, src, schema))
	if err != nil {
		t.Fatalf("Lint() error = %v", err)
	}
	var out []string
	for _, f := range findings {
		out = append(out, fmt.Sprintf("%s: %s: %s", f.Severity, f.Path, f.Message))
	}
	return out
}

func TestLint(t *testing.T) {
	const (
		loop          = "invariants:\n  - {name: positive, expr: \"self.policies.all(p, p.limit > 0)\"}\n"
		policies      = `{"type": "object", "properties": {"policies": {"type": "array", "maxItems": 1000, "items": {"type": "object"}}}}`
		loopCost      = "it could cost up to 2,621,442 on an artifact this contract allows, over the cost limit of 1,000,000"
		policiesLoop  = "it loops over self.policies, which could hold up to 524,288 items"
		noSchemaBound = "bound it with maxItems or maxProperties in a JSON Schema (schema.jsonSchema), or lower artifact.maxBytes"
	)
	tests := []struct {
		name     string
		artifact string
		schema   string // empty for a contract without one
		rest     string
		want     []string
	}{
		{name: "a loop the schema bounds", schema: policies, rest: loop},
		{
			name: "a loop without a schema",
			rest: loop,
			want: []string{"error: invariants[0].expr: " + loopCost + ": " + policiesLoop + "; " + noSchemaBound},
		},
		{
			name:   "a loop the schema doesn't bound",
			schema: `{"type": "object", "properties": {"policies": {"type": "array"}}}`,
			rest:   loop,
			want:   []string{"error: invariants[0].expr: " + loopCost + ": " + policiesLoop + "; bound it with maxItems in the schema, or lower artifact.maxBytes"},
		},
		{
			name:     "maxNodes bounds the loop",
			artifact: ", maxNodes: 400000",
			rest:     loop,
			want: []string{"error: invariants[0].expr: it could cost up to 2,000,002 on an artifact this contract allows, over the cost limit of 1,000,000: " +
				"it loops over self.policies, which could hold up to 400,000 items; bound it with maxItems or maxProperties in a JSON Schema (schema.jsonSchema), or lower artifact.maxNodes"},
		},
		{name: "a small artifact needs no bounds", artifact: ", maxNodes: 1000", rest: loop},
		{
			name: "bounds through $ref and allOf",
			schema: `{
				"$defs": {"list": {"type": "array", "maxItems": 10}},
				"properties": {
					"a": {"$ref": "#/$defs/list"},
					"b": {"allOf": [{"type": "array"}, {"maxItems": 5}]},
					"c": {"$ref": "#"}
				},
				"type": "object",
				"maxProperties": 3
			}`,
			rest: "invariants:\n  - {name: a, expr: \"self.a.all(x, x > 0)\"}\n  - {name: b, expr: \"self.b.all(x, x > 0)\"}\n  - {name: c, expr: \"self.c.all(k, k != '')\"}\n",
		},
		{
			name:   "patternProperties keeps additionalProperties from applying",
			schema: `{"properties": {"m": {"type": "object", "patternProperties": {"^a": {}}, "additionalProperties": {"type": "array", "maxItems": 3}}}}`,
			rest:   "invariants:\n  - {name: m, expr: \"self.m.x.all(v, v > 0)\"}\n",
			want: []string{"error: invariants[0].expr: it could cost up to 2,621,442 on an artifact this contract allows, over the cost limit of 1,000,000: " +
				"it loops over self.m.x, which could hold up to 524,288 items; bound it with maxItems or maxProperties in the schema, or lower artifact.maxBytes"},
		},
		{
			name:   "additionalProperties bounds a field",
			schema: `{"properties": {"m": {"type": "object", "additionalProperties": {"type": "array", "maxItems": 3}}}}`,
			rest:   "invariants:\n  - {name: m, expr: \"self.m.x.all(v, v > 0) && self.m.all(k, self.m[k].size() < 4)\"}\n",
			want: []string{"error: invariants[0].expr: it could cost up to 4,194,323 on an artifact this contract allows, over the cost limit of 1,000,000: " +
				"it loops over self.m, which could hold up to 524,288 items; bound it with maxProperties in the schema, or lower artifact.maxBytes"},
		},
		{
			name:   "a nested $id stops lint following references",
			schema: `{"$defs": {"l": {"type": "array", "maxItems": 5}}, "properties": {"a": {"$ref": "#/$defs/l"}, "b": {"$id": "nested", "type": "object"}}}`,
			rest:   "invariants:\n  - {name: a, expr: \"self.a.all(x, x > 0)\"}\n",
			want: []string{"error: invariants[0].expr: it could cost up to 2,621,442 on an artifact this contract allows, over the cost limit of 1,000,000: " +
				"it loops over self.a, which could hold up to 524,288 items; bound it with maxItems or maxProperties in the schema, or lower artifact.maxBytes"},
		},
		{
			name:   "a nested loop",
			schema: `{"properties": {"features": {"type": "array", "maxItems": 200, "items": {"properties": {"tags": {"type": "array"}}}}}}`,
			rest:   "invariants:\n  - {name: tags, expr: \"self.features.all(f, f.tags.all(t, t != ''))\"}\n",
			want: []string{"error: invariants[0].expr: it could cost up to 419,431,402 on an artifact this contract allows, over the cost limit of 1,000,000: " +
				"it loops over self.features[*].tags, which could hold up to 524,288 items; bound it with maxItems in the schema, or lower artifact.maxBytes"},
		},
		{
			name:   "a string the schema doesn't bound",
			schema: `{"properties": {"features": {"type": "array", "maxItems": 100, "items": {"properties": {"name": {"type": "string"}}}}}}`,
			rest:   "invariants:\n  - {name: names, expr: \"self.features.all(f, f.name.matches('^feature-'))\"}\n",
			want: []string{"error: invariants[0].expr: it could cost up to 31,457,802 on an artifact this contract allows, over the cost limit of 1,000,000: " +
				"it reads self.features[*].name, which could hold up to 1,048,576 characters; bound it with maxLength in the schema, or lower artifact.maxBytes"},
		},
		{
			name:   "a string the schema bounds",
			schema: `{"properties": {"features": {"type": "array", "maxItems": 100, "items": {"properties": {"name": {"type": "string", "maxLength": 64}}}}}}`,
			rest:   "invariants:\n  - {name: names, expr: \"self.features.all(f, f.name.matches('^feature-'))\"}\n",
		},
		{
			name: "meta",
			rest: "invariants:\n  - {name: digest, expr: \"meta.sha256.matches('^[0-9a-f]+$') && meta.format == 'json'\"}\n" +
				"  - {name: key, expr: \"[1, 2, 3, 4].all(i, meta.key.matches('^policies/.*[.]json$'))\"}\n",
			want: []string{"error: invariants[1].expr: it could cost up to 2,097,191 on an artifact this contract allows, over the cost limit of 1,000,000: " +
				"it reads meta.key, the artifact's key or file path, whose length lint can't bound"},
		},
		{
			name: "formats",
			schema: `{
				"$defs": {"x": {"items": {"format": "ipv4"}}},
				"properties": {
					"at": {"type": "string", "format": "date_time"},
					"mail": {"format": "idn-email"},
					"tags": {"items": {"allOf": [{"format": "uri"}, {"format": "ip4"}]}}
				}
			}`,
			want: []string{
				`error: schema.jsonSchema: the schema's format "date_time" at /properties/at isn't one 0cular checks, so it checks nothing; did you mean date-time?`,
				`error: schema.jsonSchema: the schema's format "idn-email" at /properties/mail isn't one 0cular checks, so it checks nothing; the formats it checks are ` + strings.Join(knownFormats, ", "),
				`error: schema.jsonSchema: the schema's format "ip4" at /properties/tags/items/allOf/1 isn't one 0cular checks, so it checks nothing; did you mean ipv4?`,
			},
		},
		{
			name:   "a schema that doesn't compile",
			schema: `{"type": 12}`,
			rest:   loop,
			want: []string{"error: schema.jsonSchema: invalid JSON Schema: test.schema.json: at /type: " +
				"value must be one of 'array', 'boolean', 'integer', 'null', 'number', 'object', 'string'; and 1 more"},
		},
		{
			name: "expressions that don't compile",
			rest: "invariants:\n  - {name: a, expr: \"selff.x > 0\"}\n  - {name: b, expr: \"sizee(self.x) > 0\"}\n" +
				"diffBudgets:\n  - {name: c, collection: self.items, key: \"selff.id\", maxAdded: 1}\n",
			want: []string{
				"error: invariants[0].expr: at 1:1 in the expression: undeclared reference to 'selff'; did you mean self?",
				"error: invariants[1].expr: at 1:6 in the expression: undeclared reference to 'sizee'; did you mean size?",
				"error: diffBudgets[0].key: at 1:1 in the expression: undeclared reference to 'selff'; a key reads only item, the element it identifies",
			},
		},
		{
			name: "arithmetic that fails when it runs",
			rest: "invariants:\n" +
				"  - {name: a, expr: \"self.x + 1 > 2.0\"}\n" +
				"  - {name: b, expr: \"meta.bytes * 1.5 < 10.0\"}\n" +
				"  - {name: c, expr: \"self.n % 2 == 0\"}\n" +
				"  - {name: d, expr: \"size(self.items) * self.f > 1.0\"}\n" +
				"  - {name: fine, expr: \"meta.bytes + 1 > 2 && self.x + 1.0 > 2.0 && size(self.items) + 1 > 2 && self.x * self.y > 0.0\"}\n",
			want: []string{
				"warning: invariants[0].expr: at 1:8 in the expression: self.x + 1 fails when it runs: numbers from the artifact are doubles, and 1 is an int; write 1.0",
				"warning: invariants[1].expr: at 1:12 in the expression: meta.bytes * 1.5 fails when it runs: meta.bytes is an int, and 1.5 is a double; write double(meta.bytes)",
				"warning: invariants[2].expr: at 1:8 in the expression: self.n % 2 fails when it runs: numbers from the artifact are doubles, and % takes only ints; write int(self.n)",
				"warning: invariants[3].expr: at 1:18 in the expression: size(self.items) * self.f fails when it runs: numbers from the artifact are doubles, and size(self.items) is an int; write double(size(self.items))",
			},
		},
		{
			name:   "budgets that read baseline",
			schema: `{"properties": {"items": {"type": "array", "maxItems": 100}}}`,
			rest: "invariants:\n  - {name: newer, expr: \"baseline == null || self.version > baseline.version\"}\n" +
				"diffBudgets:\n" +
				"  - {name: m, measure: \"size(baseline.items)\", maxChangePct: 10}\n" +
				"  - {name: c, collection: baseline.items, key: item.id, maxAdded: 1}\n" +
				"  - {name: shadowed, measure: \"[1].map(baseline, baseline + 1).size()\", maxChangePct: 10}\n",
			want: []string{
				"error: diffBudgets[0].measure: a measure is evaluated on the candidate and then on the baseline, each as self, so baseline is always null here; read self instead",
				"error: diffBudgets[1].collection: a collection is evaluated on the candidate and then on the baseline, each as self, so baseline is always null here; read self instead",
			},
		},
		{
			name:   "collection keys",
			schema: `{"properties": {"items": {"type": "array", "maxItems": 100}, "more": {"type": "array", "maxItems": 100}}}`,
			rest: "diffBudgets:\n" +
				"  - {name: filtered, collection: \"self.items.filter(i, i.on)\", key: item.id, maxAdded: 1}\n" +
				"  - {name: mapped, collection: \"self.items.map(i, i.id)\", key: item, maxAdded: 1}\n" +
				"  - {name: joined, collection: \"self.items + self.more\", key: item.id, maxAdded: 1}\n",
			want: []string{
				"error: diffBudgets[2].key: keying every element of the collection could cost up to 13,631,500 on an artifact this contract allows, over the cost limit of 1,000,000: " + collectionNote,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lintFindings(t, lintContract(tt.artifact, tt.schema != "", tt.rest), tt.schema)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Lint() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestLintOrder checks that findings come in file order, whatever order
// lint finds them in.
func TestLintOrder(t *testing.T) {
	src := "apiVersion: 0cular/v1alpha1\nkind: Contract\nmetadata: {name: lint}\nartifact: {format: json, maxBytes: 1KiB}\n" +
		"invariants:\n  - {name: a, expr: \"self.x + 1 > 2.0 && self.y - 2 > 0.0\"}\n" +
		"schema: {jsonSchema: test.schema.json}\n"
	findings, err := Lint(mustContract(t, src, `{"format": "dates"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range findings {
		got = append(got, f.String())
	}
	want := []string{
		"6:21: warning: invariants[0].expr: at 1:8 in the expression: self.x + 1 fails when it runs: numbers from the artifact are doubles, and 1 is an int; write 1.0",
		"6:21: warning: invariants[0].expr: at 1:28 in the expression: self.y - 2 fails when it runs: numbers from the artifact are doubles, and 2 is an int; write 2.0",
		`7:22: error: schema.jsonSchema: the schema's format "dates" at the top level isn't one 0cular checks, so it checks nothing; did you mean date?`,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Lint() mismatch (-want +got):\n%s", diff)
	}
}

func TestFindingString(t *testing.T) {
	tests := []struct {
		f    Finding
		want string
	}{
		{Finding{Problem: contract.Problem{Pos: contract.Pos{Line: 3, Column: 7}, Path: "invariants[0].expr", Message: "m"}, Severity: SeverityError}, "3:7: error: invariants[0].expr: m"},
		{Finding{Problem: contract.Problem{Message: "m"}, Severity: SeverityWarning}, "warning: m"},
	}
	for _, tt := range tests {
		if got := tt.f.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestThousands(t *testing.T) {
	for n, want := range map[uint64]string{
		0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", math.MaxUint64: "18,446,744,073,709,551,615",
	} {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestDisplayPath(t *testing.T) {
	tests := []struct {
		path []string
		want string
	}{
		{[]string{"self"}, "self"},
		{[]string{"self", "a", "@keys", "b"}, "self.a[*].b"},
		{[]string{"self", "m", "@values"}, "self.m[*]"},
	}
	for _, tt := range tests {
		if got := displayPath(tt.path); got != tt.want {
			t.Errorf("displayPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestSchemaBoundsResolve(t *testing.T) {
	doc := decodeTestJSON(t, `{"$defs": {"a/b": 1, "a~b": 2, "a b": 3}, "allOf": [4, 5]}`)
	b := newSchemaBounds(doc)
	tests := []struct {
		ref  string
		want any
		ok   bool
	}{
		{"#", doc, true},
		{"#/$defs/a~1b", 1.0, true},
		{"#/$defs/a~0b", 2.0, true},
		{"#/$defs/a%20b", 3.0, true},
		{"#/allOf/1", 5.0, true},
		{"#/allOf/2", nil, false},
		{"#/missing", nil, false},
		{"#anchor", nil, false},
		{"other.json#/$defs", nil, false},
		{"#/%zz", nil, false},
	}
	for _, tt := range tests {
		got, ok := b.resolve(tt.ref)
		if ok != tt.ok || !cmp.Equal(got, tt.want) {
			t.Errorf("resolve(%q) = %v, %v; want %v, %v", tt.ref, got, ok, tt.want, tt.ok)
		}
	}
}

func TestSizeOf(t *testing.T) {
	f := fallbacks{list: 100, str: 1000}
	set := func(schemas ...string) schemaSet {
		var out schemaSet
		for _, s := range schemas {
			out = append(out, decodeTestJSON(t, s).(map[string]any))
		}
		return out
	}
	tests := []struct {
		name     string
		set      schemaSet
		iterated bool
		size     uint64
		bounded  bool
	}{
		{name: "nothing known", size: 1000},
		{name: "nothing known, looped over", iterated: true, size: 100},
		{name: "an array", set: set(`{"type": "array", "maxItems": 7}`), size: 7, bounded: true},
		{name: "the lowest bound", set: set(`{"maxItems": 7}`, `{"type": "array", "maxItems": 5}`), size: 5, bounded: true},
		{name: "a bound above the fallback", set: set(`{"type": "array", "maxItems": 5000}`), size: 100, bounded: true},
		{name: "a string or an array", set: set(`{"type": ["string", "array"], "maxLength": 3, "maxItems": 4}`), size: 4, bounded: true},
		{name: "an unbounded string", set: set(`{"type": ["string", "array"], "maxItems": 4}`), size: 1000},
		{name: "an unbounded string, looped over", set: set(`{"type": ["string", "array"], "maxItems": 4}`), iterated: true, size: 4, bounded: true},
		{name: "a number", set: set(`{"type": "integer"}`), size: 0, bounded: true},
		{name: "types that disagree", set: set(`{"type": "array"}`, `{"type": "object"}`), size: 0, bounded: true},
	}
	for _, tt := range tests {
		size, bounded, _ := sizeOf(tt.set, tt.iterated, f)
		if size != tt.size || bounded != tt.bounded {
			t.Errorf("%s: sizeOf() = %d, %v; want %d, %v", tt.name, size, bounded, tt.size, tt.bounded)
		}
	}
}

// TestKnownFormats checks knownFormats against the validator: it checks
// each format on the list, and none of a few that aren't.
func TestKnownFormats(t *testing.T) {
	if !slices.IsSorted(knownFormats) {
		t.Errorf("knownFormats isn't sorted: %q", knownFormats)
	}
	for _, f := range append(slices.Clone(knownFormats), "idn-email", "idn-hostname", "date_time", "ip4") {
		s, err := CompileSchema(schemaContract(`{"format": "` + f + `"}`))
		if err != nil {
			t.Fatalf("CompileSchema(format %s) error = %v", f, err)
		}
		if checked, known := s.schema.Format != nil, slices.Contains(knownFormats, f); checked != known {
			t.Errorf("format %s: the validator checks it: %v; knownFormats lists it: %v", f, checked, known)
		}
	}
}
