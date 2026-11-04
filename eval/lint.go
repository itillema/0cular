package eval

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/internal/celenv"
	"github.com/itillema/0cular/internal/suggest"
)

// Severity says whether a finding fails contract lint.
type Severity string

const (
	// SeverityError is a mistake that stops the contract from working as
	// written. contract lint fails on any.
	SeverityError Severity = "error"
	// SeverityWarning is something that's probably a mistake.
	SeverityWarning Severity = "warning"
)

// Finding is one thing Lint found in a contract.
type Finding struct {
	// Problem says where the finding is in the contract file and what it
	// is.
	contract.Problem
	// Severity says whether the finding fails lint.
	Severity Severity
}

// String returns the finding as "line:column: severity: path: message",
// leaving out the parts it doesn't have.
func (f Finding) String() string {
	var b strings.Builder
	if f.Pos.IsValid() {
		b.WriteString(f.Pos.String())
		b.WriteString(": ")
	}
	b.WriteString(string(f.Severity))
	b.WriteString(": ")
	if f.Path != "" {
		b.WriteString(f.Path)
		b.WriteString(": ")
	}
	b.WriteString(f.Message)
	return b.String()
}

// Lint checks a contract that contract.LoadFile loaded, more thoroughly
// than New does. It reports these errors:
//   - what New reports: a schema or an expression that doesn't compile;
//   - a format in the schema that the validator doesn't know, so it checks
//     nothing (ADR-0013);
//   - an expression whose static cost estimate is over the cost limit, so
//     that it could fail on some artifact the contract allows (ADR-0003).
//     Sizes come from the schema's maxItems, maxProperties and maxLength,
//     or else from artifact.maxBytes and artifact.maxNodes;
//   - a measure or collection that reads baseline: a budget evaluates
//     them on each version in turn, as self, so baseline is null there.
//
// It warns about arithmetic that mixes an int with a double when it runs,
// such as self.x + 1, which fails there because numbers from the artifact
// are doubles.
//
// The findings are in file order. Lint returns an error only when it
// fails itself.
func Lint(c *contract.Contract) ([]Finding, error) {
	env, err := newEnvs()
	if err != nil {
		return nil, err
	}
	l := &linter{c: c}
	l.schema()
	for i, inv := range c.Invariants {
		path := fmt.Sprintf("invariants[%d].expr", i)
		program, err := env.rules.Compile(inv.Expr, celenv.Bool)
		if err != nil {
			l.problems(expressionProblems(c, path, err))
			continue
		}
		l.arithmetic(path, program)
		l.cost(path, "it could cost", program, itemsSource{})
	}
	for i, b := range c.DiffBudgets {
		budget, problems := compileBudget(c, i, b, env)
		l.problems(problems)
		path := fmt.Sprintf("diffBudgets[%d]", i)
		if p := budget.measure; p != nil {
			l.readsBaseline(path+".measure", "a measure", p)
			l.arithmetic(path+".measure", p)
			l.cost(path+".measure", "it could cost", p, itemsSource{})
		}
		if p := budget.collection; p != nil {
			l.readsBaseline(path+".collection", "a collection", p)
			l.arithmetic(path+".collection", p)
			l.cost(path+".collection", "it could cost", p, itemsSource{})
		}
		if p := budget.key; p != nil {
			l.arithmetic(path+".key", p)
		}
		if p := budget.keys; p != nil {
			var items itemsSource
			items.path, items.same, items.known = budget.collection.ListSource()
			l.cost(path+".key", "keying every element of the collection could cost", p, items)
		}
	}
	slices.SortStableFunc(l.findings, func(a, b Finding) int {
		return cmp.Or(cmp.Compare(a.Pos.Line, b.Pos.Line), cmp.Compare(a.Pos.Column, b.Pos.Column))
	})
	return l.findings, nil
}

// linter collects the findings for one contract.
type linter struct {
	c        *contract.Contract
	findings []Finding
	// bounds reads sizes from the schema. It's nil without a schema.
	bounds *schemaBounds
	// skipCost is set when the schema doesn't compile, so that estimates
	// don't ignore bounds the author meant to set.
	skipCost bool
}

