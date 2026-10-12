package celenv

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker"
	"github.com/google/cel-go/common"
	celast "github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/parser"

	"github.com/itillema/0cular/internal/suggest"
)

// This file holds what contract lint asks of an expression without
// evaluating it: hints for compile errors, a static cost estimate, and the
// mistakes that type-check but always fail when they run.

// maxSourceRunes bounds how much of an expression a message quotes.
const maxSourceRunes = 80

// identifier matches the function and macro names worth suggesting, which
// leaves out operators such as _+_.
var identifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.]*$`)

// namesOf returns the names an expression in env can refer to, for
// suggestions: its variables first, then its functions and macros, each
// group sorted, so that ties between equally close names break the same
// way every time.
func namesOf(env *cel.Env) []string {
	var vars, funcs []string
	for _, v := range env.Variables() {
		vars = append(vars, v.Name())
	}
	for name := range env.Functions() {
		if identifier.MatchString(name) {
			funcs = append(funcs, name)
		}
	}
	for _, m := range env.Macros() {
		if identifier.MatchString(m.Function()) {
			funcs = append(funcs, m.Function())
		}
	}
	slices.Sort(vars)
	slices.Sort(funcs)
	return append(vars, slices.Compact(funcs)...)
}

// problems turns cel-go's compile errors into problems, with hints. cel-go
// reads a mistyped function in a namespace, such as math.greatst(x), as a
// call on an undeclared variable: it reports math and greatst separately,
// and problems makes them one problem about math.greatst.
func (e *Env) problems(errs []*common.Error) []Problem {
	var out []Problem
	for i := 0; i < len(errs); i++ {
		message, loc := errs[i].Message, errs[i].Location
		if ns, ok := undeclared(message); ok && e.isNamespace(ns) && i+1 < len(errs) && errs[i+1].Location.Line() == loc.Line() {
			if fn, ok := undeclared(errs[i+1].Message); ok {
				message = "undeclared reference to '" + ns + "." + fn + "'"
				i++
			}
		}
		out = append(out, problemAt(loc, e.hint(message)))
	}
	return out
}

// undeclared returns the name in an "undeclared reference" error message.
func undeclared(message string) (string, bool) {
	name, ok := strings.CutPrefix(strings.TrimSuffix(message, " (in container '')"), "undeclared reference to '")
	if !ok {
		return "", false
	}
	return strings.CutSuffix(name, "'")
}

// isNamespace reports whether name is the namespace of some function, such
// as math.
func (e *Env) isNamespace(name string) bool {
	return slices.ContainsFunc(e.names, func(n string) bool { return strings.HasPrefix(n, name+".") })
}

// hint returns a compile error's message with a suggestion when the
// expression likely meant something else, and without cel-go's note about
// containers, which contract expressions don't use.
func (e *Env) hint(message string) string {
	message = strings.TrimSuffix(message, " (in container '')")
	name, ok := undeclared(message)
	if !ok {
		return message
	}
	if e.kind == Keys {
		if _, ok := suggest.Closest(name, []string{VarSelf, VarBaseline, VarMeta}); ok {
			return message + "; a key reads only item, the element it identifies"
		}
	}
	if best, ok := suggest.Closest(name, e.names); ok {
		return message + "; did you mean " + best + "?"
	}
	if ns, _, ok := strings.Cut(name, "."); ok && e.isNamespace(ns) {
		return message + "; " + ns + " has no function by that name"
	}
	return message
}

// Value is a value whose size a static cost estimate depends on.
type Value struct {
	// Path names the value: a variable, then field names, and "@items",
	// "@keys" or "@values" for an element, a key or a value of the list or
	// map before it. It's nil for a value that isn't reached from a
	// variable, such as a function's result.
	Path []string
	// Iterated reports whether the expression loops over the value.
	Iterated bool
	// Source is the expression that produces the value, as written, cut to
	// 80 characters. It starts with @ for a value that CEL's macros build
	// internally.
	Source string
}

// SizeFunc returns the largest size that a value can have: a list's or a
// map's number of entries, or a string's length.
type SizeFunc func(v Value) uint64

// EstimateCost returns the most one evaluation of the program can cost
// when every value is within the sizes that size gives, by cel-go's static
// estimate. Its units are close to the runtime cost's, but not the same
// (ADR-0014).
func (p *Program) EstimateCost(size SizeFunc) (cost uint64, err error) {
	defer func() {
		if r := recover(); r != nil {
			cost, err = 0, fmt.Errorf("celenv: estimating the cost failed: %v", r)
		}
	}()
	native := p.ast.NativeRep()
	iterated := map[int64]bool{}
	for _, c := range celast.MatchDescendants(celast.NavigateAST(native), celast.KindMatcher(celast.ComprehensionKind)) {
		iterated[c.AsComprehension().IterRange().ID()] = true
	}
	estimate, err := p.env.EstimateCost(p.ast, sizeEstimator{program: p, size: size, iterated: iterated})
	if err != nil {
		return 0, fmt.Errorf("celenv: estimating the cost: %w", err)
	}
	return estimate.Max, nil
}

// sizeEstimator answers cel-go's questions about sizes with a SizeFunc.
type sizeEstimator struct {
	program  *Program
	size     SizeFunc
	iterated map[int64]bool
}

// EstimateSize sizes a value that can have a size. cel-go knows that a
// scalar's size is 1, so it gets no answer for one.
func (s sizeEstimator) EstimateSize(n checker.AstNode) *checker.SizeEstimate {
	if t := n.Type(); t != nil {
		switch t.Kind() {
		case types.StringKind, types.BytesKind, types.ListKind, types.MapKind, types.DynKind, types.AnyKind, types.TypeParamKind:
		default:
			return nil
		}
	}
	v := Value{Path: n.Path()}
	if e := n.Expr(); e != nil {
		v.Iterated, v.Source = s.iterated[e.ID()], s.program.source(e)
	}
	return &checker.SizeEstimate{Min: 0, Max: s.size(v)}
}

// EstimateCallCost leaves every function's cost to the estimates the
// environment declares.
func (sizeEstimator) EstimateCallCost(string, string, *checker.AstNode, []checker.AstNode) *checker.CallEstimate {
	return nil
}

// ListSource returns the path of the list that the list the expression
// returns comes from, when it can tell: self.policies, for self.policies
// itself and for self.policies.filter(...) or .map(...), which return at
// most one element for each of its own. same reports whether the elements
// are the list's own, as filter's are, rather than values that map
// computed from them.
func (p *Program) ListSource() (path []string, same, ok bool) {
	native := p.ast.NativeRep()
	e, same := native.Expr(), true
	for e.Kind() == celast.ComprehensionKind {
		macro, ok := native.SourceInfo().GetMacroCall(e.ID())
		if !ok || macro.Kind() != celast.CallKind {
			return nil, false, false
		}
		switch macro.AsCall().FunctionName() {
		case "filter":
		case "map":
			same = false
		default:
			return nil, false, false
		}
		e = e.AsComprehension().IterRange()
	}
	for e.Kind() == celast.SelectKind && !e.AsSelect().IsTestOnly() {
		path = append(path, e.AsSelect().FieldName())
		e = e.AsSelect().Operand()
	}
	if e.Kind() != celast.IdentKind {
		return nil, false, false
	}
	path = append(path, e.AsIdent())
	slices.Reverse(path)
	return path, same, true
}

// Reads reports whether the expression reads the variable name, other than
// through a comprehension variable of the same name.
func (p *Program) Reads(name string) bool {
	return len(celast.MatchDescendants(celast.NavigateAST(p.ast.NativeRep()), func(e celast.NavigableExpr) bool {
		return e.Kind() == celast.IdentKind && e.AsIdent() == name && !shadowed(e, name)
	})) > 0
}

// arithmetic maps the arithmetic functions to their operators.
var arithmetic = map[string]string{
	operators.Add:      "+",
	operators.Subtract: "-",
	operators.Multiply: "*",
	operators.Divide:   "/",
	operators.Modulo:   "%",
}

// number is what kind of number an operand is when the expression runs.
type number int

const (
	// notNumber is an operand that isn't a number, or that can't be told.
	notNumber number = iota
	// intLiteral is an int or uint literal, such as 1.
	intLiteral
	// intValue is any other int: a value of static type int or uint, or
	// meta.bytes.
	intValue
	// doubleNumber is a double literal, or a value of static type double.
	doubleNumber
	// decoded is a dyn value from the artifact, whose numbers are doubles.
	decoded
)

// MixedArithmetic returns a problem for each arithmetic operator that mixes
// an int with a double when the expression runs, which fails there: CEL has
// no overload for the pair. Numbers from the artifact decode as doubles, so
// self.x + 1 type-checks but fails; it needs self.x + 1.0. meta.bytes is an
// int, so meta.bytes * 1.5 fails the same way. CEL's % takes only ints, so
// self.x % 2 needs int(self.x) % 2.
func (p *Program) MixedArithmetic() []Problem {
	native := p.ast.NativeRep()
	var problems []Problem
	for _, e := range celast.MatchDescendants(celast.NavigateAST(native), celast.KindMatcher(celast.CallKind)) {
		op, ok := arithmetic[e.AsCall().FunctionName()]
		kids := e.Children()
		if !ok || e.AsCall().IsMemberFunction() || len(kids) != 2 {
			continue
		}
		if message := p.mixed(e, op, kids[0], kids[1]); message != "" {
			problems = append(problems, problemAt(native.SourceInfo().GetStartLocation(e.ID()), message))
		}
	}
	slices.SortStableFunc(problems, func(a, b Problem) int {
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return a.Column - b.Column
	})
	return problems
}

// mixed describes how the operation e, left op right, mixes an int with a
// double, or returns "" if it doesn't.
func (p *Program) mixed(e celast.NavigableExpr, op string, left, right celast.NavigableExpr) string {
	l, r := p.number(left), p.number(right)
	whole := p.source(e)
	if op == "%" {
		for _, side := range []celast.NavigableExpr{left, right} {
			if p.number(side) == decoded {
				return fmt.Sprintf("%s fails when it runs: numbers from the artifact are doubles, and %% takes only ints; write int(%s)", whole, p.source(side))
			}
		}
		return ""
	}
	if r == decoded {
		l, r, left, right = r, l, right, left
	}
	switch {
	case l == decoded && r == intLiteral:
		return fmt.Sprintf("%s fails when it runs: numbers from the artifact are doubles, and %s is an int; write %s.0", whole, p.source(right), p.source(right))
	case l == decoded && r == intValue:
		return fmt.Sprintf("%s fails when it runs: numbers from the artifact are doubles, and %s is an int; write double(%s)", whole, p.source(right), p.source(right))
	}
	for _, pair := range [][2]celast.NavigableExpr{{left, right}, {right, left}} {
		if isMetaBytes(pair[0]) && p.number(pair[1]) == doubleNumber {
			return fmt.Sprintf("%s fails when it runs: meta.bytes is an int, and %s is a double; write double(meta.bytes)", whole, p.source(pair[1]))
		}
	}
	return ""
}

// number tells what kind of number e is when the expression runs.
func (p *Program) number(e celast.NavigableExpr) number {
	if e.Kind() == celast.LiteralKind {
		switch e.AsLiteral().(type) {
		case types.Int, types.Uint:
			return intLiteral
		case types.Double:
			return doubleNumber
		}
		return notNumber
	}
	if isMetaBytes(e) {
		return intValue
	}
	if e.Kind() == celast.SelectKind && isGlobal(e.AsSelect().Operand(), e, VarMeta) {
		return notNumber // meta's other fields are strings.
	}
	switch e.Type().Kind() {
	case types.IntKind, types.UintKind:
		return intValue
	case types.DoubleKind:
		return doubleNumber
	case types.DynKind:
		return decoded
	}
	return notNumber
}

// isMetaBytes reports whether e is meta.bytes, read from the meta variable.
func isMetaBytes(e celast.NavigableExpr) bool {
	return e.Kind() == celast.SelectKind && !e.AsSelect().IsTestOnly() && e.AsSelect().FieldName() == "bytes" &&
		isGlobal(e.AsSelect().Operand(), e, VarMeta)
}

// isGlobal reports whether operand, the operand of e, is the variable name
// rather than a comprehension variable of the same name.
func isGlobal(operand celast.Expr, e celast.NavigableExpr, name string) bool {
	return operand.Kind() == celast.IdentKind && operand.AsIdent() == name && !shadowed(e, name)
}

// source returns e as the expression writes it, cut to maxSourceRunes.
func (p *Program) source(e celast.Expr) string {
	s, err := parser.Unparse(e, p.ast.NativeRep().SourceInfo())
	if err != nil {
		return "the operand"
	}
	return cut(s, maxSourceRunes)
}
