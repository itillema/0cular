// Package celenv is the CEL environment that contract expressions run in
// (ADR-0003): the variables they read, the cel-go extensions at pinned
// versions, the zerocular.unique function, and the cost and time limits on
// every evaluation.
package celenv

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common"
	celast "github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/ext"
	"github.com/google/cel-go/interpreter"
)

// Pinned extension versions. Raising one can change how a contract
// behaves, so it's a change for the release notes (ADR-0003).
const (
	// StringsVersion 5 is the first that estimates every function's cost.
	StringsVersion = 5
	// ListsVersion 4 has the functions of version 3, with the corrected
	// cost estimates for distinct, flatten and sort.
	ListsVersion = 4
	// SetsVersion 0 has every sets function in cel-go v0.30.0.
	SetsVersion = 0
	// MathVersion 3 is the newest.
	MathVersion = 3
)

// The default limits on one evaluation. A caller may lower them, never
// raise them.
const (
	DefaultCostLimit = 1_000_000
	DefaultTimeout   = 2 * time.Second
)

// interruptCheckFrequency is how many comprehension iterations run between
// checks of the context, so that a timeout stops a long loop. Cost tracking
// alone doesn't bound time.
const interruptCheckFrequency = 100

// The variables contract expressions read.
const (
	// VarSelf is the candidate.
	VarSelf = "self"
	// VarBaseline is the baseline, or null without one.
	VarBaseline = "baseline"
	// VarMeta describes the candidate: meta.bytes, meta.sha256, meta.format
	// and meta.key.
	VarMeta = "meta"
	// VarItem is one element of a collection, for a budget's key.
	VarItem = "item"
	// VarItems is a collection's elements, for pairing them with their keys.
	VarItems = "items"
)

// Meta describes the version an expression reads as meta. Pass its Value
// as the meta variable.
type Meta struct {
	// Bytes is the artifact's size.
	Bytes int64
	// SHA256 is the hex SHA-256 of the artifact.
	SHA256 string
	// Format is the artifact's format, such as json.
	Format string
	// Key is the artifact's key in its store, or its file path.
	Key string
}

// metaFields are meta's fields.
var metaFields = []string{"bytes", "sha256", "format", "key"}

// Value returns m as the meta variable's value. meta is a map rather than a
// typed object, since cel-go's native types slow down every value it
// converts; Compile checks the fields an expression reads instead.
func (m Meta) Value() map[string]any {
	return map[string]any{"bytes": m.Bytes, "sha256": m.SHA256, "format": m.Format, "key": m.Key}
}

// Errors that Program.Eval wraps.
var (
	// ErrCostLimit means the evaluation used up its cost limit.
	ErrCostLimit = errors.New("the expression exceeded its cost limit")
	// ErrTimeout means the evaluation ran out of time.
	ErrTimeout = errors.New("the expression timed out")
	// ErrEval means the evaluation failed, such as on a missing field or a
	// division by zero.
	ErrEval = errors.New("the expression failed")
	// ErrResult means the expression returned the wrong kind of value.
	ErrResult = errors.New("the expression returned the wrong kind of value")
)

// Kind is which variables an environment declares.
type Kind int

const (
	// Rules is the environment for invariants and measures: self, baseline
	// and meta.
	Rules Kind = iota
	// Keys is the environment for a collection budget's key: item.
	Keys
	// Items is the environment that pairs a collection's elements with
	// their keys: items.
	Items
)

// Limits bound one evaluation. A zero field takes its default.
type Limits struct {
	// Cost is the runtime cost limit, at most DefaultCostLimit.
	Cost uint64
	// Timeout is the time limit, at most DefaultTimeout.
	Timeout time.Duration
}

// Env compiles contract expressions.
type Env struct {
	env    *cel.Env
	limits Limits
}