func (l *linter) add(severity Severity, path, message string) {
	l.findings = append(l.findings, Finding{
		Problem:  contract.Problem{Pos: l.c.Position(path), Path: path, Message: message},
		Severity: severity,
	})
}

func (l *linter) problems(problems []contract.Problem) {
	for _, p := range problems {
		l.findings = append(l.findings, Finding{Problem: p, Severity: SeverityError})
	}
}

// schema compiles the contract's schema, checks its formats, and reads the
// bounds it sets.
func (l *linter) schema() {
	if l.c.Schema == nil {
		return
	}
	if _, err := CompileSchema(l.c); err != nil {
		l.add(SeverityError, "schema.jsonSchema", err.Error())
		l.skipCost = true
		return
	}
	doc, err := decodeSchemaFile(l.c.Schema.Data)
	if err != nil { // CompileSchema decoded it already.
		l.skipCost = true
		return
	}
	l.bounds = newSchemaBounds(doc)
	walkSchemas(doc, "", func(s map[string]any, pointer string) {
		f, ok := s["format"].(string)
		if !ok || slices.Contains(knownFormats, f) {
			return
		}
		message := fmt.Sprintf("the schema's format %q at %s isn't one 0cular checks, so it checks nothing", truncate(f), schemaPlace(pointer))
		if best, ok := suggest.Closest(f, knownFormats); ok {
			message += "; did you mean " + best + "?"
		} else {
			message += "; the formats it checks are " + strings.Join(knownFormats, ", ")
		}
		l.add(SeverityError, "schema.jsonSchema", message)
	})
}

// knownFormats are the formats the JSON Schema validator checks, those of
// jsonschema/v6 v6.0.3. It ignores any other format.
var knownFormats = []string{
	"date", "date-time", "duration", "email", "hostname", "ipv4", "ipv6", "iri", "iri-reference", "json-pointer",
	"period", "regex", "relative-json-pointer", "semver", "time", "uri", "uri-reference", "uri-template", "uuid",
}

// schemaPlace names the place a JSON Pointer in a schema points to.
func schemaPlace(pointer string) string {
	if pointer == "" {
		return "the top level"
	}
	return pointer
}

// arithmetic warns about each operator in an expression that mixes an int
// with a double when it runs.
func (l *linter) arithmetic(path string, p *celenv.Program) {
	for _, problem := range p.MixedArithmetic() {
		l.add(SeverityWarning, path, inExpression(problem))
	}
}

// readsBaseline reports a budget expression that reads baseline.
func (l *linter) readsBaseline(path, what string, p *celenv.Program) {
	if p.Reads(celenv.VarBaseline) {
		l.add(SeverityError, path, what+" is evaluated on the candidate and then on the baseline, each as self, so baseline is always null here; read self instead")
	}
}

// itemsSource is what lint knows of the list a key's items variable holds:
// the list in the artifact it comes from, if known, and whether its
// elements are that list's own.
type itemsSource struct {
	path        []string
	same, known bool
}

// cost reports an expression whose static cost estimate is over the limit.
// subject begins the message, such as "it could cost". items describes the
// collection that a key's items variable holds.
func (l *linter) cost(path, subject string, p *celenv.Program, items itemsSource) {
	if l.skipCost {
		return
	}
	s := &sizer{bounds: l.bounds, fallback: newFallbacks(l.c.Artifact), items: items}
	cost, err := p.EstimateCost(s.size)
	if err != nil {
		l.add(SeverityError, path, "lint can't estimate its cost: "+err.Error())
		return
	}
	if cost > celenv.DefaultCostLimit {
		l.add(SeverityError, path, s.explain(subject, cost, s.culprit(p, cost), l.c.Schema != nil))
	}
}

