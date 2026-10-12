package eval

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/itillema/0cular/contract"
)

// featureFile returns a JSON or YAML artifact of about size bytes, shaped
// like the replay suite's bot-feature file: a list of small objects.
func featureFile(format contract.Format, size int) []byte {
	var b bytes.Buffer
	if format == contract.FormatJSON {
		b.WriteString(`{"version": 7, "features": [`)
	} else {
		b.WriteString("version: 7\nfeatures:\n")
	}
	for i := 0; b.Len() < size; i++ {
		if format == contract.FormatJSON {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, "\n  {\"name\": \"feature-%d\", \"weight\": %d.25, \"enabled\": true, \"tags\": [\"bot\", \"score\"]}", i, i%1000)
		} else {
			fmt.Fprintf(&b, "  - {name: feature-%d, weight: %d.25, enabled: true, tags: [bot, score]}\n", i, i%1000)
		}
	}
	if format == contract.FormatJSON {
		b.WriteString("\n]}\n")
	}
	return b.Bytes()
}

func BenchmarkValidate(b *testing.B) {
	s, err := CompileSchema(schemaContract(`{
		"type": "object",
		"required": ["features"],
		"properties": {
			"version": {"type": "integer", "minimum": 1},
			"features": {"type": "array", "items": {
				"type": "object",
				"required": ["name", "weight"],
				"additionalProperties": false,
				"properties": {
					"name": {"type": "string", "pattern": "^feature-[0-9]+$"},
					"weight": {"type": "number", "minimum": 0},
					"enabled": {"type": "boolean"},
					"tags": {"type": "array", "items": {"enum": ["bot", "score"]}}
				}
			}}
		}
	}`))
	if err != nil {
		b.Fatal(err)
	}
	data := featureFile(contract.FormatJSON, 5<<20)
	doc, err := Decode(b.Context(), data, contract.Artifact{Format: contract.FormatJSON, MaxBytes: 8 << 20, MaxDepth: 64, MaxNodes: 10 << 20})
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if violations, _ := s.Validate(doc.Value); len(violations) > 0 {
			b.Fatal(violations)
		}
	}
}

func BenchmarkDecode(b *testing.B) {
	for _, format := range []contract.Format{contract.FormatJSON, contract.FormatYAML} {
		b.Run(string(format)+"-5MiB", func(b *testing.B) {
			data := featureFile(format, 5<<20)
			a := contract.Artifact{Format: format, MaxBytes: 8 << 20, MaxDepth: contract.DefaultMaxDepth, MaxNodes: 10 << 20}
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Decode(b.Context(), data, a); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// benchContract has ten invariants and five budgets over featureFile's
// shape, as the Phase 1 performance target describes: a 5 MB JSON candidate
// with 10 invariants and 5 budgets evaluates in under 500 ms (p95).
const benchContract = `apiVersion: 0cular/v1alpha1
kind: Contract
metadata:
  name: bot-features
artifact:
  format: json
  maxBytes: 8MiB
  maxNodes: 10000000
invariants:
  - {name: feature-cap, expr: "size(self.features) <= 100000"}
  - {name: weights-not-negative, expr: "self.features.all(f, f.weight >= 0.0)"}
  - {name: every-feature-named, expr: "self.features.all(f, has(f.name) && size(f.name) <= 64)"}
  - {name: unique-names, expr: "zerocular.unique(self.features.map(f, f.name))"}
  - {name: known-tags, expr: "self.features.all(f, f.tags.all(t, t in ['bot', 'score']))"}
  - {name: versioned, expr: "self.version >= 1"}
  - {name: some-enabled, expr: "self.features.exists(f, f.enabled)"}
  - {name: weights-bounded, expr: "self.features.all(f, f.weight < 1000.0)"}
  - {name: names-prefixed, expr: "self.features.all(f, f.name.startsWith('feature-'))"}
  - {name: small-enough, expr: "meta.bytes <= 8388608"}
diffBudgets:
  - {name: feature-count-stable, measure: size(self.features), maxChangePct: 20}
  - {name: size-stable, measure: meta.bytes, maxChangePct: 50}
  - {name: enabled-stable, measure: "self.features.filter(f, f.enabled).size()", maxChangePct: 20}
  - {name: feature-churn, collection: self.features, key: item.name, maxRemoved: 100, maxAdded: 5000}
  - {name: weight-churn, collection: self.features, key: item.name, maxChangedPct: 10}
`

const benchSchema = `{
	"type": "object",
	"required": ["features"],
	"properties": {
		"version": {"type": "integer", "minimum": 1},
		"features": {"type": "array", "items": {
			"type": "object",
			"required": ["name", "weight"],
			"additionalProperties": false,
			"properties": {
				"name": {"type": "string", "pattern": "^feature-[0-9]+$"},
				"weight": {"type": "number", "minimum": 0},
				"enabled": {"type": "boolean"},
				"tags": {"type": "array", "items": {"enum": ["bot", "score"]}}
			}
		}}
	}
}`

// BenchmarkCheck measures the Phase 1 performance target, and the same
// check with benchSchema too. The target asks for a p95 under 500 ms, so
// run single iterations to see the spread:
//
//	go test -run '^$' -bench 'BenchmarkCheck/' -benchtime 1x -count 30 ./eval
func BenchmarkCheck(b *testing.B) {
	const size = 5_000_000 // 5 MB
	data := featureFile(contract.FormatJSON, size)
	baseline := Input{Data: featureFile(contract.FormatJSON, size-100_000), Key: "bot-features.json"}
	for _, bc := range []struct {
		name   string
		schema bool
	}{{"target", false}, {"with-schema", true}} {
		b.Run(bc.name, func(b *testing.B) {
			text := benchContract
			if bc.schema {
				text += "schema:\n  jsonSchema: bot-features.schema.json\n"
			}
			c, err := contract.Parse([]byte(text))
			if err != nil {
				b.Fatal(err)
			}
			if bc.schema {
				c.Schema.Data = []byte(benchSchema)
			}
			e, err := New(c)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				r, err := e.Check(b.Context(), Input{Data: data, Key: "bot-features.json"}, &baseline)
				if err != nil {
					b.Fatal(err)
				}
				if r.Verdict != Pass || len(r.Checks) != 2+len(c.Invariants)+len(c.DiffBudgets)+btoi(bc.schema) {
					b.Fatalf("Verdict = %s: %+v", r.Verdict, r.Checks)
				}
			}
		})
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