// New returns an environment of the given kind. Limits above the defaults
// are an error.
func New(kind Kind, limits Limits) (*Env, error) {
	if limits.Cost == 0 {
		limits.Cost = DefaultCostLimit
	}
	if limits.Timeout == 0 {
		limits.Timeout = DefaultTimeout
	}
	if limits.Cost > DefaultCostLimit || limits.Timeout < 0 || limits.Timeout > DefaultTimeout {
		return nil, fmt.Errorf("celenv: limits can only be lowered: cost %d (at most %d), timeout %v (at most %v)",
			limits.Cost, DefaultCostLimit, limits.Timeout, DefaultTimeout)
	}
	opts := []cel.EnvOption{
		ext.Strings(ext.StringsVersion(StringsVersion)),
		// lists.range can't build a list larger than the cost limit allows.
		ext.Lists(ext.ListsVersion(ListsVersion), ext.ListsMaxRangeSize(DefaultCostLimit)),
		ext.Sets(ext.SetsVersion(SetsVersion)),
		ext.Math(ext.MathVersion(MathVersion)),
		cel.CrossTypeNumericComparisons(true),
		// Explain unparses the macro calls in its explanations.
		cel.EnableMacroCallTracking(),
		uniqueFunction(),
	}
	switch kind {
	case Rules:
		opts = append(opts,
			cel.Variable(VarSelf, cel.DynType),
			cel.Variable(VarBaseline, cel.DynType),
			cel.Variable(VarMeta, cel.MapType(cel.StringType, cel.DynType)),
		)
	case Keys:
		opts = append(opts, cel.Variable(VarItem, cel.DynType))
	case Items:
		opts = append(opts, cel.Variable(VarItems, cel.DynType))
	default:
		return nil, fmt.Errorf("celenv: unknown environment kind %d", kind)
	}
	env, err := cel.NewEnv(opts...)
	if err != nil {
		return nil, fmt.Errorf("celenv: %w", err)
	}
	return &Env{env: env, limits: limits}, nil
}

// Result is the kind of value an expression must return.
type Result int

const (
	// Bool is for invariants.
	Bool Result = iota
	// Number is for measures: an int, uint or double.
	Number
	// List is for collections.
	List
	// Any is for collection keys.
	Any
)

func (r Result) String() string {
	switch r {
	case Bool:
		return "a boolean"
	case Number:
		return "a number"
	case List:
		return "a list"
	default:
		return "a value"
	}
}

// Problem is one thing wrong with an expression.
type Problem struct {
	// Line and Column locate the problem within the expression, counting
	// from 1. Either is 0 if it's unknown.
	Line, Column int
	// Message describes the problem.
	Message string
}

// CompileError lists what's wrong with an expression.
type CompileError struct {
	// Problems is never empty.
	Problems []Problem
}

// Error returns one problem per line, each after its position.
func (e *CompileError) Error() string {
	lines := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		switch {
		case p.Line > 0 && p.Column > 0:
			lines[i] = fmt.Sprintf("%d:%d: %s", p.Line, p.Column, p.Message)
		case p.Line > 0:
			lines[i] = fmt.Sprintf("%d: %s", p.Line, p.Message)
		default:
			lines[i] = p.Message
		}
	}
	return strings.Join(lines, "\n")
}

// Program is a compiled expression, ready to evaluate any number of times,
// from any number of goroutines.
type Program struct {
	ast *cel.Ast
	prg cel.Program
	// tracking records every subexpression's value, for Explain. Only
	// boolean programs have one.
	tracking cel.Program
	// env compiles the bodies of all() calls that Explain looks into.
	env    *cel.Env
	want   Result
	limits Limits
}

// Compile parses and type-checks expr, which must return want. An invalid
// expression returns a *CompileError.
func (e *Env) Compile(expr string, want Result) (*Program, error) {
	ast, issues := e.env.Compile(expr)
	if issues != nil && issues.Err() != nil {
		var problems []Problem
		for _, ce := range issues.Errors() {
			problems = append(problems, problemAt(ce.Location, ce.Message))
		}
		return nil, &CompileError{Problems: problems}
	}
	if problems := checkMeta(ast); len(problems) > 0 {
		return nil, &CompileError{Problems: problems}
	}
	if !resultMatches(ast.OutputType(), want) {
		return nil, &CompileError{Problems: []Problem{{
			Message: fmt.Sprintf("the expression returns %s, but it must return %s", typeName(ast.OutputType()), want),
		}}}
	}
	limits := []cel.ProgramOption{
		cel.CustomDecoratorV2(countCost),
		cel.InterruptCheckFrequency(interruptCheckFrequency),
	}
	prg, err := e.env.Program(ast, append(limits, cel.EvalOptions(cel.OptOptimize))...)
	if err != nil {
		return nil, &CompileError{Problems: []Problem{{Message: err.Error()}}}
	}
	p := &Program{ast: ast, prg: prg, env: e.env, want: want, limits: e.limits}
	if want == Bool {
		if p.tracking, err = e.env.Program(ast, append(limits, cel.EvalOptions(cel.OptTrackState))...); err != nil {
			return nil, &CompileError{Problems: []Problem{{Message: err.Error()}}}
		}
	}
	return p, nil
}

// resultMatches reports whether an expression's static type can produce
// want. A dyn result is checked when the expression runs.
func resultMatches(t *cel.Type, want Result) bool {
	if t.Kind() == types.DynKind || want == Any {
		return true
	}
	switch want {
	case Bool:
		return t.Kind() == types.BoolKind
	case Number:
		return t.Kind() == types.IntKind || t.Kind() == types.UintKind || t.Kind() == types.DoubleKind
	case List:
		return t.Kind() == types.ListKind
	}
	return false
}

