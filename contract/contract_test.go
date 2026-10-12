package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// header is the start of a valid contract, seven lines long. Cases append
// to it, so their problems start on line 8.
const header = `apiVersion: 0cular/v1alpha1
kind: Contract
metadata:
  name: bot-features
artifact:
  format: json
  maxBytes: 1MiB
`

func ptr[T any](v T) *T { return &v }

var ignorePos = cmpopts.IgnoreUnexported(Contract{})

// fullContract sets every field a contract can hold.
const fullContract = `apiVersion: 0cular/v1alpha1
kind: Contract
metadata:
  name: rate-limit-policies
artifact:
  format: json
  maxBytes: 2MiB
  maxDepth: 32
  maxNodes: 500000
schema:
  jsonSchema: schemas/rate-limit-policies.schema.json
invariants:
  - name: every-policy-has-a-limit
    expr: self.policies.all(p, has(p.limit) && p.limit != null && p.limit > 0)
    message: consumers dereference limit without a null check
  - name: unique-ids
    expr: >-
      zerocular.unique(
      self.policies.map(p, p.id))
diffBudgets:
  - name: policy-count-stable
    measure: size(self.policies)
    maxChangePct: 20
    maxIncrease: 50
    maxDecrease: 0.5
  - name: policy-churn
    collection: self.policies
    key: item.id
    maxRemoved: 5
    maxAdded: 0
    maxChangedPct: 10
    overridable: false
onFailure:
  noLastKnownGood: open
  trustLastKnownGood: true
  parseError: stale
  invariantViolation: stale
  fetchError: stale
`

func TestParse(t *testing.T) {
	src := fullContract
	got, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(src))
	want := &Contract{
		Metadata: Metadata{Name: "rate-limit-policies"},
		Artifact: Artifact{Format: FormatJSON, MaxBytes: 2 << 20, MaxDepth: 32, MaxNodes: 500_000},
		Schema:   &Schema{JSONSchema: "schemas/rate-limit-policies.schema.json"},
		Invariants: []Invariant{
			{
				Name:    "every-policy-has-a-limit",
				Expr:    "self.policies.all(p, has(p.limit) && p.limit != null && p.limit > 0)",
				Message: "consumers dereference limit without a null check",
			},
			{Name: "unique-ids", Expr: "zerocular.unique( self.policies.map(p, p.id))"},
		},
		DiffBudgets: []DiffBudget{
			{
				Name:         "policy-count-stable",
				Measure:      "size(self.policies)",
				MaxChangePct: ptr(20.0),
				MaxIncrease:  ptr(50.0),
				MaxDecrease:  ptr(0.5),
				Overridable:  true,
			},
			{
				Name:          "policy-churn",
				Collection:    "self.policies",
				Key:           "item.id",
				MaxRemoved:    ptr[int64](5),
				MaxAdded:      ptr[int64](0),
				MaxChangedPct: ptr(10.0),
			},
		},
		OnFailure: OnFailure{NoLastKnownGood: PolicyOpen, TrustLastKnownGood: true},
		Digest:    "sha256:" + hex.EncodeToString(sum[:]),
	}
	if diff := cmp.Diff(want, got, ignorePos); diff != "" {
		t.Errorf("Parse() mismatch (-want +got):\n%s", diff)
	}
	if got.DiffBudgets[0].IsCollection() || !got.DiffBudgets[1].IsCollection() {
		t.Error("IsCollection() is wrong")
	}
}

