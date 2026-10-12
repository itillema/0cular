package eval

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/internal/celenv"
	"github.com/itillema/0cular/internal/redact"
)

// BudgetValues are what a diff budget measured. Reports show them, and an
// approval of a breach binds to them.
type BudgetValues struct {
	// Baseline and Candidate are a measure budget's values.
	Baseline, Candidate float64
	// Collection is set for a collection budget, whose values are the
	// counts below; a measure budget's are Baseline and Candidate.
	Collection bool
	// BaselineItems and CandidateItems are how many items a collection
	// budget's lists hold; Removed, Added and Changed count the items the
	// candidate removed, added and changed, matched by key.
	BaselineItems, CandidateItems int
	Removed, Added, Changed       int
	// Breached lists the limits the change exceeds, by their names in the
	// contract, such as maxChangePct.
	Breached []string
	// Redacted is set when a measure reads a field that holds a secret
	// (ADR-0015). Reports then show neither Baseline nor Candidate.
	Redacted bool
}

type compiledBudget struct {
	contract.DiffBudget
	// measure evaluates a measure budget's measure.
	measure *celenv.Program
	// collection evaluates a collection budget's collection, and keys
	// evaluates its elements' keys, in one evaluation rather than one per
	// element. key is the key alone, which Lint checks.
	collection, keys, key *celenv.Program
	// secretMeasure and secretKeys are set when the measure, or the
	// collection or its key, reads a field that holds a secret, so evidence
	// hides the values they yield (ADR-0015).
	secretMeasure, secretKeys bool
}

// envs are the CEL environments a contract's expressions compile in.
type envs struct {
	rules, keys, items *celenv.Env
}

// newEnvs returns the environments, with the default limits.
func newEnvs() (envs, error) {
	rules, err := celenv.New(celenv.Rules, celenv.Limits{})
	if err != nil {
		return envs{}, err
	}
	keys, err := celenv.New(celenv.Keys, celenv.Limits{})
	if err != nil {
		return envs{}, err
	}
	items, err := celenv.New(celenv.Items, celenv.Limits{})
	if err != nil {
		return envs{}, err
	}
	return envs{rules: rules, keys: keys, items: items}, nil
}

// compileBudget compiles a budget's expressions. A collection's key is
// compiled alone too, in an environment with only item, so that it can't
// read anything else. The budget holds every expression that compiled, for
// Lint, even when another didn't.
func compileBudget(c *contract.Contract, i int, b contract.DiffBudget, env envs) (compiledBudget, []contract.Problem) {
	path := fmt.Sprintf("diffBudgets[%d]", i)
	cb := compiledBudget{DiffBudget: b}
	if !b.IsCollection() {
		measure, err := env.rules.Compile(b.Measure, celenv.Number)
		if err != nil {
			return cb, expressionProblems(c, path+".measure", err)
		}
		cb.measure = measure
		return cb, nil
	}
	var problems []contract.Problem
	collection, err := env.rules.Compile(b.Collection, celenv.List)
	if err != nil {
		problems = append(problems, expressionProblems(c, path+".collection", err)...)
	}
	key, err := env.keys.Compile(b.Key, celenv.Any)
	if err != nil {
		problems = append(problems, expressionProblems(c, path+".key", err)...)
	}
	cb.collection, cb.key = collection, key
	if len(problems) > 0 {
		return cb, problems
	}
	// The key compiled alone above, where only item exists. The line break
	// ends any comment at the end of it.
	keys, err := env.items.Compile("items.map(item, "+b.Key+"\n)", celenv.List)
	if err != nil {
		return cb, expressionProblems(c, path+".key", err)
	}
	cb.keys = keys
	return cb, nil
}

// version is one decoded version, for a budget to measure.
type version struct {
	doc *Document
	key string
}

// diffs holds the collection diffs one Check has made, by collection and
// key, so that budgets with the same collection and key share a diff.
type diffs map[string]*collectionDiff

// collectionDiff is what diffing a collection found.
type collectionDiff struct {
	baseItems, candItems    int
	removed, added, changed int
	// cost is the CEL cost of the diff, or 0 once a budget has reported it.
	cost uint64
	err  error
}

