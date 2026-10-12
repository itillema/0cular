package celenv

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/google/cel-go/interpreter"
	"github.com/google/cel-go/parser"
)

// maxShownRunes bounds each value an explanation quotes, since values come
// from the artifact.
const maxShownRunes = 80

// comparisons maps CEL's comparison functions to their operators.
var comparisons = map[string]string{
	operators.Equals:        "==",
	operators.NotEquals:     "!=",
	operators.Less:          "<",
	operators.LessEquals:    "<=",
	operators.Greater:       ">",
	operators.GreaterEquals: ">=",
}

// flipped turns a comparison around, so `200 >= x` reads as `x <= 200`.
var flipped = map[string]string{"==": "==", "!=": "!=", "<": ">", "<=": ">=", ">": "<", ">=": "<="}

// Explain evaluates a boolean program again, recording every
// subexpression's value, and describes why it returned false. It descends
// through && to the operand that was false, through || to its last
// operand, and into all() to the first element that failed, and shows the
// values of a failed comparison:
//
//	size(self.features) = 240, not <= 200
//	fails for self.policies[3]: p.limit = null, not != null
//
// It returns "" if the program doesn't return false, or if it can't be
// evaluated again within its limits.
func (p *Program) Explain(ctx context.Context, vars map[string]any) (explanation string) {
	if p.tracking == nil {
		return ""
	}
	defer func() {
		if recover() != nil {
			explanation = ""
		}
	}()
	evalCtx, cancel := context.WithTimeout(ctx, p.limits.Timeout)
	defer cancel()
	b, err := newBudget(evalCtx, vars, p.limits.Cost)
	if err != nil {
		return ""
	}
	out, details, err := p.tracking.ContextEval(evalCtx, b)
	if err != nil || out != types.False || details == nil {
		return ""
	}
	native := p.ast.NativeRep()
	x := &explainer{ctx: evalCtx, env: p.env, vars: vars, limits: p.limits, ast: native, state: details.State()}
	return x.explain(native.Expr())
}

// explainer describes one failed evaluation from the values it recorded.
type explainer struct {
	ctx    context.Context
	env    *cel.Env
	vars   map[string]any
	limits Limits
	ast    *ast.AST
	state  interpreter.EvalState
}

func (x *explainer) value(e ast.Expr) (ref.Val, bool) {
	return x.state.Value(e.ID())
}

func (x *explainer) source(e ast.Expr) string {
	s, err := parser.Unparse(e, x.ast.SourceInfo())
	if err != nil {
		return "the expression"
	}
	return s
}

func (x *explainer) explain(e ast.Expr) string {
	switch e.Kind() {
	case ast.CallKind:
		call := e.AsCall()
		switch call.FunctionName() {
		case operators.LogicalAnd:
			for _, arg := range call.Args() {
				if v, ok := x.value(arg); ok && v == types.False {
					return x.explain(arg)
				}
			}
		case operators.LogicalOr:
			// Every operand of a false || is false. The last one is usually
			// the rule, after guards such as baseline == null.
			if args := call.Args(); len(args) > 0 {
				return x.explain(args[len(args)-1])
			}
		}
		if op, ok := comparisons[call.FunctionName()]; ok && len(call.Args()) == 2 {
			if s := x.comparison(op, call.Args()[0], call.Args()[1]); s != "" {
				return s
			}
		}
	case ast.ComprehensionKind:
		if s := x.all(e); s != "" {
			return s
		}
	}
	return x.source(e) + " is false"
}

// comparison shows the value of a failed comparison's operands, with any
// literal on the right.
func (x *explainer) comparison(op string, left, right ast.Expr) string {
	if left.Kind() == ast.LiteralKind && right.Kind() != ast.LiteralKind {
		left, right, op = right, left, flipped[op]
	}
	lv, ok := x.value(left)
	if !ok {
		return ""
	}
	text := fmt.Sprintf("%s = %s, not %s %s", x.source(left), Show(lv), op, x.source(right))
	if right.Kind() != ast.LiteralKind {
		if rv, ok := x.value(right); ok {
			text += " (" + Show(rv) + ")"
		}
	}
	return text
}

