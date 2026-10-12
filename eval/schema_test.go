package eval

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"

	"github.com/itillema/0cular/contract"
)

// schemaContract returns a contract that names a schema with the given
// content.
func schemaContract(schema string) *contract.Contract {
	return &contract.Contract{Schema: &contract.Schema{JSONSchema: "schemas/test.schema.json", Data: []byte(schema)}}
}

func compileTestSchema(t *testing.T, schema string) *Schema {
	t.Helper()
	s, err := CompileSchema(schemaContract(schema))
	if err != nil {
		t.Fatalf("CompileSchema() error = %v", err)
	}
	return s
}

func decodeTestJSON(t *testing.T, src string) any {
	t.Helper()
	doc, err := Decode(t.Context(), []byte(src), caps(contract.FormatJSON))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	return doc.Value
}

func TestCompileSchemaWithoutSchema(t *testing.T) {
	s, err := CompileSchema(&contract.Contract{})
	if s != nil || err != nil {
		t.Errorf("CompileSchema() = %v, %v; want nil, nil", s, err)
	}
}

func TestCompileSchemaErrors(t *testing.T) {
	tests := []struct {
		name   string
		schema *string
		want   string
	}{
		{name: "not read", schema: nil, want: "schemas/test.schema.json wasn't read; load the contract with contract.LoadFile"},
		{name: "invalid JSON", schema: ptr(`{"type": }`), want: "line 1, column "},
		{name: "duplicate key", schema: ptr(`{"type": "object", "type": "array"}`), want: `duplicate key "type" in the top level`},
		{name: "invalid UTF-8", schema: ptr("{\"title\": \"\xff\"}"), want: "line 1, column 12: the schema isn't valid UTF-8"},
		{name: "integer beyond 2^53", schema: ptr(`{"maxItems": 12345678901234567890}`), want: "the integer 12345678901234567890 at /maxItems is beyond"},
		{name: "not a schema", schema: ptr(`{"type": 5}`), want: "schemas/test.schema.json: at /type: value must be one of 'array'"},
		{name: "reference to a file", schema: ptr(`{"$ref": "common.json#/$defs/name"}`), want: "the schema refers to file:///common.json, but 0cular reads only the schema file the contract names; bundle the other schemas into it"},
		{name: "reference to a URL", schema: ptr(`{"$ref": "https://example.com/s.json"}`), want: "the schema refers to https://example.com/s.json"},
		{name: "regex that RE2 can't run", schema: ptr(`{"pattern": "^(?=a)"}`), want: "at /pattern: '^(?=a)' is not valid regex"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := schemaContract("")
			c.Schema.Data = nil
			if tt.schema != nil {
				c.Schema.Data = []byte(*tt.schema)
			}
			s, err := CompileSchema(c)
			if s != nil || !errors.Is(err, ErrSchema) {
				t.Fatalf("CompileSchema() = %v, %v; want an error wrapping ErrSchema", s, err)
			}
			if !strings.HasPrefix(err.Error(), "invalid JSON Schema: schemas/test.schema.json") || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("CompileSchema() error = %q, want it to name the schema and contain %q", err, tt.want)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestCompileSchemaReferences(t *testing.T) {
	s := compileTestSchema(t, `{
		"$id": "https://example.com/bot-features.schema.json",
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "array",
		"items": {"$ref": "#/$defs/feature"},
		"$defs": {"feature": {"type": "string", "maxLength": 3}}
	}`)
	violations, total := s.Validate(decodeTestJSON(t, `["abc", "abcd"]`))
	want := []Violation{{Pointer: "/1", Keyword: "/items/$ref/maxLength", Message: "maxLength: got 4, want 3"}}
	if diff := cmp.Diff(want, violations); diff != "" || total != 1 {
		t.Errorf("Validate() = %d in all, mismatch (-want +got):\n%s", total, diff)
	}
}

// TestValidateReplayFixtures checks the replay suite's baselines and
// candidates against their contracts' schemas. The schemas are permissive
// on purpose: the candidates fail their invariants, not their schemas.
func TestValidateReplayFixtures(t *testing.T) {
	contracts, err := filepath.Glob("../replay/*/contract.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range contracts {
		c, err := contract.LoadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		s, err := CompileSchema(c)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, version := range []string{"baseline", "candidate"} {
			path := filepath.Join(filepath.Dir(file), version+"."+string(c.Artifact.Format))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := Decode(t.Context(), data, c.Artifact)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			if violations, _ := s.Validate(doc.Value); len(violations) > 0 {
				t.Errorf("%s fails its schema: %+v", path, violations)
			}
		}
	}
}

const featureSchema = `{
	"type": "object",
	"required": ["features"],
	"properties": {
		"features": {"type": "array", "maxItems": 2, "items": {"$ref": "#/$defs/feature"}},
		"version": {"type": "integer", "minimum": 1},
		"address": {"type": "string", "format": "ipv4"}
	},
	"additionalProperties": false,
	"$defs": {
		"feature": {
			"type": "object",
			"required": ["name"],
			"properties": {"name": {"type": "string", "pattern": "^[a-z]+$"}}
		}
	}
}`

func TestValidate(t *testing.T) {
	s := compileTestSchema(t, featureSchema)
	if violations, total := s.Validate(decodeTestJSON(t, `{"features": [{"name": "ok"}], "version": 2, "address": "192.0.2.1"}`)); total != 0 || violations != nil {
		t.Fatalf("Validate() of a valid value = %+v, %d", violations, total)
	}
	got, total := s.Validate(decodeTestJSON(t, `{
		"features": [{"name": "Not-OK"}, {"weight": 1}, {"name": "x"}],
		"version": 0,
		"address": "300.1.1.1",
		"extra": true
	}`))
	want := []Violation{
		{Pointer: "", Keyword: "/additionalProperties", Message: "additional properties 'extra' not allowed"},
		{Pointer: "/address", Keyword: "/properties/address/format", Message: "'300.1.1.1' is not valid ipv4: decimal must be between 0 and 255"},
		{Pointer: "/features", Keyword: "/properties/features/maxItems", Message: "maxItems: got 3, want 2"},
		{Pointer: "/features/0/name", Keyword: "/properties/features/items/$ref/properties/name/pattern", Message: "'Not-OK' does not match pattern '^[a-z]+$'"},
		{Pointer: "/features/1", Keyword: "/properties/features/items/$ref/required", Message: "missing property 'name'"},
		{Pointer: "/version", Keyword: "/properties/version/minimum", Message: "minimum: got 0, want 1"},
	}
	if diff := cmp.Diff(want, got); diff != "" || total != len(want) {
		t.Errorf("Validate() = %d in all, mismatch (-want +got):\n%s", total, diff)
	}
}

func TestValidateListsTheFirstViolationsInOrder(t *testing.T) {
	s := compileTestSchema(t, `{"type": "array", "items": {"type": "string"}}`)
	v := decodeTestJSON(t, "["+strings.Repeat("1, ", 29)+"1]")
	first, total := s.Validate(v)
	if total != 30 || len(first) != MaxViolations {
		t.Fatalf("Validate() = %d violations, %d in all; want %d, 30", len(first), total, MaxViolations)
	}
	for i, violation := range first {
		if want := "/" + strconv.Itoa(i); violation.Pointer != want {
			t.Fatalf("violation %d is at %q, want %q", i, violation.Pointer, want)
		}
	}
}

// TestValidateIsDeterministic checks that violations come out in the same
// order every time, although the validator visits properties in map order.
func TestValidateIsDeterministic(t *testing.T) {
	s := compileTestSchema(t, `{"additionalProperties": {"type": "string"}}`)
	var src strings.Builder
	src.WriteString("{")
	for i := range 15 {
		if i > 0 {
			src.WriteString(", ")
		}
		src.WriteString(`"k` + strconv.Itoa(i) + `": 1`)
	}
	src.WriteString("}")
	v := decodeTestJSON(t, src.String())
	first, _ := s.Validate(v)
	for range 20 {
		if again, _ := s.Validate(v); !cmp.Equal(first, again) {
			t.Fatalf("Validate() changed its output:\n%+v\nthen:\n%+v", first, again)
		}
	}
}

func TestValidateTruncatesMessages(t *testing.T) {
	s := compileTestSchema(t, `{"pattern": "^a$"}`)
	long := strings.Repeat("\xc3\xa9", 10_000)
	violations, _ := s.Validate(decodeTestJSON(t, `"`+long+`"`))
	if len(violations) != 1 {
		t.Fatalf("Validate() = %+v, want one violation", violations)
	}
	message := violations[0].Message
	if n := utf8.RuneCountInString(message); n != maxMessageRunes || !strings.HasSuffix(message, "\xe2\x80\xa6") {
		t.Errorf("message has %d characters and ends %q; want %d, ending with an ellipsis", n, message[len(message)-3:], maxMessageRunes)
	}
	if got := truncate(strings.Repeat("x", maxMessageRunes)); got != strings.Repeat("x", maxMessageRunes) {
		t.Errorf("truncate() shortened a message that fits")
	}
}

func TestValidateFormats(t *testing.T) {
	s := compileTestSchema(t, `{"properties": {
		"when": {"format": "date-time"},
		"id": {"format": "uuid"},
		"note": {"format": "not-a-format-0cular-checks"}
	}}`)
	if violations, _ := s.Validate(decodeTestJSON(t, `{"when": "2026-10-11T12:00:00Z", "id": "0b5ad2e1-49a7-4c48-8a35-bb2a3c7c3f9b", "note": "x"}`)); len(violations) > 0 {
		t.Errorf("Validate() of valid formats = %+v", violations)
	}
	violations, total := s.Validate(decodeTestJSON(t, `{"when": "yesterday", "id": "nope"}`))
	if total != 2 || violations[0].Pointer != "/id" || violations[1].Pointer != "/when" {
		t.Errorf("Validate() of invalid formats = %+v, %d", violations, total)
	}
}

func TestComparePointers(t *testing.T) {
	ordered := []string{"", "/a", "/a/0", "/a/2", "/a/10", "/a/b", "/a~1b", "/b", "/b/01", "/b/1"}
	for i, a := range ordered {
		for j, b := range ordered {
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}
			if got := comparePointers(a, b); got != want {
				t.Errorf("comparePointers(%q, %q) = %d, want %d", a, b, got, want)
			}
		}
	}
}
