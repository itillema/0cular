package eval

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/cel-go/common/types"

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/internal/celenv"
	"github.com/itillema/0cular/internal/redact"
)

// Verdict is the outcome of checking a candidate.
type Verdict string

// The verdicts, from best to worst.
const (
	// Pass means every check passed.
	Pass Verdict = "PASS"
	// NeedsApproval means a diff budget was breached, and an approval can
	// accept the breach.
	NeedsApproval Verdict = "NEEDS_APPROVAL"
	// Block means a check failed, which no approval can accept.
	Block Verdict = "BLOCK"
)

// Kind is the step a check belongs to.
type Kind string

// The kinds of check, in the order they run.
const (
	KindEnvelope  Kind = "envelope"
	KindSchema    Kind = "schema"
	KindInvariant Kind = "invariant"
	KindBudget    Kind = "budget"
)

// Status is the outcome of one check.
type Status string

// The statuses a check can have.
const (
	// StatusPass means the check passed.
	StatusPass Status = "pass"
	// StatusFail means the candidate broke the rule. It blocks.
	StatusFail Status = "fail"
	// StatusError means the rule couldn't be evaluated: it failed, or used
	// up its cost or time limit. It blocks, since nothing was checked.
	StatusError Status = "error"
	// StatusNeedsApproval means a diff budget was breached, and an
	// approval can accept it.
	StatusNeedsApproval Status = "needs-approval"
	// StatusSkipped means the check didn't run, such as when the candidate
	// didn't decode.
	StatusSkipped Status = "skipped"
)

// Blocks reports whether a check with this status blocks the candidate.
func (s Status) Blocks() bool {
	return s == StatusFail || s == StatusError
}

// Check is the result of one check.
type Check struct {
	// Kind is the step the check belongs to.
	Kind Kind
	// Name identifies the check: an invariant's or budget's name, the
	// schema's path, or "candidate" or "baseline" for an envelope.
	Name string
	// Status is the check's outcome.
	Status Status
	// Message is the rule's own explanation from the contract, such as an
	// invariant's message. It can be empty.
	Message string
	// Evidence is what the check found, such as the values that broke a
	// rule or the error that stopped it. It can quote the artifact, so
	// reports must escape it.
	Evidence string
	// Violations lists a failed schema check's violations, at most
	// MaxViolations of them; ViolationCount says how many there are in all.
	Violations     []Violation
	ViolationCount int
	// Budget holds what a diff budget measured, or is nil for other kinds
	// of check and for a budget that didn't run.
	Budget *BudgetValues
	// Cost is the CEL cost the check used. Collection budgets with the
	// same collection and key share one diff, so only the first of them
	// reports its cost.
	Cost uint64
	// Duration is how long the check took.
	Duration time.Duration
}

// Artifact identifies a version of the artifact that was checked.
type Artifact struct {
	// Key is the artifact's key in its store, or its file path.
	Key string
	// Size is its length in bytes.
	Size int64
	// Digest is "sha256:" followed by its hex SHA-256.
	Digest string
}

// Result is the outcome of checking a candidate against a contract.
type Result struct {
	// Verdict is the overall outcome.
	Verdict Verdict
	// Contract is the contract's metadata.name, and ContractDigest the
	// digest of its file.
	Contract       string
	ContractDigest string
	// Candidate is the version that was checked, and Baseline the version
	// it was compared with, or nil if there was none.
	Candidate Artifact
	Baseline  *Artifact
	// Checks holds every check, ordered by step and then as the contract
	// lists them.
	Checks []Check
}

// Input is one version of an artifact to check.
type Input struct {
	// Data is the version's bytes.
	Data []byte
	// Key is the artifact's key in its store, or its file path. Rules read
	// it as meta.key.
	Key string
}

// Evaluator checks candidates against one contract. New compiles the
// contract's schema and CEL expressions once; Check can then run any number
// of times, from any number of goroutines.
type Evaluator struct {
	contract   *contract.Contract
	schema     *Schema
	invariants []compiledInvariant
	budgets    []compiledBudget
	// redact matches the fields whose values evidence hides (ADR-0015).
	redact *redact.Matcher
}

type compiledInvariant struct {
	contract.Invariant
	program *celenv.Program
}