// all explains a failed all(): the first element that fails it, and why.
// cel-go folds a reference such as p.limit into one lookup, so the loop
// variable's value isn't recorded; instead, the body is evaluated again for
// each element, in order, until one fails.
func (x *explainer) all(e ast.Expr) string {
	macro, ok := x.ast.SourceInfo().GetMacroCall(e.ID())
	if !ok || macro.Kind() != ast.CallKind || macro.AsCall().FunctionName() != "all" {
		return ""
	}
	comp := e.AsComprehension()
	step := comp.LoopStep()
	if step.Kind() != ast.CallKind || len(step.AsCall().Args()) != 2 {
		return ""
	}
	rng, ok := x.value(comp.IterRange())
	if !ok {
		return ""
	}
	iterable, ok := rng.(traits.Iterable)
	if !ok {
		return ""
	}
	// Compile the body with the loop variable declared. A body that uses an
	// enclosing loop's variable won't compile alone, and isn't explained.
	env, err := x.env.Extend(cel.Variable(comp.IterVar(), cel.DynType))
	if err != nil {
		return ""
	}
	body, issues := env.Compile(x.source(step.AsCall().Args()[1]))
	if issues != nil && issues.Err() != nil {
		return ""
	}
	prg, err := env.Program(body,
		cel.CustomDecoratorV2(countCost),
		cel.InterruptCheckFrequency(interruptCheckFrequency),
		cel.EvalOptions(cel.OptTrackState),
	)
	if err != nil {
		return ""
	}
	vars := maps.Clone(x.vars)
	target := x.source(macro.AsCall().Target())
	i := 0
	for it := iterable.Iterator(); it.HasNext() == types.True; i++ {
		elem := it.Next()
		vars[comp.IterVar()] = elem
		b, err := newBudget(x.ctx, vars, x.limits.Cost)
		if err != nil {
			return ""
		}
		out, details, err := prg.ContextEval(x.ctx, b)
		if err != nil || details == nil {
			return ""
		}
		if out != types.False {
			continue
		}
		where := fmt.Sprintf("%s[%d]", target, i)
		if _, isMap := rng.(traits.Mapper); isMap {
			where = fmt.Sprintf("%s key %s", target, Show(elem))
		}
		sub := *x
		sub.vars, sub.ast, sub.state = vars, body.NativeRep(), details.State()
		return fmt.Sprintf("fails for %s: %s", where, sub.explain(sub.ast.Expr()))
	}
	return ""
}

// Show renders a CEL value for evidence, compactly and in JSON's syntax
// where it has one, cut to maxShownRunes characters.
func Show(v ref.Val) string {
	var b strings.Builder
	writeValue(&b, v)
	return cut(b.String(), maxShownRunes)
}

// writeValue writes v, stopping early once b holds more than it can show.
func writeValue(b *strings.Builder, v ref.Val) {
	if b.Len() > 4*maxShownRunes {
		return
	}
	switch v := v.(type) {
	case types.Null:
		b.WriteString("null")
	case types.Bool:
		b.WriteString(strconv.FormatBool(bool(v)))
	case types.Int:
		b.WriteString(strconv.FormatInt(int64(v), 10))
	case types.Uint:
		b.WriteString(strconv.FormatUint(uint64(v), 10))
	case types.Double:
		b.WriteString(FormatNumber(float64(v)))
	case types.String:
		q, _ := json.Marshal(string(v))
		b.Write(q)
	case traits.Lister:
		b.WriteString("[")
		first := true
		for it := v.Iterator(); it.HasNext() == types.True && b.Len() <= 4*maxShownRunes; first = false {
			if !first {
				b.WriteString(", ")
			}
			writeValue(b, it.Next())
		}
		b.WriteString("]")
	case traits.Mapper:
		var keys []ref.Val
		for it := v.Iterator(); it.HasNext() == types.True; {
			keys = append(keys, it.Next())
		}
		slices.SortFunc(keys, func(a, b ref.Val) int { return cmp.Compare(Show(a), Show(b)) })
		b.WriteString("{")
		for i, k := range keys {
			if b.Len() > 4*maxShownRunes {
				break
			}
			if i > 0 {
				b.WriteString(", ")
			}
			writeValue(b, k)
			b.WriteString(": ")
			writeValue(b, v.Get(k))
		}
		b.WriteString("}")
	default:
		fmt.Fprint(b, v.Value())
	}
}

// FormatNumber formats a double without needless decimals: 6000, not
// 6000.0 or 6e+03.
func FormatNumber(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// cut shortens s to at most n characters, ending a shortened string with
// an ellipsis.
func cut(s string, n int) string {
	count := 0
	for i := range s {
		if count == n-1 && utf8.RuneCountInString(s[i:]) > 1 {
			return s[:i] + "…"
		}
		count++
	}
	return s
}
