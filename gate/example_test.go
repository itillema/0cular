package gate_test

import (
	"context"
	"fmt"
	"time"

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/eval"
	"github.com/itillema/0cular/gate"
)

// rateLimits is a contract whose budget lets each limit change by at most
// half.
const rateLimits = `apiVersion: 0cular/v1alpha1
kind: Contract
metadata: {name: rate-limits}
artifact: {format: json, maxBytes: 64KiB}
invariants:
  - {name: search-limit-positive, expr: "self.search > 0", message: the API divides by the limit}
diffBudgets:
  - {name: search-limit-stable, measure: self.search, maxChangePct: 50}
`

func ExampleCheck() {
	c, err := contract.Parse([]byte(rateLimits))
	if err != nil {
		fmt.Println(err)
		return
	}
	baseline := eval.Input{Data: []byte(`{"search": 100}`), Key: "limits.json"}
	for _, candidate := range []string{`{"search": 120}`, `{"search": 1000}`, `{"search": 0}`} {
		r, err := gate.Check(context.Background(), c, eval.Input{Data: []byte(candidate), Key: "limits.json"}, &baseline)
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println(candidate, r.Verdict)
	}
	// Output:
	// {"search": 120} PASS
	// {"search": 1000} NEEDS_APPROVAL
	// {"search": 0} BLOCK
}

// An approval accepts the change its approver saw, and nothing else.
func ExampleApprove() {
	c, err := contract.Parse([]byte(rateLimits))
	if err != nil {
		fmt.Println(err)
		return
	}
	ctx := context.Background()
	baseline := eval.Input{Data: []byte(`{"search": 100}`), Key: "limits.json"}
	candidate := eval.Input{Data: []byte(`{"search": 1000}`), Key: "limits.json"}

	// The approver sees the first result, and approves it for a day.
	seen, err := gate.Check(ctx, c, candidate, &baseline)
	if err != nil {
		fmt.Println(err)
		return
	}
	approval, err := gate.Approve(seen, "alice", "search moves to the new cluster", time.Now().Add(24*time.Hour))
	if err != nil {
		fmt.Println(err)
		return
	}

	// Checking the same change with the approval passes.
	r, err := gate.Check(ctx, c, candidate, &baseline, gate.WithApproval(approval))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(r.Verdict, r.Checks[3].Name, r.Checks[3].Status)

	// A different candidate needs its own approval.
	other := eval.Input{Data: []byte(`{"search": 5000}`), Key: "limits.json"}
	r, err = gate.Check(ctx, c, other, &baseline, gate.WithApproval(approval))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(r.Verdict, r.Approvals[0].Rejection)
	// Output:
	// PASS search-limit-stable approved
	// NEEDS_APPROVAL it's for another candidate
}