// culprit returns the unbounded value whose bound would cut the estimate
// the most, preferring one the expression loops over, or nil if there's
// none. It estimates again with each value in turn assumed to be tiny.
func (s *sizer) culprit(p *celenv.Program, cost uint64) *unbounded {
	candidates := slices.Clone(s.unbounded)
	slices.SortStableFunc(candidates, func(a, b unbounded) int {
		switch {
		case a.iterated == b.iterated:
			return 0
		case a.iterated:
			return -1
		}
		return 1
	})
	var best *unbounded
	var bestCut uint64
	for i, u := range candidates {
		trial := &sizer{bounds: s.bounds, fallback: s.fallback, items: s.items, assume: u.path}
		lower, err := p.EstimateCost(trial.size)
		if err != nil {
			continue
		}
		if cut := cost - min(lower, cost); best == nil || cut > bestCut {
			best, bestCut = &candidates[i], cut
		}
	}
	return best
}

// fallbacks bound sizes the schema doesn't.
type fallbacks struct {
	// list bounds a list's or a map's entries: each takes at least 2 bytes
	// of the artifact, and counts against maxNodes.
	list uint64
	// listCap names the cap that sets list.
	listCap string
	// str bounds a string's length: it can't be longer than the artifact.
	str uint64
	// built bounds a value an expression builds, such as a function's
	// result: building costs at least its size, which the cost limit
	// bounds, unless it comes from the artifact, which maxBytes bounds.
	built uint64
}

func newFallbacks(a contract.Artifact) fallbacks {
	bytes := uint64(max(a.MaxBytes, 0))
	f := fallbacks{list: bytes / 2, listCap: "maxBytes", str: bytes, built: max(bytes, celenv.DefaultCostLimit)}
	if nodes := uint64(max(a.MaxNodes, 0)); nodes < f.list {
		f.list, f.listCap = nodes, "maxNodes"
	}
	return f
}

// sizer sizes the values one expression reads, and records those that only
// the fallbacks bound.
type sizer struct {
	bounds   *schemaBounds
	fallback fallbacks
	// items is what's known of the collection that items holds, for a key.
	items     itemsSource
	unbounded []unbounded
	// assume is the path of an unbounded value to size as 1, to see how
	// much bounding it would cut the estimate.
	assume string
}

// unbounded is a value that only the fallbacks bound.
type unbounded struct {
	// path is the value as CEL writes it, such as self.policies[*].tags, or
	// the expression that builds it.
	path     string
	iterated bool
	size     uint64
	// kinds are what the value can be: lists, maps or strings.
	kinds kinds
	// note explains a value that no schema can bound, such as one the
	// expression builds. It's empty for a value from the artifact.
	note string
}

// collectionNote explains a key's collection that lint can't trace to a
// list in the artifact.
const collectionNote = "lint can't bound how many elements the collection has; write it as a list from the artifact, such as self.policies, or as a filter() or map() over one"

// size is a celenv.SizeFunc.
func (s *sizer) size(v celenv.Value) uint64 {
	path := v.Path
	if len(path) > 0 && path[0] == celenv.VarItems {
		switch {
		case !s.items.known:
			return s.unbound(unbounded{path: "the collection", iterated: v.Iterated, size: s.fallback.built, note: collectionNote})
		case len(path) == 1 || s.items.same:
			path = slices.Concat(s.items.path, path[1:])
		default:
			return s.fallback.built // a value that map computed
		}
	}
	switch {
	case len(path) == 0:
		if strings.HasPrefix(v.Source, "@") { // a macro's internal value
			return s.fallback.built
		}
		note := fmt.Sprintf("it %s %s, which it builds, and lint can't bound that", verb(v.Iterated), v.Source)
		return s.unbound(unbounded{path: v.Source, iterated: v.Iterated, size: s.fallback.built, note: note})
	case path[0] == celenv.VarMeta && len(path) > 1 && path[1] == "key":
		note := "it reads meta.key, the artifact's key or file path, whose length lint can't bound"
		return s.unbound(unbounded{path: "meta.key", size: s.fallback.built, note: note})
	case path[0] == celenv.VarMeta:
		return metaSize(path[1:])
	case path[0] != celenv.VarSelf && path[0] != celenv.VarBaseline:
		return s.fallback.built
	}
	var set schemaSet
	if s.bounds != nil {
		set = s.bounds.expand(s.bounds.root)
		for _, step := range path[1:] {
			set = s.bounds.step(set, step)
		}
	}
	size, bounded, k := sizeOf(set, v.Iterated, s.fallback)
	if !bounded {
		return s.unbound(unbounded{path: displayPath(path), iterated: v.Iterated, size: size, kinds: k})
	}
	return size
}

