package celenv

import (
	"context"

	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/google/cel-go/interpreter"
)

// 0cular counts the cost of an evaluation itself (ADR-0014). cel-go's own
// runtime cost tracker keeps a stack that grows with every iteration of a
// comprehension and is searched on every step, so a rule over a long list
// takes quadratic time; over a 5 MB artifact's list it runs past the
// timeout. The cost here is linear to count:
//
//   - every function call, operator and comprehension step costs 1;
//   - every list or map a step creates costs its size, and every string a
//     tenth of its length;
//   - zerocular.unique costs the size of the list it checks.
//
// Functions slower than linear, such as sort() and distinct(), cost 1 per
// call; the timeout bounds them.

// budget is the activation an evaluation starts from. Every step finds it
// through the chain of activations, and charges its cost to it. It also
// checks the evaluation's context, since cel-go can't interrupt a
// comprehension that this package's decorator has wrapped.
type budget struct {
	interpreter.Activation
	ctx   context.Context
	cost  uint64
	limit uint64
	steps uint64
}

// newBudget returns the activation for one evaluation of vars.
func newBudget(ctx context.Context, vars map[string]any, limit uint64) (*budget, error) {
	act, err := interpreter.NewActivation(vars)
	if err != nil {
		return nil, err
	}
	return &budget{Activation: act, ctx: ctx, limit: limit}, nil
}

// budgetOf finds the budget an activation descends from, or nil, as when
// cel-go folds constants before any evaluation. An execution frame's
// Parent skips the frame's own activation, so the search steps into it.
func budgetOf(a interpreter.Activation) *budget {
	for a != nil {
		switch v := a.(type) {
		case *budget:
			return v
		case *interpreter.ExecutionFrame:
			a = v.Activation
		default:
			a = a.Parent()
		}
	}
	return nil
}

// charge adds n to the cost. It stops the evaluation once the cost is over
// the limit, or, checking every interruptCheckFrequency steps, once the
// context has ended.
//
// It stops the evaluation the way cel-go's own limits do: by panicking with
// an EvalCancelledError, which cel-go's Program.Eval recovers and returns
// as an error, and which never leaves this package (Program.Eval recovers
// too). Returning a CEL error value instead could be swallowed: true || err
// is true.
func (b *budget) charge(n uint64) {
	b.cost = saturatingAdd(b.cost, n)
	if b.cost > b.limit {
		//nolint:forbidigo // cel-go's cancellation protocol; recovered into an error, as described above.
		panic(interpreter.EvalCancelledError{Cause: interpreter.CostLimitExceeded, Message: "operation canceled: the cost limit was exceeded"})
	}
	if b.steps++; b.steps%interruptCheckFrequency == 0 && b.ctx.Err() != nil {
		//nolint:forbidigo // cel-go's cancellation protocol; recovered into an error, as described above.
		panic(interpreter.EvalCancelledError{Cause: interpreter.ContextCancelled, Message: "operation canceled: the context ended"})
	}
}

// chargeStep charges a step that produced v.
func chargeStep(a interpreter.Activation, v ref.Val) {
	if b := budgetOf(a); b != nil {
		b.charge(1 + createdSize(v))
	}
}

// createdSize is what a value costs to create: its size for a list or map,
// and a tenth of its length for a string. A comprehension's accumulator is
// a mutable list that each step returns again; it's charged once, as an
// immutable list, when the comprehension ends.
func createdSize(v ref.Val) uint64 {
	switch v := v.(type) {
	case types.String:
		return uint64(len(v)) / 10
	case traits.MutableLister:
		return 0
	case traits.Lister:
		return sizeOf(v)
	case traits.Mapper:
		return sizeOf(v)
	}
	return 0
}

// sizeOf returns the size of a list or map.
func sizeOf(v traits.Sizer) uint64 {
	if n, ok := v.Size().(types.Int); ok && n > 0 {
		return uint64(n)
	}
	return 0
}

// countCost decorates each step of a program so that it charges its cost.
// Attributes, constants and literals cost nothing.
func countCost(i interpreter.InterpretableV2) (interpreter.InterpretableV2, error) {
	switch step := i.(type) {
	case interpreter.InterpretableAttribute, interpreter.InterpretableConst, interpreter.InterpretableConstructor:
		return i, nil
	case interpreter.InterpretableCall:
		if step.OverloadID() == uniqueOverload && len(step.Args()) == 1 {
			return &uniqueCall{InterpretableV2: i, arg: step.Args()[0]}, nil
		}
	}
	return &counted{InterpretableV2: i}, nil
}

// counted charges a step: a call, an operator or a comprehension. cel-go
// evaluates with Exec, or with Eval through its older interface.
type counted struct {
	interpreter.InterpretableV2
}

// Eval evaluates the step and charges it.
func (c *counted) Eval(a interpreter.Activation) ref.Val {
	v := c.InterpretableV2.Eval(a)
	chargeStep(a, v)
	return v
}

// Exec evaluates the step and charges it.
func (c *counted) Exec(f *interpreter.ExecutionFrame) ref.Val {
	v := c.InterpretableV2.Exec(f)
	chargeStep(f, v)
	return v
}

// uniqueCall evaluates zerocular.unique itself, so that it can charge the
// list's size before checking it.
type uniqueCall struct {
	interpreter.InterpretableV2
	arg interpreter.Interpretable
}

// Eval evaluates the list, charges its size, and checks it.
func (u *uniqueCall) Eval(a interpreter.Activation) ref.Val {
	return u.check(a, u.arg.Eval(a))
}

// Exec evaluates the list, charges its size, and checks it.
func (u *uniqueCall) Exec(f *interpreter.ExecutionFrame) ref.Val {
	if arg, ok := u.arg.(interpreter.InterpretableV2); ok {
		return u.check(f, arg.Exec(f))
	}
	return u.check(f, u.arg.Eval(f))
}

func (u *uniqueCall) check(a interpreter.Activation, list ref.Val) ref.Val {
	if types.IsUnknownOrError(list) {
		return list
	}
	if b := budgetOf(a); b != nil {
		b.charge(1 + valueCost(list))
	}
	return unique(list)
}