func (e *Evaluator) budgetCheck(ctx context.Context, b compiledBudget, cand, base version, skip string, d diffs) (Check, error) {
	check := Check{Kind: KindBudget, Name: b.Name}
	if skip != "" {
		check.Status, check.Evidence = StatusSkipped, skip
		return check, nil
	}
	start := time.Now()
	var (
		values *BudgetValues
		err    error
	)
	if b.IsCollection() {
		values, check.Cost, err = e.collectionChange(ctx, b, cand, base, d)
	} else {
		values, check.Cost, err = e.measureChange(ctx, b, cand, base)
	}
	check.Duration = time.Since(start)
	switch {
	case err != nil && ctx.Err() != nil:
		return Check{}, ctx.Err()
	case err != nil:
		check.Status, check.Evidence = StatusError, err.Error()
		return check, nil
	}
	check.Budget = values
	if b.IsCollection() {
		check.Evidence = collectionEvidence(b.DiffBudget, values)
	} else {
		check.Evidence = measureEvidence(b.DiffBudget, values)
	}
	switch {
	case len(values.Breached) == 0:
		check.Status = StatusPass
	case b.Overridable:
		check.Status = StatusNeedsApproval
	default:
		check.Status = StatusFail
	}
	return check, nil
}

// vars returns the variables for evaluating an expression on one version:
// self is the version, and baseline is null.
func (e *Evaluator) vars(v version) map[string]any {
	return map[string]any{
		celenv.VarSelf:     v.doc.Value,
		celenv.VarBaseline: nil,
		celenv.VarMeta:     meta(v.doc, e.contract.Artifact.Format, v.key),
	}
}

func (e *Evaluator) measureChange(ctx context.Context, b compiledBudget, cand, base version) (*BudgetValues, uint64, error) {
	cv, ccost, err := e.number(ctx, b.measure, cand)
	if err != nil {
		return nil, ccost, fmt.Errorf("on the candidate: %w", err)
	}
	bv, bcost, err := e.number(ctx, b.measure, base)
	if err != nil {
		return nil, ccost + bcost, fmt.Errorf("on the baseline: %w", err)
	}
	return &BudgetValues{Baseline: bv, Candidate: cv, Breached: measureBreaches(b.DiffBudget, bv, cv), Redacted: b.secretMeasure}, ccost + bcost, nil
}

func (e *Evaluator) number(ctx context.Context, p *celenv.Program, v version) (float64, uint64, error) {
	val, cost, err := p.Eval(ctx, e.vars(v))
	if err != nil {
		return 0, cost, err
	}
	var f float64
	switch n := val.(type) {
	case types.Int:
		f = float64(n)
	case types.Uint:
		f = float64(n)
	case types.Double:
		f = float64(n)
	}
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, cost, fmt.Errorf("the measure is %v, not a finite number", f)
	}
	return f, cost, nil
}

// measureBreaches returns the limits that the change from base to cand
// exceeds. The comparisons are exact, so a change right at a limit is
// within it.
func measureBreaches(b contract.DiffBudget, base, cand float64) []string {
	var breached []string
	change := new(big.Rat).Sub(rat(cand), rat(base))
	if b.MaxChangePct != nil && !withinPct(new(big.Rat).Abs(change), rat(math.Abs(base)), *b.MaxChangePct) {
		breached = append(breached, "maxChangePct")
	}
	if b.MaxIncrease != nil && change.Cmp(rat(*b.MaxIncrease)) > 0 {
		breached = append(breached, "maxIncrease")
	}
	if b.MaxDecrease != nil && new(big.Rat).Neg(change).Cmp(rat(*b.MaxDecrease)) > 0 {
		breached = append(breached, "maxDecrease")
	}
	return breached
}

// withinPct reports whether part / max(whole, 1) × 100 ≤ pct, exactly.
func withinPct(part, whole *big.Rat, pct float64) bool {
	if whole.Cmp(ratOne) < 0 {
		whole = ratOne
	}
	lhs := new(big.Rat).Mul(part, ratHundred)
	rhs := new(big.Rat).Mul(rat(pct), whole)
	return lhs.Cmp(rhs) <= 0
}

var (
	ratOne     = big.NewRat(1, 1)
	ratHundred = big.NewRat(100, 1)
)

// rat returns the exact value of a finite float64.
func rat(f float64) *big.Rat {
	return new(big.Rat).SetFloat64(f)
}

// changePct is the change from base to cand, as a percentage of
// max(|base|, 1).
func changePct(base, cand float64) float64 {
	return (cand - base) / math.Max(math.Abs(base), 1) * 100
}