// unbound records u and returns its size, or 1 if the sizer assumes it's
// tiny.
func (s *sizer) unbound(u unbounded) uint64 {
	if u.path == s.assume {
		return 1
	}
	s.record(u)
	return u.size
}

// record adds u, unless a value of the same path is already there; a value
// that's looped over replaces one that isn't.
func (s *sizer) record(u unbounded) {
	for i, other := range s.unbounded {
		if other.path == u.path {
			if u.iterated && !other.iterated {
				s.unbounded[i] = u
			}
			return
		}
	}
	s.unbounded = append(s.unbounded, u)
}

// metaSize sizes meta and its fields, other than key.
func metaSize(fields []string) uint64 {
	if len(fields) == 0 {
		return 4 // meta's fields
	}
	switch fields[0] {
	case "sha256":
		return 64
	case "format":
		return 4
	}
	return 0 // bytes, an int
}

// verb says how an expression uses a value.
func verb(iterated bool) string {
	if iterated {
		return "loops over"
	}
	return "reads"
}

// displayPath writes a value's path as CEL would, with [*] for any element:
// self.policies[*].limit.
func displayPath(path []string) string {
	var b strings.Builder
	for i, p := range path {
		switch {
		case strings.HasPrefix(p, "@"):
			b.WriteString("[*]")
		case i > 0:
			b.WriteString(".")
			fallthrough
		default:
			b.WriteString(p)
		}
	}
	return b.String()
}

// explain describes why an expression's estimate is over the limit, and
// how to bring it under: by bounding u, or, if u is nil, by tightening the
// schema's bounds.
func (s *sizer) explain(subject string, cost uint64, u *unbounded, hasSchema bool) string {
	var b strings.Builder
	if cost == math.MaxUint64 {
		fmt.Fprintf(&b, "%s more than the cost limit of %s on an artifact this contract allows", subject, thousands(celenv.DefaultCostLimit))
	} else {
		fmt.Fprintf(&b, "%s up to %s on an artifact this contract allows, over the cost limit of %s", subject, thousands(cost), thousands(celenv.DefaultCostLimit))
	}
	switch {
	case u == nil:
		b.WriteString(", even within the bounds the schema sets; lower them, or simplify the expression")
	case u.note != "":
		b.WriteString(": " + u.note)
	default:
		where := "the schema"
		if !hasSchema {
			where = "a JSON Schema (schema.jsonSchema)"
		}
		capName := "maxBytes"
		if u.kinds&stringKind == 0 && s.fallback.listCap == "maxNodes" {
			capName = "maxNodes"
		}
		fmt.Fprintf(&b, ": it %s %s, which could hold up to %s %s; bound it with %s in %s, or lower artifact.%s",
			verb(u.iterated), u.path, thousands(u.size), u.kinds.noun(), u.kinds.keyword(), where, capName)
	}
	return b.String()
}