// New compiles the JSON Schema and CEL expressions of c, which
// contract.LoadFile should have loaded. A schema or expression that doesn't
// compile is a problem with the contract: New returns a *contract.Error
// that places each problem in the contract file.
func New(c *contract.Contract) (*Evaluator, error) {
	env, err := newEnvs()
	if err != nil {
		return nil, err
	}
	var problems []contract.Problem
	schema, err := CompileSchema(c)
	if err != nil {
		problems = append(problems, contract.Problem{Pos: c.Position("schema.jsonSchema"), Path: "schema.jsonSchema", Message: err.Error()})
	}
	e := &Evaluator{contract: c, schema: schema, redact: redact.New(c.Report.Redact)}
	for i, inv := range c.Invariants {
		path := fmt.Sprintf("invariants[%d].expr", i)
		program, err := env.rules.Compile(inv.Expr, celenv.Bool)
		if err != nil {
			problems = append(problems, expressionProblems(c, path, err)...)
			continue
		}
		e.invariants = append(e.invariants, compiledInvariant{Invariant: inv, program: program})
	}
	for i, b := range c.DiffBudgets {
		budget, budgetProblems := compileBudget(c, i, b, env)
		problems = append(problems, budgetProblems...)
		budget.secretMeasure = budget.measure != nil && budget.measure.ReadsRedacted(e.redact.Field)
		budget.secretKeys = budget.collection != nil && budget.key != nil &&
			(budget.collection.ReadsRedacted(e.redact.Field) || budget.key.ReadsRedacted(e.redact.Field))
		e.budgets = append(e.budgets, budget)
	}
	if len(problems) > 0 {
		slices.SortStableFunc(problems, func(a, b contract.Problem) int {
			return cmp.Or(cmp.Compare(a.Pos.Line, b.Pos.Line), cmp.Compare(a.Pos.Column, b.Pos.Column))
		})
		return nil, &contract.Error{File: c.Path, Problems: problems}
	}
	return e, nil
}

// expressionProblems turns an expression's compile error into contract
// problems at the expression's place in the file.
func expressionProblems(c *contract.Contract, path string, err error) []contract.Problem {
	var ce *celenv.CompileError
	if !errors.As(err, &ce) {
		return []contract.Problem{{Pos: c.Position(path), Path: path, Message: err.Error()}}
	}
	problems := make([]contract.Problem, len(ce.Problems))
	for i, p := range ce.Problems {
		problems[i] = contract.Problem{Pos: c.Position(path), Path: path, Message: inExpression(p)}
	}
	return problems
}

// inExpression returns a problem's message, after its place in the
// expression when it has one.
func inExpression(p celenv.Problem) string {
	if p.Line > 0 {
		return fmt.Sprintf("at %d:%d in the expression: %s", p.Line, p.Column, p.Message)
	}
	return p.Message
}

// Check checks candidate against the contract, and against baseline when
// it isn't nil. Without a baseline, the diff budgets are skipped. Problems
// with either version are results, not errors: Check returns an error only
// when ctx ends first.
func (e *Evaluator) Check(ctx context.Context, candidate Input, baseline *Input) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r := &Result{
		Contract:       e.contract.Metadata.Name,
		ContractDigest: e.contract.Digest,
		Candidate:      artifactOf(candidate),
	}
	cand, check, err := e.envelope(ctx, "candidate", candidate)
	if err != nil {
		return nil, err
	}
	r.Checks = append(r.Checks, check)
	var base *Document
	if baseline != nil {
		a := artifactOf(*baseline)
		r.Baseline = &a
		if base, check, err = e.envelope(ctx, "baseline", *baseline); err != nil {
			return nil, err
		}
		if check.Status.Blocks() {
			check.Evidence = "the baseline doesn't decode under this contract: " + check.Evidence
		}
		r.Checks = append(r.Checks, check)
	}
	if e.schema != nil {
		r.Checks = append(r.Checks, e.schemaCheck(cand))
	}
	for _, inv := range e.invariants {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		check, err := e.invariantCheck(ctx, inv, cand, base, candidate.Key)
		if err != nil {
			return nil, err
		}
		r.Checks = append(r.Checks, check)
	}
	d := diffs{}
	for _, b := range e.budgets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		skip := ""
		switch {
		case cand == nil:
			skip = "the candidate didn't decode"
		case baseline == nil:
			skip = "there's no baseline to compare with"
		case base == nil:
			skip = "the baseline didn't decode"
		}
		var baseKey string
		if baseline != nil {
			baseKey = baseline.Key
		}
		check, err := e.budgetCheck(ctx, b, version{cand, candidate.Key}, version{base, baseKey}, skip, d)
		if err != nil {
			return nil, err
		}
		r.Checks = append(r.Checks, check)
	}
	r.Verdict = verdict(r.Checks)
	return r, nil
}

func artifactOf(in Input) Artifact {
	sum := sha256.Sum256(in.Data)
	return Artifact{Key: in.Key, Size: int64(len(in.Data)), Digest: "sha256:" + hex.EncodeToString(sum[:])}
}

