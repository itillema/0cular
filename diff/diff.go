// Package diff compares two versions of an artifact under its contract: how
// each of the contract's diff budgets measures the change, and the JSON
// Patch (RFC 6902) that turns the baseline into the candidate, in brief.
// The 0cular diff command shows a Result with package report.
//
// A Result's values come from the artifact, so reports must escape them.
// A value read through a field that holds a secret shows [redacted]
// instead, as evidence does (ADR-0015).
//
// The package is experimental: its API may change before 0cular 1.0.
package diff

import (
	"context"
	"fmt"

	"github.com/wI2L/jsondiff"

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/eval"
	"github.com/itillema/0cular/internal/celenv"
	"github.com/itillema/0cular/internal/redact"
)

// MaxChanges is the most changes a Result lists. Its counts cover all of
// them.
const MaxChanges = 50

// Op is a JSON Patch operation.
type Op string

// The operations a patch can hold. A Result's patch has no move, copy or
// test operations.
const (
	Add     Op = "add"
	Remove  Op = "remove"
	Replace Op = "replace"
)

// Change is one operation of the JSON Patch from the baseline to the
// candidate.
type Change struct {
	// Op is the operation.
	Op Op
	// Path is the JSON Pointer (RFC 6901) of the value that the operation
	// adds, removes or replaces: "" is the whole document, and a last token
	// of "-" appends to an array. Operations apply in order, so a path
	// points into the document as the operations before it left it.
	Path string
	// Old is the value a remove or replace takes out, and New the value an
	// add or replace puts in, shown as evidence shows values: in JSON's
	// syntax, cut to 80 characters, with [redacted] for a secret. Each is
	// "" when the operation has none.
	Old, New string
}

// Result is what Compare found.
type Result struct {
	// Contract is the contract's metadata.name, and ContractDigest the
	// digest of its file.
	Contract       string
	ContractDigest string
	// Baseline and Candidate are the versions compared.
	Baseline, Candidate eval.Artifact
	// Checks holds the baseline's envelope check, then the candidate's,
	// then the contract's diff budgets, as eval.Evaluator.Measure returns
	// them.
	Checks []eval.Check
	// Compared is set when both versions decoded, so that the result has
	// their changes. When it isn't, the envelope checks say why.
	Compared bool
	// Changes lists the patch's first MaxChanges operations, in order.
	// Added, Removed and Replaced count each kind of operation in the whole
	// patch.
	Changes                  []Change
	Added, Removed, Replaced int
}

// Count returns how many operations the whole patch has.
func (r *Result) Count() int {
	return r.Added + r.Removed + r.Replaced
}

// Differ compares versions of an artifact under one contract. New compiles
// the contract once; Compare can then run any number of times, from any
// number of goroutines.
type Differ struct {
	eval *eval.Evaluator
	// secret matches the fields whose values a Result hides (ADR-0015).
	secret *redact.Matcher
}

// New compiles the JSON Schema and CEL expressions of c, which
// contract.LoadFile should have loaded, as eval.New does. A schema or
// expression that doesn't compile is a problem with the contract: New
// returns a *contract.Error that places each problem in the contract file.
func New(c *contract.Contract) (*Differ, error) {
	e, err := eval.New(c)
	if err != nil {
		return nil, err
	}
	return &Differ{eval: e, secret: redact.New(c.Report.Redact)}, nil
}

// Compare compares candidate with baseline. Problems with either version
// are results, not errors: Compare returns an error when ctx ends first,
// or if jsondiff fails on the decoded versions, which eval's decoding rules
// out. Making the patch takes time and memory in proportion to the
// versions' sizes and the number of changes, which the contract's caps on
// size and nodes bound.
func (d *Differ) Compare(ctx context.Context, baseline, candidate eval.Input) (*Result, error) {
	m, err := d.eval.Measure(ctx, baseline, candidate)
	if err != nil {
		return nil, err
	}
	r := &Result{
		Contract:       m.Contract,
		ContractDigest: m.ContractDigest,
		Baseline:       m.Baseline,
		Candidate:      m.Candidate,
		Checks:         m.Checks,
	}
	if m.BaselineDoc == nil || m.CandidateDoc == nil {
		return r, nil
	}
	patch, err := makePatch(m.BaselineDoc.Value, m.CandidateDoc.Value)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.Compared = true
	for _, op := range patch {
		switch opOf(op) {
		case Add:
			r.Added++
		case Remove:
			r.Removed++
		case Replace:
			r.Replaced++
		default:
			return nil, fmt.Errorf("diff: the patch has a %s operation", op.Type)
		}
		if len(r.Changes) < MaxChanges {
			r.Changes = append(r.Changes, d.change(op))
		}
	}
	return r, nil
}

// makePatch returns the JSON Patch from base to cand. eval decodes versions
// to the types that jsondiff compares, so neither is encoded again. A panic
// in jsondiff, which those types rule out, is an error.
func makePatch(base, cand any) (patch jsondiff.Patch, err error) {
	defer func() {
		if r := recover(); r != nil {
			patch, err = nil, fmt.Errorf("diff: comparing the versions: %v", r)
		}
	}()
	return jsondiff.CompareWithoutMarshal(base, cand)
}

// opOf returns an operation's type. jsondiff adds a value at the root to
// replace a value of another type there, which a reader knows as a
// replace; RFC 6902 allows either.
func opOf(op jsondiff.Operation) Op {
	if op.Type == jsondiff.OperationAdd && op.Path == "" {
		return Replace
	}
	return Op(op.Type)
}

// change describes an operation, hiding the values read through fields that
// hold secrets.
func (d *Differ) change(op jsondiff.Operation) Change {
	c := Change{Op: opOf(op), Path: op.Path}
	show := func(v any) string {
		if d.secret.Pointer(op.Path) {
			return redact.Marker
		}
		return celenv.ShowValue(v, d.secret.Field)
	}
	if c.Op != Add {
		c.Old = show(op.OldValue)
	}
	if c.Op != Remove {
		c.New = show(op.Value)
	}
	return c
}
