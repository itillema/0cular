package contract_test

import (
	"errors"
	"fmt"

	"github.com/itillema/0cular/contract"
)

func ExampleParse() {
	c, err := contract.Parse([]byte(`apiVersion: 0cular/v1alpha1
kind: Contract
metadata:
  name: bot-features
artifact:
  format: json
  maxBytes: 5MiB
invariants:
  - name: feature-cap
    expr: size(self.features) <= 200
    message: the proxy preallocates 200 features
diffBudgets:
  - name: feature-count-stable
    measure: size(self.features)
    maxChangePct: 20
`))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(c.Metadata.Name, c.Artifact.Format, c.Artifact.MaxBytes, "bytes")
	for _, inv := range c.Invariants {
		fmt.Printf("invariant %s: %s\n", inv.Name, inv.Expr)
	}
	for _, b := range c.DiffBudgets {
		fmt.Printf("budget %s: %s may change by %v%%\n", b.Name, b.Measure, *b.MaxChangePct)
	}
	// Output:
	// bot-features json 5242880 bytes
	// invariant feature-cap: size(self.features) <= 200
	// budget feature-count-stable: size(self.features) may change by 20%
}

// Parse reports every problem in a contract at once, each with its line,
// column and field.
func ExampleParse_problems() {
	_, err := contract.Parse([]byte(`apiVersion: 0cular/v1alpha1
kind: Contract
metadata:
  name: bot-features
artifact:
  format: json
  maxbytes: 5MiB
diffBudgets:
  - name: feature-count
    measure: size(self.features)
    maxValue: 200
    overridable: yes
`))
	fmt.Println(err)
	fmt.Println(errors.Is(err, contract.ErrInvalid))
	// Output:
	// 5:1: artifact: missing required field maxBytes
	// 7:3: artifact.maxbytes: unknown field; did you mean maxBytes?
	// 9:5: diffBudgets[0]: a measure budget needs a limit: maxChangePct, maxIncrease or maxDecrease
	// 11:5: diffBudgets[0].maxValue: budgets bound only change from the baseline; put an absolute limit in an invariant, such as size(self.items) <= 200
	// 12:18: diffBudgets[0].overridable: must be true or false; YAML 1.1 forms such as yes aren't booleans in 0cular
	// true
}

func ExampleLoadFile() {
	c, err := contract.LoadFile("../replay/doubled-feature-file/contract.yaml")
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(c.Metadata.Name)
	fmt.Println(c.Schema.JSONSchema)
	fmt.Println(c.OnFailure.NoLastKnownGood)
	// Output:
	// bot-features
	// schema.json
	// closed
}