func typeName(t *cel.Type) string {
	switch t.Kind() {
	case types.BoolKind:
		return "a boolean"
	case types.IntKind, types.UintKind, types.DoubleKind:
		return "a number (" + t.String() + ")"
	case types.StringKind:
		return "a string"
	case types.ListKind:
		return "a list"
	case types.MapKind:
		return "a map"
	case types.NullTypeKind:
		return "null"
	default:
		return t.String()
	}
}

// Eval evaluates the program with the given variables, within its limits.
// It returns the result and the cost the evaluation used. A failure wraps
// ErrCostLimit, ErrTimeout, ErrEval or ErrResult, or is ctx's own error if
// ctx ended first.
func (p *Program) Eval(ctx context.Context, vars map[string]any) (val ref.Val, cost uint64, err error) {
	defer func() {
		if r := recover(); r != nil {
			val, err = nil, fmt.Errorf("%w: internal CEL error: %v", ErrEval, r)
		}
	}()
	evalCtx, cancel := context.WithTimeout(ctx, p.limits.Timeout)
	defer cancel()
	b, err := newBudget(evalCtx, vars, p.limits.Cost)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrEval, err)
	}
	out, _, err := p.prg.ContextEval(evalCtx, b)
	cost = b.cost
	if err != nil {
		var canceled interpreter.EvalCancelledError
		switch {
		case ctx.Err() != nil:
			return nil, cost, ctx.Err()
		case errors.As(err, &canceled) && canceled.Cause == interpreter.CostLimitExceeded:
			return nil, cost, fmt.Errorf("%w of %d", ErrCostLimit, p.limits.Cost)
		case evalCtx.Err() != nil:
			return nil, cost, fmt.Errorf("%w after %v", ErrTimeout, p.limits.Timeout)
		default:
			return nil, cost, fmt.Errorf("%w: %w", ErrEval, err)
		}
	}
	if !valueMatches(out, p.want) {
		return nil, cost, fmt.Errorf("%w: it returned %s, not %s", ErrResult, valueName(out), p.want)
	}
	return out, cost, nil
}

func valueMatches(v ref.Val, want Result) bool {
	switch want {
	case Bool:
		return v.Type() == types.BoolType
	case Number:
		return v.Type() == types.IntType || v.Type() == types.UintType || v.Type() == types.DoubleType
	case List:
		return v.Type() == types.ListType
	}
	return true
}

func valueName(v ref.Val) string {
	switch v.Type() {
	case types.BoolType:
		return "a boolean"
	case types.IntType, types.UintType, types.DoubleType:
		return "a number"
	case types.StringType:
		return "a string"
	case types.ListType:
		return "a list"
	case types.MapType:
		return "a map"
	case types.NullType:
		return "null"
	default:
		return v.Type().TypeName()
	}
}

// checkMeta reports each field of meta that an expression reads but meta
// doesn't have. meta is a map, so CEL would otherwise fail on such a field
// only when the expression runs.
func checkMeta(checked *cel.Ast) []Problem {
	native := checked.NativeRep()
	selects := celast.MatchDescendants(celast.NavigateAST(native), func(e celast.NavigableExpr) bool {
		if e.Kind() != celast.SelectKind {
			return false
		}
		operand := e.AsSelect().Operand()
		return operand.Kind() == celast.IdentKind && operand.AsIdent() == VarMeta && !shadowed(e, VarMeta)
	})
	var problems []Problem
	for _, e := range selects {
		if field := e.AsSelect().FieldName(); !slices.Contains(metaFields, field) {
			problems = append(problems, problemAt(native.SourceInfo().GetStartLocation(e.ID()), fmt.Sprintf("undefined field '%s'", field)))
		}
	}
	return problems
}

// shadowed reports whether a comprehension around e declares a variable
// called name, which hides the global of that name where e is.
func shadowed(e celast.NavigableExpr, name string) bool {
	child := e
	for parent, ok := e.Parent(); ok; parent, ok = parent.Parent() {
		if parent.Kind() == celast.ComprehensionKind {
			c := parent.AsComprehension()
			switch child.ID() {
			case c.LoopCondition().ID(), c.LoopStep().ID():
				if c.IterVar() == name || c.IterVar2() == name || c.AccuVar() == name {
					return true
				}
			case c.Result().ID():
				if c.AccuVar() == name {
					return true
				}
			}
		}
		child = parent
	}
	return false
}

// problemAt returns a problem at loc, with its column counted from 1.
func problemAt(loc common.Location, message string) Problem {
	p := Problem{Message: message}
	if loc.Line() > 0 {
		p.Line, p.Column = loc.Line(), max(loc.Column()+1, 0)
	}
	return p
}