// collectionChange diffs a collection budget's collection and checks the
// budget's limits. The first budget with a given collection and key makes
// the diff and reports its cost; later ones reuse it, at no cost.
func (e *Evaluator) collectionChange(ctx context.Context, b compiledBudget, cand, base version, d diffs) (*BudgetValues, uint64, error) {
	id := b.Collection + "\x00" + b.Key
	diff, ok := d[id]
	if !ok {
		diff = e.diffCollection(ctx, b, cand, base)
		d[id] = diff
	}
	cost := diff.cost
	diff.cost = 0
	if diff.err != nil {
		return nil, cost, diff.err
	}
	v := &BudgetValues{
		Collection:    true,
		BaselineItems: diff.baseItems, CandidateItems: diff.candItems,
		Removed: diff.removed, Added: diff.added, Changed: diff.changed,
	}
	if b.MaxRemoved != nil && int64(v.Removed) > *b.MaxRemoved {
		v.Breached = append(v.Breached, "maxRemoved")
	}
	if b.MaxAdded != nil && int64(v.Added) > *b.MaxAdded {
		v.Breached = append(v.Breached, "maxAdded")
	}
	if b.MaxChangedPct != nil && !withinPct(big.NewRat(int64(v.Changed), 1), big.NewRat(int64(v.BaselineItems), 1), *b.MaxChangedPct) {
		v.Breached = append(v.Breached, "maxChangedPct")
	}
	return v, cost, nil
}

func (e *Evaluator) diffCollection(ctx context.Context, b compiledBudget, cand, base version) *collectionDiff {
	candItems, ccost, err := e.items(ctx, b, cand)
	if err != nil {
		return &collectionDiff{cost: ccost, err: fmt.Errorf("on the candidate: %w", err)}
	}
	baseItems, bcost, err := e.items(ctx, b, base)
	if err != nil {
		return &collectionDiff{cost: ccost + bcost, err: fmt.Errorf("on the baseline: %w", err)}
	}
	removed, added, changed := diffItems(baseItems, candItems)
	return &collectionDiff{
		baseItems: len(baseItems), candItems: len(candItems),
		removed: removed, added: added, changed: changed,
		cost: ccost + bcost,
	}
}

// items evaluates a collection, which must be a list, and the keys of its
// elements; it returns the elements by key. Two elements with the same key
// are an error, since neither could be matched with confidence.
func (e *Evaluator) items(ctx context.Context, b compiledBudget, v version) (map[string]any, uint64, error) {
	val, cost, err := b.collection.Eval(ctx, e.vars(v))
	if err != nil {
		return nil, cost, err
	}
	keysVal, keysCost, err := b.keys.Eval(ctx, map[string]any{celenv.VarItems: val})
	cost += keysCost
	if err != nil {
		return nil, cost, err
	}
	list, keys := val.(traits.Lister), keysVal.(traits.Lister)
	// A list straight from the artifact holds its decoded values, which
	// equalItems compares as they are.
	native, _ := val.Value().([]any)
	n := int(list.Size().(types.Int))
	items := make(map[string]any, n)
	for i := range n {
		key := keys.Get(types.Int(i))
		k, ok, err := celenv.EqualityKey(key)
		switch {
		case err != nil:
			return nil, cost, fmt.Errorf("the key of item %d failed: %w", i, err)
		case !ok:
			return nil, cost, fmt.Errorf("the key of item %d, %s, can't equal anything", i, e.showKey(b, key))
		}
		if _, dup := items[k]; dup {
			return nil, cost, fmt.Errorf("two items have the key %s", e.showKey(b, key))
		}
		if native != nil {
			items[k] = native[i]
		} else {
			items[k] = list.Get(types.Int(i))
		}
	}
	return items, cost, nil
}

// showKey renders a collection key for evidence, or [redacted] if the keys
// come from a field that holds a secret.
func (e *Evaluator) showKey(b compiledBudget, key ref.Val) string {
	if b.secretKeys {
		return redact.Marker
	}
	return celenv.Show(key, e.redact.Field)
}

// diffItems counts the keys only base holds, the keys only cand holds, and
// the keys both hold with items that differ.
func diffItems(base, cand map[string]any) (removed, added, changed int) {
	for k, b := range base {
		c, ok := cand[k]
		switch {
		case !ok:
			removed++
		case !equalItems(b, c):
			changed++
		}
	}
	for k := range cand {
		if _, ok := base[k]; !ok {
			added++
		}
	}
	return removed, added, changed
}

