package diff_test

import (
	"context"
	"fmt"

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/diff"
	"github.com/itillema/0cular/eval"
)

func ExampleDiffer_Compare() {
	c, err := contract.Parse([]byte(`apiVersion: 0cular/v1alpha1
kind: Contract
metadata: {name: kv-endpoint}
artifact: {format: json, maxBytes: 64KiB}
diffBudgets:
  - {name: address-count-stable, measure: size(self.addresses), maxChangePct: 50}
`))
	if err != nil {
		fmt.Println(err)
		return
	}
	d, err := diff.New(c)
	if err != nil {
		fmt.Println(err)
		return
	}
	baseline := eval.Input{Data: []byte(`{"addresses": ["192.0.2.10", "192.0.2.11"], "ttl": 60, "token": "abc123"}`), Key: "endpoint.json"}
	candidate := eval.Input{Data: []byte(`{"addresses": [], "ttl": 30, "token": "def456"}`), Key: "endpoint.json"}
	r, err := d.Compare(context.Background(), baseline, candidate)
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, check := range r.Checks {
		if check.Kind == eval.KindBudget {
			fmt.Println(check.Name, check.Status, check.Evidence)
		}
	}
	fmt.Println(r.Count(), "changes:")
	for _, change := range r.Changes {
		switch change.Op {
		case diff.Add:
			fmt.Println(change.Op, change.Path, change.New)
		case diff.Remove:
			fmt.Println(change.Op, change.Path, change.Old)
		case diff.Replace:
			fmt.Println(change.Op, change.Path, change.Old, "→", change.New)
		}
	}
	// Output:
	// address-count-stable needs-approval 2 → 0 (-100%); budget ±50%
	// 4 changes:
	// remove /addresses/0 "192.0.2.10"
	// remove /addresses/0 "192.0.2.11"
	// replace /token [redacted] → [redacted]
	// replace /ttl 60 → 30
}