// envelope decodes one version. It returns the document, or nil if the
// version doesn't decode, and the envelope check.
func (e *Evaluator) envelope(ctx context.Context, name string, in Input) (*Document, Check, error) {
	start := time.Now()
	a := e.contract.Artifact
	doc, err := Decode(ctx, in.Data, a)
	check := Check{Kind: KindEnvelope, Name: name}
	var de *DecodeError
	switch {
	case err == nil:
		check.Status = StatusPass
		check.Evidence = fmt.Sprintf("%s ≤ %s; valid %s", FormatSize(doc.Size), FormatSize(a.MaxBytes), strings.ToUpper(string(a.Format)))
	case errors.As(err, &de):
		check.Status = StatusFail
		check.Evidence = e.decodeEvidence(de)
	case ctx.Err() != nil:
		return nil, Check{}, ctx.Err()
	default:
		check.Status = StatusError
		check.Evidence = err.Error()
	}
	check.Duration = time.Since(start)
	return doc, check, nil
}

// decodeEvidence describes a decode error, hiding the value it quotes when
// the value's field holds a secret.
func (e *Evaluator) decodeEvidence(de *DecodeError) string {
	if de.Value == "" || !e.redact.Pointer(de.Pointer) {
		return de.Error()
	}
	hidden := *de
	hidden.Message = strings.Replace(de.Message, de.Value, redact.Marker, 1)
	hidden.Value = redact.Marker
	return hidden.Error()
}

func (e *Evaluator) schemaCheck(cand *Document) Check {
	check := Check{Kind: KindSchema, Name: e.schema.Name()}
	if cand == nil {
		check.Status, check.Evidence = StatusSkipped, "the candidate didn't decode"
		return check
	}
	start := time.Now()
	violations, total := e.schema.Validate(cand.Value)
	check.Duration = time.Since(start)
	if total == 0 {
		check.Status = StatusPass
		return check
	}
	for i, v := range violations {
		// A message can quote the value, such as one that fails a pattern.
		if e.redact.Pointer(v.Pointer) {
			violations[i].Message = redact.Marker + " fails the schema's " + keywordName(v.Keyword)
		}
	}
	check.Status, check.Violations, check.ViolationCount = StatusFail, violations, total
	check.Evidence = describeViolation(violations[0])
	if total > 1 {
		check.Evidence += fmt.Sprintf("; and %d more", total-1)
	}
	return check
}

func (e *Evaluator) invariantCheck(ctx context.Context, inv compiledInvariant, cand, base *Document, key string) (Check, error) {
	check := Check{Kind: KindInvariant, Name: inv.Name, Message: inv.Message}
	if cand == nil {
		check.Status, check.Evidence = StatusSkipped, "the candidate didn't decode"
		return check, nil
	}
	start := time.Now()
	vars := map[string]any{
		celenv.VarSelf:     cand.Value,
		celenv.VarBaseline: nil,
		celenv.VarMeta:     meta(cand, e.contract.Artifact.Format, key),
	}
	if base != nil {
		vars[celenv.VarBaseline] = base.Value
	}
	val, cost, err := inv.program.Eval(ctx, vars)
	check.Cost = cost
	switch {
	case err != nil && ctx.Err() != nil:
		return Check{}, ctx.Err()
	case err != nil:
		check.Status, check.Evidence = StatusError, err.Error()
	case val == types.True:
		check.Status = StatusPass
	default:
		check.Status = StatusFail
		if check.Evidence = inv.program.Explain(ctx, vars, e.redact.Field); check.Evidence == "" {
			check.Evidence = inv.Expr + " is false"
		}
	}
	check.Duration = time.Since(start)
	return check, nil
}

// keywordName returns the name of the schema keyword a JSON Pointer points
// to, such as pattern for /properties/token/pattern.
func keywordName(pointer string) string {
	name := pointer[strings.LastIndexByte(pointer, '/')+1:]
	if name == "" {
		return "rules"
	}
	return strings.ReplaceAll(strings.ReplaceAll(name, "~1", "/"), "~0", "~")
}

// meta returns the meta variable for doc.
func meta(doc *Document, format contract.Format, key string) map[string]any {
	return celenv.Meta{Bytes: doc.Size, SHA256: strings.TrimPrefix(doc.Digest, "sha256:"), Format: string(format), Key: key}.Value()
}

// verdict combines the checks: BLOCK if any check blocks, NEEDS_APPROVAL
// if any budget needs approval, and PASS otherwise.
func verdict(checks []Check) Verdict {
	v := Pass
	for _, c := range checks {
		switch {
		case c.Status.Blocks():
			return Block
		case c.Status == StatusNeedsApproval:
			v = NeedsApproval
		}
	}
	return v
}

// FormatSize formats a size in bytes with a binary unit and at most one
// decimal, rounded down: 512 B, 1.8 MiB, 5 MiB.
func FormatSize(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	f := float64(n)
	unit := 0
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	for f /= 1024; f >= 1024 && unit < len(units)-1; unit++ {
		f /= 1024
	}
	return strconv.FormatFloat(math.Floor(f*10)/10, 'f', -1, 64) + " " + units[unit]
}