// equalItems reports whether two items are equal, as CEL's == compares
// them. An item is a decoded value or a CEL value. Decoded values hold only
// the types Decode produces, whose numbers are all finite doubles; for
// those, comparing the Go values is the same and much faster. Anything
// else, such as a map that a collection's map() built, uses CEL's own
// comparison.
func equalItems(a, b any) bool {
	if equal, ok := equalDecoded(a, b); ok {
		return equal
	}
	return celValue(a).Equal(celValue(b)) == types.True
}

func celValue(v any) ref.Val {
	if rv, ok := v.(ref.Val); ok {
		return rv
	}
	return types.DefaultTypeAdapter.NativeToValue(v)
}

// equalDecoded compares two values of the types Decode produces. ok is
// false if it meets another type before it can tell the answer.
func equalDecoded(a, b any) (equal, ok bool) {
	if !decoded(a) || !decoded(b) {
		return false, false
	}
	switch a := a.(type) {
	case map[string]any:
		bm, isMap := b.(map[string]any)
		if !isMap || len(a) != len(bm) {
			return false, true
		}
		for k, av := range a {
			bv, has := bm[k]
			if !has {
				return false, true
			}
			if equal, ok := equalDecoded(av, bv); !equal || !ok {
				return equal, ok
			}
		}
		return true, true
	case []any:
		bl, isList := b.([]any)
		if !isList || len(a) != len(bl) {
			return false, true
		}
		for i := range a {
			if equal, ok := equalDecoded(a[i], bl[i]); !equal || !ok {
				return equal, ok
			}
		}
		return true, true
	default:
		// nil, a bool, a float64 or a string, which equals only a value of
		// its own type.
		return a == b, true
	}
}

func decoded(v any) bool {
	switch v.(type) {
	case nil, bool, float64, string, []any, map[string]any:
		return true
	}
	return false
}

// measureEvidence describes a measure's change and the budget, as in
// "120 → 240 (+100%); budget ±20%".
func measureEvidence(b contract.DiffBudget, v *BudgetValues) string {
	var change, limits []string
	if b.MaxChangePct != nil {
		change = append(change, signed(changePct(v.Baseline, v.Candidate))+"%")
		limits = append(limits, "±"+celenv.FormatNumber(*b.MaxChangePct)+"%")
	}
	if b.MaxIncrease != nil || b.MaxDecrease != nil {
		change = append(change, signed(v.Candidate-v.Baseline))
	}
	if b.MaxIncrease != nil {
		limits = append(limits, "+"+celenv.FormatNumber(*b.MaxIncrease))
	}
	if b.MaxDecrease != nil {
		limits = append(limits, "-"+celenv.FormatNumber(*b.MaxDecrease))
	}
	if v.Redacted {
		return fmt.Sprintf("%s → %s; budget %s", redact.Marker, redact.Marker, strings.Join(limits, ", "))
	}
	return fmt.Sprintf("%s → %s (%s); budget %s", celenv.FormatNumber(v.Baseline), celenv.FormatNumber(v.Candidate),
		strings.Join(change, ", "), strings.Join(limits, ", "))
}

// collectionEvidence describes a collection's changes and the budget, as
// in "120 → 125 items: 3 removed, 8 added, 4 changed (3.3%); budget at most
// 5 removed, 10% changed".
func collectionEvidence(b contract.DiffBudget, v *BudgetValues) string {
	changedPct := float64(v.Changed) / math.Max(float64(v.BaselineItems), 1) * 100
	var limits []string
	if b.MaxRemoved != nil {
		limits = append(limits, fmt.Sprintf("%d removed", *b.MaxRemoved))
	}
	if b.MaxAdded != nil {
		limits = append(limits, fmt.Sprintf("%d added", *b.MaxAdded))
	}
	if b.MaxChangedPct != nil {
		limits = append(limits, celenv.FormatNumber(*b.MaxChangedPct)+"% changed")
	}
	return fmt.Sprintf("%d → %d items: %d removed, %d added, %d changed (%s%%); budget at most %s",
		v.BaselineItems, v.CandidateItems, v.Removed, v.Added, v.Changed, strings.TrimPrefix(signed(changedPct), "+"), strings.Join(limits, ", "))
}

// signed formats a change with its sign and at most one decimal: +100,
// -3.3, 0. A change too small for one decimal keeps two significant
// digits, so it doesn't read as no change.
func signed(f float64) string {
	if f == 0 {
		return "0"
	}
	s := strings.TrimSuffix(strconv.FormatFloat(f, 'f', 1, 64), ".0")
	if s == "0" || s == "-0" {
		s = strconv.FormatFloat(f, 'g', 2, 64)
	}
	if f > 0 {
		s = "+" + s
	}
	return s
}
