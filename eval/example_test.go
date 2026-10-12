package eval_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/eval"
)

func ExampleDecode() {
	artifact := contract.Artifact{Format: contract.FormatJSON, MaxBytes: 1 << 20, MaxDepth: 64, MaxNodes: 1_000_000}
	doc, err := eval.Decode(context.Background(), []byte(`{"features": ["a", "b"], "version": 7}`), artifact)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(doc.Value.(map[string]any)["version"], doc.Size)

	// A 64-bit ID written as a number can't be held exactly, so it's an error.
	_, err = eval.Decode(context.Background(), []byte(`{"id": 12345678901234567890}`), artifact)
	fmt.Println(errors.Is(err, eval.ErrNumber))
	fmt.Println(err)
	// Output:
	// 7 38
	// true
	// line 1, column 8: the integer 12345678901234567890 at /id is beyond ±9007199254740991 (2^53 − 1), the largest a double holds exactly; quote it if it's an identifier
}

func ExampleSchema_Validate() {
	// contract.LoadFile reads the schema file into Schema.Data.
	c := &contract.Contract{Schema: &contract.Schema{
		JSONSchema: "schemas/endpoint.schema.json",
		Data:       []byte(`{"properties": {"addresses": {"type": "array", "minItems": 1, "items": {"format": "ipv4"}}}}`),
	}}
	schema, err := eval.CompileSchema(c)
	if err != nil {
		fmt.Println(err)
		return
	}
	artifact := contract.Artifact{Format: contract.FormatJSON, MaxBytes: 1 << 20, MaxDepth: 64, MaxNodes: 1_000_000}
	doc, err := eval.Decode(context.Background(), []byte(`{"addresses": ["192.0.2.10", "192.0.2.300"]}`), artifact)
	if err != nil {
		fmt.Println(err)
		return
	}
	violations, total := schema.Validate(doc.Value)
	fmt.Println(total, "violation")
	for _, v := range violations {
		fmt.Printf("%s: %s\n", v.Pointer, v.Message)
	}
	// Output:
	// 1 violation
	// /addresses/1: '192.0.2.300' is not valid ipv4: decimal must be between 0 and 255
}

// YAML scalars resolve under the YAML 1.2 core schema: yes is a string, a
// leading zero doesn't make a number octal, and dates stay strings.
func ExampleDecode_yaml() {
	artifact := contract.Artifact{Format: contract.FormatYAML, MaxBytes: 1 << 20, MaxDepth: 64, MaxNodes: 1_000_000}
	doc, err := eval.Decode(context.Background(), []byte("answer: yes\nmode: 0777\nsince: 2026-10-11\n"), artifact)
	if err != nil {
		fmt.Println(err)
		return
	}
	v := doc.Value.(map[string]any)
	fmt.Printf("%q %v %q\n", v["answer"], v["mode"], v["since"])
	// Output:
	// "yes" 777 "2026-10-11"
}

func ExampleEvaluator_Check() {
	c, err := contract.Parse([]byte(`apiVersion: 0cular/v1alpha1
kind: Contract
metadata:
  name: bot-features
artifact:
  format: json
  maxBytes: 1MiB
invariants:
  - name: feature-cap
    expr: size(self.features) <= 2
    message: the proxy preallocates 2 features
`))
	if err != nil {
		fmt.Println(err)
		return
	}
	e, err := eval.New(c)
	if err != nil {
		fmt.Println(err)
		return
	}
	candidate := eval.Input{Data: []byte(`{"features": ["a", "b", "c"]}`), Key: "bot-features.json"}
	r, err := e.Check(context.Background(), candidate, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(r.Verdict)
	for _, check := range r.Checks {
		fmt.Printf("%s %s %s: %s\n", check.Status, check.Kind, check.Name, check.Evidence)
	}
	// Output:
	// BLOCK
	// pass envelope candidate: 29 B ≤ 1 MiB; valid JSON
	// fail invariant feature-cap: size(self.features) = 3, not <= 2
}