func TestParseDefaults(t *testing.T) {
	got, err := Parse([]byte(header))
	if err != nil {
		t.Fatal(err)
	}
	want := &Contract{
		Metadata:  Metadata{Name: "bot-features"},
		Artifact:  Artifact{Format: FormatJSON, MaxBytes: 1 << 20, MaxDepth: DefaultMaxDepth, MaxNodes: DefaultMaxNodes},
		OnFailure: OnFailure{NoLastKnownGood: PolicyClosed},
	}
	if diff := cmp.Diff(want, got, ignorePos, cmpopts.IgnoreFields(Contract{}, "Digest")); diff != "" {
		t.Errorf("Parse() mismatch (-want +got):\n%s", diff)
	}
	budget, err := Parse([]byte(header + "diffBudgets:\n  - {name: size, measure: meta.bytes, maxChangePct: 50}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !budget.DiffBudgets[0].Overridable {
		t.Error("a budget isn't overridable by default; want overridable")
	}
}

// TestLoadReplayContracts loads the replay suite's contracts, which are the
// shapes 0cular must handle first.
func TestLoadReplayContracts(t *testing.T) {
	files, err := filepath.Glob("../replay/*/contract.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 3 {
		t.Fatalf("found %d replay contracts, want at least 3", len(files))
	}
	for _, file := range files {
		c, err := LoadFile(file)
		if err != nil {
			t.Errorf("LoadFile(%s): %v", file, err)
			continue
		}
		if c.Path != file {
			t.Errorf("LoadFile(%s).Path = %q", file, c.Path)
		}
		if len(c.Invariants) == 0 {
			t.Errorf("%s: no invariants; every replay contract needs one to block its candidate", file)
		}
		if c.Schema == nil || len(c.Schema.Data) == 0 {
			t.Errorf("%s: LoadFile didn't read the schema file", file)
		}
	}
}

// problemCase is an invalid contract and the problems Parse reports for it.
type problemCase struct {
	name string
	src  string
	want string
	// schemaMisses, when set, says why the contract JSON Schema accepts the
	// contract although Parse doesn't.
	schemaMisses string
}

// problemCases lists invalid contracts. The JSON Schema tests use them too.
var problemCases = []problemCase{
	// The document.
	{name: "empty", src: "", want: "the contract is empty"},
	{name: "comment only", src: "# nothing\n", want: "the contract is empty"},
	{name: "explicit empty document", src: "---\n", want: "the contract is empty"},
	{name: "two documents", src: header + "---\n" + header, want: "the file holds more than one YAML document; a contract file holds exactly one"},
	{name: "syntax error", src: "apiVersion: 0cular/v1alpha1\n  kind: Contract\n", want: "2: invalid YAML: mapping values are not allowed in this context"},
	{name: "not a mapping", src: "- apiVersion: 0cular/v1alpha1\n", want: "1:1: a contract must be a YAML mapping, not a list"},

	// The header.
	{name: "missing apiVersion", src: "kind: Contract\nmetadata: 7\n", want: "1:1: missing required field apiVersion; this build of 0cular reads 0cular/v1alpha1"},
	{name: "other apiVersion", src: "apiVersion: 0cular/v1\nkind: Contract\n", want: "1:13: apiVersion: 0cular/v1 isn't supported; this build of 0cular reads 0cular/v1alpha1"},
	{
		name: "not a contract",
		src:  "apiVersion: apps/v1\nkind: Deployment\n",
		want: "1:13: apiVersion: must be 0cular/v1alpha1; this file isn't a 0cular contract\n" +
			"2:7: kind: must be Contract",
	},
	{name: "missing kind", src: "apiVersion: 0cular/v1alpha1\n", want: "1:1: missing required field kind; it must be Contract"},
	{name: "unquoted number as apiVersion", src: "apiVersion: 1\nkind: Contract\n", want: `1:13: apiVersion: must be a string, not an integer; quote it if you mean "1"`},
	{name: "blank kind", src: "apiVersion: 0cular/v1alpha1\nkind:\n", want: "2:6: kind: has no value"},

	// Fields.
	{
		name: "missing sections",
		src:  "apiVersion: 0cular/v1alpha1\nkind: Contract\n",
		want: "1:1: missing required field metadata\n" +
			"1:1: missing required field artifact",
	},
	{name: "unknown field with a hint", src: header + "invariant: []\n", want: "8:1: invariant: unknown field; did you mean invariants?"},
	{name: "unknown field in another case", src: header + "DiffBudgets: []\n", want: "8:1: DiffBudgets: unknown field; did you mean diffBudgets?"},
	{
		name: "unknown field without a hint",
		src:  header + "owner: me\n",
		want: "8:1: owner: unknown field; the fields here are apiVersion, kind, metadata, artifact, schema, invariants, diffBudgets and onFailure",
	},
	{name: "unknown nested field", src: header + "schema:\n  jsonSchema: s.json\n  draft: 2020-12\n", want: "10:3: schema.draft: unknown field; the only field here is jsonSchema"},
	{name: "duplicate key", src: header + "artifact:\n  format: yaml\n", want: "8:1: artifact: duplicate key; it's already set on line 5"},
	{name: "blank field", src: header + "schema:\n  jsonSchema:\n", want: "9:3: schema.jsonSchema: has no value"},
	{name: "blank section", src: header + "onFailure:\n", want: "8:1: onFailure: has no value"},
	{name: "keys are strings", src: header + "? [a]\n: b\n", want: "8:3: keys must be strings, not a list"},
	{name: "section of the wrong shape", src: header + "schema: s.json\n", want: "8:9: schema: must be a mapping, not a string"},
	{name: "list of the wrong shape", src: header + "invariants:\n  name: x\n", want: "9:3: invariants: must be a list, not a mapping"},
	{name: "list item of the wrong shape", src: header + "invariants:\n  - x\n", want: "9:5: invariants[0]: must be a mapping, not a string"},
	{name: "blank list item", src: header + "invariants:\n  -\n", want: "9:4: invariants[0]: has no value"},

	// Fields from later releases.
	{
		name: "consumer tests",
		src:  header + "consumerTests:\n  - name: proxy\n",
		want: "8:1: consumerTests: consumer tests arrive in 0cular v0.3, and this build can't run them; remove them for now",
	},
	{name: "rollout", src: header + "rollout: {bake: 10m}\n", want: "8:1: rollout: rollouts arrive in 0cular v0.4, and this build can't run them; remove it for now"},
	{name: "csv", src: header + "  csv: {delimiter: ';'}\n", want: "8:3: artifact.csv: CSV artifacts aren't supported yet"},

	// YAML features a contract can't use.
	{
		name: "anchor and alias",
		src:  "apiVersion: 0cular/v1alpha1\nkind: Contract\nmetadata:\n  name: &n bot-features\nartifact:\n  format: json\n  maxBytes: 1MiB\ninvariants:\n  - name: *n\n    expr: 'true'\n",
		want: "4:9: metadata.name: anchors (&n) aren't supported in contract files\n" +
			"9:11: invariants[0].name: aliases (*n) aren't supported in contract files",
	},
	{
		name: "merge key",
		src:  header + "invariants:\n  - <<: {name: a}\n    expr: 'true'\n",
		want: "9:5: invariants[0]: merge keys (<<) aren't supported in contract files\n" +
			"9:5: invariants[0]: missing required field name",
	},
	{name: "standard tag", src: header + "schema:\n  jsonSchema: !!str s.json\n", want: "9:15: schema.jsonSchema: explicit tags (!!str) aren't supported in contract files"},
	{name: "custom tag", src: header + "onFailure: !policy {noLastKnownGood: open}\n", want: "8:12: onFailure: explicit tags (!policy) aren't supported in contract files"},

	// Values.
	{
		name: "names",
		src: "apiVersion: 0cular/v1alpha1\nkind: Contract\nmetadata:\n  name: Bot_Features\nartifact:\n  format: json\n  maxBytes: 1MiB\n" +
			"invariants:\n  - {name: -a, expr: x}\n  - {name: a-, expr: x}\n  - {name: '', expr: x}\n  - {name: " + strings.Repeat("a", 64) + ", expr: x}\n",
		want: "4:9: metadata.name: must be 1 to 63 lowercase letters, digits and hyphens, starting and ending with a letter or digit, such as bot-features\n" +
			"9:12: invariants[0].name: must be 1 to 63 lowercase letters, digits and hyphens, starting and ending with a letter or digit, such as bot-features\n" +
			"10:12: invariants[1].name: must be 1 to 63 lowercase letters, digits and hyphens, starting and ending with a letter or digit, such as bot-features\n" +
			"11:12: invariants[2].name: must be 1 to 63 lowercase letters, digits and hyphens, starting and ending with a letter or digit, such as bot-features\n" +
			"12:12: invariants[3].name: must be 1 to 63 lowercase letters, digits and hyphens, starting and ending with a letter or digit, such as bot-features",
	},
	{name: "unquoted number as a name", src: header + "invariants:\n  - {name: 2024, expr: x}\n", want: `9:12: invariants[0].name: must be a string, not an integer; quote it if you mean "2024"`},
	{
		name:         "duplicate check names",
		src:          header + "invariants:\n  - {name: cap, expr: x}\ndiffBudgets:\n  - {name: cap, measure: x, maxChangePct: 1}\n",
		want:         "11:12: diffBudgets[0].name: invariants[0] already has the name cap; each check needs its own name",
		schemaMisses: "a JSON Schema can't require a property to differ across a list's items",
	},
	{name: "empty expression", src: header + "invariants:\n  - {name: a, expr: '  '}\n", want: "9:21: invariants[0].expr: must not be empty"},
	{name: "unsupported format", src: strings.Replace(header, "format: json", "format: toml", 1), want: "6:11: artifact.format: the toml format isn't supported yet; this build reads json and yaml"},
	{name: "unknown format", src: strings.Replace(header, "format: json", "format: xml", 1), want: "6:11: artifact.format: must be json or yaml"},
	{name: "integer as a string", src: header + "  maxDepth: '64'\n", want: "8:13: artifact.maxDepth: must be an integer, not a string"},
	{name: "zero depth", src: header + "  maxDepth: 0\n", want: "8:13: artifact.maxDepth: must be at least 1"},
	{name: "depth over the ceiling", src: header + "  maxDepth: 10001\n", want: "8:13: artifact.maxDepth: must be at most 10000"},
	{name: "huge node cap", src: header + "  maxNodes: 99999999999999999999\n", want: "8:13: artifact.maxNodes: must be at most 2147483647"},
	{name: "decimal size unit", src: strings.Replace(header, "1MiB", "5MB", 1), want: "7:13: artifact.maxBytes: sizes use binary units: write 5MiB (1 MiB is 1048576 bytes)"},
	{name: "spaced size", src: strings.Replace(header, "1MiB", "5 MiB", 1), want: "7:13: artifact.maxBytes: write the size without a space, as 5MiB"},
	{name: "zero size", src: strings.Replace(header, "1MiB", "0", 1), want: "7:13: artifact.maxBytes: must be at least 1 byte"},
	{
		name:         "huge size",
		src:          strings.Replace(header, "1MiB", "99999999999GiB", 1),
		want:         "7:13: artifact.maxBytes: is too large",
		schemaMisses: "a size's upper limit depends on its unit, which a pattern can't compute",
	},
	{name: "fractional size", src: strings.Replace(header, "1MiB", "1.5", 1), want: "7:13: artifact.maxBytes: must be a size such as 5MiB, not a number"},
	{name: "schema URL", src: header + "schema:\n  jsonSchema: https://example.com/s.json\n", want: "9:15: schema.jsonSchema: must be a file path, not a URL; 0cular doesn't fetch schemas"},
	{name: "schema backslashes", src: header + "schema:\n  jsonSchema: schemas\\s.json\n", want: "9:15: schema.jsonSchema: must use forward slashes, such as schemas/bot-features.schema.json"},
	{name: "absolute schema path", src: header + "schema:\n  jsonSchema: /etc/s.json\n", want: "9:15: schema.jsonSchema: must be relative to the contract file, such as schemas/bot-features.schema.json"},
	{name: "drive schema path", src: header + "schema:\n  jsonSchema: C:/s.json\n", want: "9:15: schema.jsonSchema: must be relative to the contract file, such as schemas/bot-features.schema.json"},

	// Budgets.
	{name: "measure and collection", src: header + "diffBudgets:\n  - {name: b, measure: x, collection: y, maxChangePct: 1}\n", want: "9:5: diffBudgets[0]: set measure or collection, not both"},
	{name: "neither measure nor collection", src: header + "diffBudgets:\n  - {name: b, maxChangePct: 1}\n", want: "9:5: diffBudgets[0]: set measure, a value to compare, or collection, a keyed list to compare"},
	{name: "measure without a limit", src: header + "diffBudgets:\n  - {name: b, measure: x}\n", want: "9:5: diffBudgets[0]: a measure budget needs a limit: maxChangePct, maxIncrease or maxDecrease"},
	{
		name: "measure with collection fields",
		src:  header + "diffBudgets:\n  - name: b\n    measure: x\n    maxChangePct: 1\n    key: item.id\n    maxRemoved: 2\n",
		want: "12:10: diffBudgets[0].key: applies only to collection budgets\n" +
			"13:17: diffBudgets[0].maxRemoved: applies only to collection budgets",
	},
	{
		name: "collection without key or limit",
		src:  header + "diffBudgets:\n  - {name: b, collection: x, maxIncrease: 1}\n",
		want: "9:5: diffBudgets[0]: a collection budget needs key, an expression over item that identifies each element, such as item.id\n" +
			"9:5: diffBudgets[0]: a collection budget needs a limit: maxRemoved, maxAdded or maxChangedPct\n" +
			"9:43: diffBudgets[0].maxIncrease: applies only to measure budgets",
	},
	{
		name: "absolute limits in a budget",
		src:  header + "diffBudgets:\n  - name: b\n    measure: x\n    maxChangePct: 1\n    maxValue: 200\n",
		want: "12:5: diffBudgets[0].maxValue: budgets bound only change from the baseline; put an absolute limit in an invariant, such as size(self.items) <= 200",
	},
	{
		name: "bad limits",
		src: header + "diffBudgets:\n  - name: b\n    measure: x\n    maxChangePct: -1\n    maxDecrease: lots\n" +
			"  - name: c\n    collection: x\n    key: item.id\n    maxRemoved: 1.5\n    maxAdded: -2\n",
		want: "11:19: diffBudgets[0].maxChangePct: must not be negative\n" +
			"12:18: diffBudgets[0].maxDecrease: must be a number, not a string\n" +
			"16:17: diffBudgets[1].maxRemoved: must be an integer, not a number\n" +
			"17:15: diffBudgets[1].maxAdded: must be at least 0",
	},
	{name: "infinite limit", src: header + "diffBudgets:\n  - {name: b, measure: x, maxIncrease: .inf}\n", want: "9:40: diffBudgets[0].maxIncrease: must be a finite number"},
	{
		name: "YAML 1.1 boolean",
		src:  header + "diffBudgets:\n  - {name: b, measure: x, maxChangePct: 1, overridable: yes}\n",
		want: "9:57: diffBudgets[0].overridable: must be true or false; YAML 1.1 forms such as yes aren't booleans in 0cular",
	},
	{
		name: "quoted boolean",
		src:  header + "onFailure:\n  trustLastKnownGood: 'true'\n",
		want: "9:23: onFailure.trustLastKnownGood: must be true or false, not a string",
	},

	// onFailure.
	{
		name: "failure policies",
		src:  header + "onFailure:\n  noLastKnownGood: stale\n  parseError: open\n  fetchError: retry\n",
		want: "9:20: onFailure.noLastKnownGood: must be closed or open: without a last known good, there's nothing to keep\n" +
			"10:15: onFailure.parseError: must be stale: with a last known good, every failure keeps it; open and closed apply only to noLastKnownGood\n" +
			"11:15: onFailure.fetchError: must be stale",
	},
	{name: "unknown policy", src: header + "onFailure:\n  noLastKnownGood: maybe\n", want: "9:20: onFailure.noLastKnownGood: must be closed or open"},
}

func TestParseProblems(t *testing.T) {
	for _, tt := range problemCases {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Parse([]byte(tt.src))
			if err == nil {
				t.Fatalf("Parse() = %+v, want an error", c)
			}
			if c != nil {
				t.Errorf("Parse() returned a contract and an error")
			}
			var cerr *Error
			if !errors.As(err, &cerr) || !errors.Is(err, ErrInvalid) {
				t.Fatalf("Parse() error is %T, want *Error matching ErrInvalid", err)
			}
			if got := err.Error(); got != tt.want {
				t.Errorf("Parse() problems:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestErrorString(t *testing.T) {
	problems := []Problem{
		{Message: "the whole file"},
		{Pos: Pos{Line: 2}, Message: "a line"},
		{Pos: Pos{Line: 3, Column: 5}, Path: "a.b", Message: "a field"},
	}
	for _, tt := range []struct {
		file, want string
	}{
		{file: "", want: "the whole file\n2: a line\n3:5: a.b: a field"},
		{file: "c.yaml", want: "c.yaml: the whole file\nc.yaml:2: a line\nc.yaml:3:5: a.b: a field"},
	} {
		err := &Error{File: tt.file, Problems: problems}
		if got := err.Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
		if !errors.Is(err, ErrInvalid) {
			t.Error("errors.Is(err, ErrInvalid) = false")
		}
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return file
	}

	t.Run("valid", func(t *testing.T) {
		file := write("valid.yaml", header)
		c, err := LoadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(header))
		if c.Path != file || c.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
			t.Errorf("LoadFile() Path = %q, Digest = %q", c.Path, c.Digest)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		file := write("invalid.yaml", header+"owner: me\n")
		_, err := LoadFile(file)
		if !errors.Is(err, ErrInvalid) || !strings.HasPrefix(err.Error(), file+":8:1: owner: unknown field") {
			t.Errorf("LoadFile() error = %v", err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		_, err := LoadFile(filepath.Join(dir, "missing.yaml"))
		if !errors.Is(err, fs.ErrNotExist) || errors.Is(err, ErrInvalid) {
			t.Errorf("LoadFile() error = %v, want one wrapping fs.ErrNotExist", err)
		}
	})
	t.Run("too large", func(t *testing.T) {
		file := write("large.yaml", header+"# "+strings.Repeat("x", MaxFileBytes)+"\n")
		_, err := LoadFile(file)
		if !errors.Is(err, ErrInvalid) || err.Error() != file+": the file is larger than 1 MiB, the limit for a contract" {
			t.Errorf("LoadFile() error = %v", err)
		}
	})
	t.Run("schema", func(t *testing.T) {
		if err := os.MkdirAll(filepath.Join(dir, "schemas"), 0o750); err != nil {
			t.Fatal(err)
		}
		schema := `{"type": "object"}`
		write(filepath.Join("schemas", "s.json"), schema)
		c, err := LoadFile(write("with-schema.yaml", header+"schema:\n  jsonSchema: schemas/s.json\n"))
		if err != nil {
			t.Fatal(err)
		}
		if string(c.Schema.Data) != schema {
			t.Errorf("Schema.Data = %q, want %q", c.Schema.Data, schema)
		}
	})
	t.Run("missing schema", func(t *testing.T) {
		file := write("missing-schema.yaml", header+"schema:\n  jsonSchema: schemas/missing.json\n")
		_, err := LoadFile(file)
		if !errors.Is(err, ErrInvalid) || !strings.HasPrefix(err.Error(), file+":9:15: schema.jsonSchema: can't read the schema file: ") {
			t.Errorf("LoadFile() error = %v", err)
		}
	})
	t.Run("schema too large", func(t *testing.T) {
		write("large.json", `"`+strings.Repeat("x", MaxSchemaBytes)+`"`)
		file := write("large-schema.yaml", header+"schema:\n  jsonSchema: large.json\n")
		_, err := LoadFile(file)
		if !errors.Is(err, ErrInvalid) || err.Error() != file+":9:15: schema.jsonSchema: the schema file is larger than 4 MiB, the limit for a schema" {
			t.Errorf("LoadFile() error = %v", err)
		}
	})
}

func TestParseLeavesSchemaDataNil(t *testing.T) {
	c, err := Parse([]byte(header + "schema:\n  jsonSchema: s.json\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Schema.Data != nil {
		t.Errorf("Parse() read the schema file: Data = %q", c.Schema.Data)
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		text    string
		want    int64
		wantErr string
	}{
		{text: "1", want: 1},
		{text: "512B", want: 512},
		{text: "64KiB", want: 64 << 10},
		{text: "5MiB", want: 5 << 20},
		{text: "2GiB", want: 2 << 30},
		{text: "8589934591GiB", want: 8589934591 << 30},
		{text: "8589934592GiB", wantErr: "is too large"},
		{text: "99999999999999999999", wantErr: "is too large"},
		{text: "0KiB", wantErr: "must be at least 1 byte"},
		{text: "5mib", wantErr: "must be a size"},
		{text: "5TiB", wantErr: "must be a size"},
		{text: "1.5MiB", wantErr: "must be a size"},
		{text: "-1", wantErr: "must be a size"},
		{text: "5kB", wantErr: "sizes use binary units: write 5KiB (1 KiB is 1024 bytes)"},
		{text: "5G", wantErr: "sizes use binary units: write 5GiB (1 GiB is 1073741824 bytes)"},
		{text: "5 KiB", wantErr: "write the size without a space, as 5KiB"},
	}
	for _, tt := range tests {
		got, err := parseSize(tt.text)
		if tt.wantErr != "" {
			if err == nil || !strings.HasPrefix(err.Error(), tt.wantErr) {
				t.Errorf("parseSize(%q) = %d, %v; want error %q", tt.text, got, err, tt.wantErr)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", tt.text, got, err, tt.want)
		}
	}
}

func TestPosition(t *testing.T) {
	c, err := Parse([]byte(header + "invariants:\n  - name: cap\n    expr: size(self.x) <= 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]Pos{
		"artifact.maxBytes":  {Line: 7, Column: 13},
		"invariants[0]":      {Line: 9, Column: 5},
		"invariants[0].expr": {Line: 10, Column: 11},
		"invariants[1]":      {},
		"nothing":            {},
	} {
		if got := c.Position(path); got != want {
			t.Errorf("Position(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestPosString(t *testing.T) {
	for pos, want := range map[Pos]string{{}: "", {Line: 4}: "4", {Line: 4, Column: 2}: "4:2", {Column: 2}: ""} {
		if got := pos.String(); got != want {
			t.Errorf("%#v.String() = %q, want %q", pos, got, want)
		}
	}
}