// thousands formats n with commas: 1,000,000.
func thousands(n uint64) string {
	s := strconv.FormatUint(n, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// kinds is a set of the JSON types a value can have, of those that have a
// size.
type kinds uint8

const (
	arrayKind kinds = 1 << iota
	objectKind
	stringKind
	// otherKinds are the JSON types without a size: booleans, numbers and
	// null.
	otherKinds

	listKinds = arrayKind | objectKind
	anyKind   = arrayKind | objectKind | stringKind | otherKinds
)

// keyword names the schema keywords that bound a value of these kinds.
func (k kinds) keyword() string {
	var words []string
	if k&arrayKind != 0 {
		words = append(words, "maxItems")
	}
	if k&objectKind != 0 {
		words = append(words, "maxProperties")
	}
	if k&stringKind != 0 {
		words = append(words, "maxLength")
	}
	return joinOr(words)
}

// noun names what a value of these kinds holds.
func (k kinds) noun() string {
	switch {
	case k&stringKind == 0:
		return "items"
	case k&listKinds == 0:
		return "characters"
	}
	return "items or characters"
}

func joinOr(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + " or " + words[len(words)-1]
}

// sizeOf returns the largest size of a value that set applies to, whether
// the schema bounds it rather than the fallbacks, and the kinds the value
// can have. CEL loops over lists and maps only, so a value it loops over is
// one of those.
func sizeOf(set schemaSet, iterated bool, f fallbacks) (size uint64, bounded bool, k kinds) {
	k = allowedKinds(set)
	if iterated {
		k &= listKinds
	}
	bounded = true
	for _, c := range []struct {
		kind     kinds
		keyword  string
		fallback uint64
	}{
		{arrayKind, "maxItems", f.list},
		{objectKind, "maxProperties", f.list},
		{stringKind, "maxLength", f.str},
	} {
		if k&c.kind == 0 {
			continue
		}
		limit, ok := set.lowest(c.keyword)
		if !ok {
			bounded = false
		}
		if !ok || limit > c.fallback {
			limit = c.fallback
		}
		size = max(size, limit)
	}
	return size, bounded, k &^ otherKinds
}

// schemaSet is subschemas of a JSON Schema that all apply to one value, as
// far as lint can tell. An empty set says nothing about the value.
type schemaSet []map[string]any

// lowest returns the lowest value of a keyword, such as maxItems, in the
// set.
func (set schemaSet) lowest(keyword string) (uint64, bool) {
	var lowest uint64
	found := false
	for _, s := range set {
		f, ok := s[keyword].(float64)
		if !ok || f < 0 || f > math.MaxUint64/2 {
			continue
		}
		if v := uint64(f); !found || v < lowest {
			lowest, found = v, true
		}
	}
	return lowest, found
}

// allowedKinds returns the kinds that every type keyword in the set allows.
func allowedKinds(set schemaSet) kinds {
	k := anyKind
	for _, s := range set {
		var types []any
		switch t := s["type"].(type) {
		case string:
			types = []any{t}
		case []any:
			types = t
		default:
			continue
		}
		var allowed kinds
		for _, t := range types {
			switch t {
			case "array":
				allowed |= arrayKind
			case "object":
				allowed |= objectKind
			case "string":
				allowed |= stringKind
			default:
				allowed |= otherKinds
			}
		}
		k &= allowed
	}
	return k
}

// maxApplicable bounds how many subschemas lint gathers for one value.
const maxApplicable = 256

// schemaBounds reads the size bounds a JSON Schema sets. It follows $ref
// within the schema file, allOf, properties, additionalProperties, items
// and propertyNames, and nothing that applies only sometimes, such as
// anyOf: everything it gathers applies to the value, so every bound it
// reads holds.
type schemaBounds struct {
	root any
	// followRefs is false when the schema has a nested $id, which would
	// change what its references mean.
	followRefs bool
}

func newSchemaBounds(doc any) *schemaBounds {
	b := &schemaBounds{root: doc, followRefs: true}
	walkSchemas(doc, "", func(s map[string]any, pointer string) {
		_, id := s["$id"]
		_, oldID := s["id"].(string)
		if pointer != "" && (id || oldID) {
			b.followRefs = false
		}
	})
	return b
}

// expand returns the schema v with the schemas it applies through $ref and
// allOf.
func (b *schemaBounds) expand(v any) schemaSet {
	var set schemaSet
	followed := map[string]bool{}
	var add func(v any)
	add = func(v any) {
		s, ok := v.(map[string]any)
		if !ok || len(set) >= maxApplicable {
			return // true, false, or not a schema: lint assumes nothing.
		}
		set = append(set, s)
		if ref, ok := s["$ref"].(string); ok && !followed[ref] {
			followed[ref] = true
			if target, ok := b.resolve(ref); ok {
				add(target)
			}
		}
		if all, ok := s["allOf"].([]any); ok {
			for _, sub := range all {
				add(sub)
			}
		}
	}
	add(v)
	return set
}

// resolve returns what a reference within the schema file points to.
func (b *schemaBounds) resolve(ref string) (any, bool) {
	fragment, ok := strings.CutPrefix(ref, "#")
	if !ok || !b.followRefs {
		return nil, false
	}
	fragment, err := url.PathUnescape(fragment)
	if err != nil {
		return nil, false
	}
	if fragment == "" {
		return b.root, true
	}
	if !strings.HasPrefix(fragment, "/") {
		return nil, false // an anchor
	}
	v := b.root
	for _, token := range strings.Split(fragment[1:], "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		switch node := v.(type) {
		case map[string]any:
			if v, ok = node[token]; !ok {
				return nil, false
			}
		case []any:
			i, err := strconv.Atoi(token)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			v = node[i]
		default:
			return nil, false
		}
	}
	return v, true
}

// step returns the subschemas that apply to the value at step from a value
// that set applies to. step is a field name, or one of cel-go's "@items",
// "@keys" and "@values": an element of a list, or a key or a value of a
// map, depending on what the schema says the value is.
func (b *schemaBounds) step(set schemaSet, step string) schemaSet {
	if !strings.HasPrefix(step, "@") {
		return b.gather(set, func(s map[string]any) (any, bool) {
			if props, ok := s["properties"].(map[string]any); ok {
				if sub, ok := props[step]; ok {
					return sub, true
				}
			}
			if _, ok := s["patternProperties"]; ok {
				return nil, false
			}
			sub, ok := s["additionalProperties"]
			return sub, ok
		})
	}
	switch allowedKinds(set) &^ otherKinds {
	case arrayKind:
		return b.gather(set, func(s map[string]any) (any, bool) {
			if _, tuple := s["prefixItems"]; tuple {
				return nil, false
			}
			sub, ok := s["items"].(map[string]any)
			return sub, ok
		})
	case objectKind:
		if step == "@keys" {
			return b.gather(set, func(s map[string]any) (any, bool) {
				sub, ok := s["propertyNames"]
				return sub, ok
			})
		}
		return b.gather(set, func(s map[string]any) (any, bool) {
			_, props := s["properties"]
			_, patterns := s["patternProperties"]
			sub, ok := s["additionalProperties"]
			return sub, ok && !props && !patterns
		})
	}
	return nil
}

// gather expands the subschema that sub picks from each schema in set.
func (b *schemaBounds) gather(set schemaSet, sub func(map[string]any) (any, bool)) schemaSet {
	var out schemaSet
	for _, s := range set {
		if v, ok := sub(s); ok && len(out) < maxApplicable {
			out = append(out, b.expand(v)...)
		}
	}
	return out
}

// Keywords whose values are schemas, in Draft 2020-12 and the drafts
// before it: one schema, a list of them, or an object of them.
var (
	schemaKeywords     = []string{"additionalItems", "additionalProperties", "contains", "contentSchema", "else", "if", "items", "not", "propertyNames", "then", "unevaluatedItems", "unevaluatedProperties"}
	schemaListKeywords = []string{"allOf", "anyOf", "items", "oneOf", "prefixItems"}
	schemaMapKeywords  = []string{"$defs", "definitions", "dependencies", "dependentSchemas", "patternProperties", "properties"}
)

// walkSchemas calls visit for every schema object in a JSON Schema
// document, with its JSON Pointer, in a fixed order.
func walkSchemas(v any, pointer string, visit func(s map[string]any, pointer string)) {
	s, ok := v.(map[string]any)
	if !ok {
		return
	}
	visit(s, pointer)
	for _, k := range schemaKeywords {
		if sub, ok := s[k].(map[string]any); ok {
			walkSchemas(sub, pointer+"/"+escapePointer(k), visit)
		}
	}
	for _, k := range schemaListKeywords {
		if list, ok := s[k].([]any); ok {
			for i, sub := range list {
				walkSchemas(sub, pointer+"/"+escapePointer(k)+"/"+strconv.Itoa(i), visit)
			}
		}
	}
	for _, k := range schemaMapKeywords {
		if m, ok := s[k].(map[string]any); ok {
			for _, name := range slices.Sorted(maps.Keys(m)) {
				walkSchemas(m[name], pointer+"/"+escapePointer(k)+"/"+escapePointer(name), visit)
			}
		}
	}
}

// escapePointer escapes a JSON Pointer token.
func escapePointer(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}
