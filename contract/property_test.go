package contract

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"github.com/itillema/0cular/internal/redact"
	"github.com/itillema/0cular/internal/yamlx"
)

// TestParseRoundTrip writes random valid contracts as YAML, with values in
// every scalar style and keys in random order, and checks that Parse reads
// back exactly what was written.
func TestParseRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		want, src := genContract(t)
		got, err := Parse([]byte(src))
		if err != nil {
			t.Fatalf("Parse() error:\n%v\nsource:\n%s", err, src)
		}
		opts := cmp.Options{ignorePos, cmpopts.IgnoreFields(Contract{}, "Digest"), cmpopts.EquateEmpty()}
		if diff := cmp.Diff(want, got, opts); diff != "" {
			t.Fatalf("Parse() mismatch (-want +got):\n%s\nsource:\n%s", diff, src)
		}
		checkValid(t, got)
		if err := validateSchema(t, src); err != nil {
			t.Fatalf("the JSON Schema rejects a valid contract: %v\nsource:\n%s", err, src)
		}
	})
}

func FuzzParse(f *testing.F) {
	files, err := filepath.Glob("../replay/*/contract.yaml")
	if err != nil {
		f.Fatal(err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	for _, seed := range []string{
		header + "diffBudgets:\n  - {name: b, collection: self.x, key: item.id, maxRemoved: 1, overridable: false}\n",
		header + "invariants:\n  - <<: *a\n",
		header + "schema: {jsonSchema: !!str s.json}\nonFailure: {noLastKnownGood: open, parseError: stale}\n",
		"apiVersion: 0cular/v1alpha1\nkind: Contract\nmetadata: &m {name: x}\nartifact: *m\n",
		"apiVersion: 0cular/v1\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		c, err := Parse(data)
		if err == nil {
			checkValid(t, c)
			if err := validateSchema(t, string(data)); err != nil {
				t.Fatalf("the JSON Schema rejects a contract Parse accepts: %v", err)
			}
			return
		}
		var cerr *Error
		if c != nil || !errors.As(err, &cerr) || len(cerr.Problems) == 0 {
			t.Fatalf("Parse() = %v, %v; want only a *Error with problems", c, err)
		}
		for _, p := range cerr.Problems {
			if p.Message == "" {
				t.Fatalf("a problem has no message: %+v", p)
			}
		}
		if _, again := Parse(data); again == nil || again.Error() != err.Error() {
			t.Fatalf("Parse() isn't deterministic:\n%v\nthen:\n%v", err, again)
		}
	})
}

// fataler is the part of testing.TB that *rapid.T also has.
type fataler interface {
	Helper()
	Fatal(args ...any)
	Fatalf(format string, args ...any)
}

// checkValid checks what every contract Parse returns must satisfy.
func checkValid(t fataler, c *Contract) {
	t.Helper()
	validName := func(name string) bool { return len(name) <= maxNameLen && namePattern.MatchString(name) }
	if !validName(c.Metadata.Name) {
		t.Fatalf("invalid metadata.name %q", c.Metadata.Name)
	}
	a := c.Artifact
	if a.Format != FormatJSON && a.Format != FormatYAML || a.MaxBytes < 1 || a.MaxDepth < 1 || a.MaxNodes < 1 {
		t.Fatalf("invalid artifact %+v", a)
	}
	if c.Schema != nil && strings.TrimSpace(c.Schema.JSONSchema) == "" {
		t.Fatal("empty schema path")
	}
	names := map[string]bool{}
	checkName := func(name string) {
		if !validName(name) || names[name] {
			t.Fatalf("invalid or repeated check name %q", name)
		}
		names[name] = true
	}
	for _, inv := range c.Invariants {
		checkName(inv.Name)
		if strings.TrimSpace(inv.Expr) == "" {
			t.Fatalf("invariant %s has no expression", inv.Name)
		}
	}
	finite := func(v *float64) bool { return v == nil || *v >= 0 && !math.IsInf(*v, 0) && !math.IsNaN(*v) }
	for _, b := range c.DiffBudgets {
		checkName(b.Name)
		if !finite(b.MaxChangePct) || !finite(b.MaxIncrease) || !finite(b.MaxDecrease) || !finite(b.MaxChangedPct) ||
			b.MaxRemoved != nil && *b.MaxRemoved < 0 || b.MaxAdded != nil && *b.MaxAdded < 0 {
			t.Fatalf("budget %s has an invalid limit", b.Name)
		}
		measureLimit := b.MaxChangePct != nil || b.MaxIncrease != nil || b.MaxDecrease != nil
		collectionLimit := b.MaxRemoved != nil || b.MaxAdded != nil || b.MaxChangedPct != nil
		if b.IsCollection() {
			if b.Measure != "" || b.Key == "" || measureLimit || !collectionLimit {
				t.Fatalf("invalid collection budget %+v", b)
			}
		} else if b.Measure == "" || b.Key != "" || collectionLimit || !measureLimit {
			t.Fatalf("invalid measure budget %+v", b)
		}
	}
	if p := c.OnFailure.NoLastKnownGood; p != PolicyClosed && p != PolicyOpen {
		t.Fatalf("invalid noLastKnownGood %q", p)
	}
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(c.Digest) {
		t.Fatalf("invalid digest %q", c.Digest)
	}
}

var (
	nameGen = rapid.StringMatching(`[a-z0-9]([-a-z0-9]{0,20}[a-z0-9])?`)
	exprGen = rapid.OneOf(
		rapid.StringMatching(`[a-z_(][a-z0-9_ .()<>=!&|]{0,30}[a-z0-9_)]`),
		rapid.String(),
	).Filter(func(s string) bool { return strings.TrimSpace(s) != "" })
	// safePlain matches strings that can be written as plain scalars.
	safePlain = regexp.MustCompile(`^[A-Za-z0-9_(]([A-Za-z0-9_ .()<>=!&|+*/-]*[A-Za-z0-9_)])?$`)
)

// entry is a key and its value in the YAML a test writes. The value is a
// scalar's YAML text, a mapping, or a list of mappings.
type entry struct {
	key     string
	scalar  string
	mapping []entry
	list    [][]entry
	isList  bool
}

func genContract(t *rapid.T) (*Contract, string) {
	c := &Contract{
		Metadata:  Metadata{Name: nameGen.Draw(t, "name")},
		Artifact:  Artifact{Format: rapid.SampledFrom([]Format{FormatJSON, FormatYAML}).Draw(t, "format"), MaxDepth: DefaultMaxDepth, MaxNodes: DefaultMaxNodes},
		OnFailure: OnFailure{NoLastKnownGood: PolicyClosed},
	}
	artifact := []entry{
		{key: "format", scalar: yamlString(t, string(c.Artifact.Format), "format")},
		{key: "maxBytes", scalar: genSize(t, &c.Artifact.MaxBytes)},
	}
	if rapid.Bool().Draw(t, "set maxDepth") {
		c.Artifact.MaxDepth = rapid.IntRange(1, MaxDepthCeiling).Draw(t, "maxDepth")
		artifact = append(artifact, entry{key: "maxDepth", scalar: strconv.Itoa(c.Artifact.MaxDepth)})
	}
	if rapid.Bool().Draw(t, "set maxNodes") {
		c.Artifact.MaxNodes = rapid.IntRange(1, math.MaxInt32).Draw(t, "maxNodes")
		artifact = append(artifact, entry{key: "maxNodes", scalar: strconv.Itoa(c.Artifact.MaxNodes)})
	}
	top := []entry{
		{key: "apiVersion", scalar: yamlString(t, APIVersion, "apiVersion")},
		{key: "kind", scalar: yamlString(t, Kind, "kind")},
		{key: "metadata", mapping: []entry{{key: "name", scalar: yamlString(t, c.Metadata.Name, "name")}}},
		{key: "artifact", mapping: shuffle(t, artifact, "artifact")},
	}
	if rapid.Bool().Draw(t, "set schema") {
		c.Schema = &Schema{JSONSchema: rapid.StringMatching(`([a-z0-9_.-]{1,8}/){0,3}[a-z0-9_-]{1,8}\.json`).Draw(t, "jsonSchema")}
		top = append(top, entry{key: "schema", mapping: []entry{{key: "jsonSchema", scalar: yamlString(t, c.Schema.JSONSchema, "jsonSchema")}}})
	}

	invariants := rapid.IntRange(0, 3).Draw(t, "invariants")
	budgets := rapid.IntRange(0, 3).Draw(t, "budgets")
	names := rapid.SliceOfNDistinct(nameGen, invariants+budgets, invariants+budgets, rapid.ID[string]).Draw(t, "check names")
	if invariants > 0 || rapid.Bool().Draw(t, "write empty invariants") {
		var items [][]entry
		for i := range invariants {
			inv := Invariant{Name: names[i], Expr: exprGen.Draw(t, "expr")}
			item := []entry{
				{key: "name", scalar: yamlString(t, inv.Name, "invariant name")},
				{key: "expr", scalar: yamlString(t, inv.Expr, "expr")},
			}
			if rapid.Bool().Draw(t, "set message") {
				inv.Message = rapid.String().Draw(t, "message")
				item = append(item, entry{key: "message", scalar: yamlString(t, inv.Message, "message")})
			}
			c.Invariants = append(c.Invariants, inv)
			items = append(items, shuffle(t, item, "invariant"))
		}
		top = append(top, entry{key: "invariants", list: items, isList: true})
	}
	if budgets > 0 {
		var items [][]entry
		for i := range budgets {
			b, item := genBudget(t, names[invariants+i])
			c.DiffBudgets = append(c.DiffBudgets, b)
			items = append(items, shuffle(t, item, "budget"))
		}
		top = append(top, entry{key: "diffBudgets", list: items, isList: true})
	}
	if rapid.Bool().Draw(t, "set onFailure") {
		var onFailure []entry
		if rapid.Bool().Draw(t, "set noLastKnownGood") {
			c.OnFailure.NoLastKnownGood = rapid.SampledFrom([]Policy{PolicyClosed, PolicyOpen}).Draw(t, "noLastKnownGood")
			onFailure = append(onFailure, entry{key: "noLastKnownGood", scalar: yamlString(t, string(c.OnFailure.NoLastKnownGood), "policy")})
		}
		if rapid.Bool().Draw(t, "set trustLastKnownGood") {
			c.OnFailure.TrustLastKnownGood = rapid.Bool().Draw(t, "trustLastKnownGood")
			onFailure = append(onFailure, entry{key: "trustLastKnownGood", scalar: yamlBool(t, c.OnFailure.TrustLastKnownGood)})
		}
		for _, key := range []string{"parseError", "invariantViolation", "fetchError"} {
			if rapid.Bool().Draw(t, "set "+key) {
				onFailure = append(onFailure, entry{key: key, scalar: yamlString(t, "stale", key)})
			}
		}
		if len(onFailure) > 0 {
			top = append(top, entry{key: "onFailure", mapping: shuffle(t, onFailure, "onFailure")})
		}
	}
	if rapid.Bool().Draw(t, "set report") {
		patterns := rapid.SliceOfNDistinct(rapid.StringMatching(`[A-Za-z][A-Za-z0-9_-]{0,10}`), 0, 4, redact.Normalize).Draw(t, "redact")
		quoted := make([]string, len(patterns))
		for i, p := range patterns {
			quoted[i] = strconv.Quote(p)
		}
		c.Report.Redact = patterns
		top = append(top, entry{key: "report", mapping: []entry{{key: "redact", scalar: "[" + strings.Join(quoted, ", ") + "]"}}})
	}

	var b strings.Builder
	writeMapping(&b, "", shuffle(t, top, "top"))
	return c, b.String()
}

func genBudget(t *rapid.T, name string) (DiffBudget, []entry) {
	b := DiffBudget{Name: name, Overridable: true}
	item := []entry{{key: "name", scalar: yamlString(t, name, "budget name")}}
	limit := func(key string, dst **float64) {
		v := rapid.Float64Range(0, 1e9).Draw(t, key) + 0
		*dst = &v
		item = append(item, entry{key: key, scalar: strconv.FormatFloat(v, 'g', -1, 64)})
	}
	count := func(key string, dst **int64) {
		v := rapid.Int64Range(0, math.MaxInt64).Draw(t, key)
		*dst = &v
		item = append(item, entry{key: key, scalar: yamlInt(t, v)})
	}
	// Each bit of limits sets one limit; at least one is set.
	limits := rapid.IntRange(1, 7).Draw(t, "limits")
	if rapid.Bool().Draw(t, "collection") {
		b.Collection = exprGen.Draw(t, "collection")
		b.Key = exprGen.Draw(t, "key")
		item = append(item,
			entry{key: "collection", scalar: yamlString(t, b.Collection, "collection")},
			entry{key: "key", scalar: yamlString(t, b.Key, "key")})
		if limits&1 != 0 {
			count("maxRemoved", &b.MaxRemoved)
		}
		if limits&2 != 0 {
			count("maxAdded", &b.MaxAdded)
		}
		if limits&4 != 0 {
			limit("maxChangedPct", &b.MaxChangedPct)
		}
	} else {
		b.Measure = exprGen.Draw(t, "measure")
		item = append(item, entry{key: "measure", scalar: yamlString(t, b.Measure, "measure")})
		if limits&1 != 0 {
			limit("maxChangePct", &b.MaxChangePct)
		}
		if limits&2 != 0 {
			limit("maxIncrease", &b.MaxIncrease)
		}
		if limits&4 != 0 {
			limit("maxDecrease", &b.MaxDecrease)
		}
	}
	if rapid.Bool().Draw(t, "set overridable") {
		b.Overridable = rapid.Bool().Draw(t, "overridable")
		item = append(item, entry{key: "overridable", scalar: yamlBool(t, b.Overridable)})
	}
	return b, item
}

// genSize draws a size and returns it in one of the forms a contract may
// use: a plain or quoted number of bytes, or a number with a unit.
func genSize(t *rapid.T, dst *int64) string {
	unit := rapid.SampledFrom([]string{"", "B", "KiB", "MiB", "GiB"}).Draw(t, "unit")
	n := rapid.Int64Range(1, math.MaxInt64/sizeUnits[unit]).Draw(t, "size")
	*dst = n * sizeUnits[unit]
	text := strconv.FormatInt(n, 10) + unit
	if unit == "" && rapid.Bool().Draw(t, "plain size") {
		return text
	}
	return yamlString(t, text, "size")
}

// yamlString returns s as a YAML scalar, in a style drawn from those that
// can hold it.
func yamlString(t *rapid.T, s, label string) string {
	styles := []string{"double"}
	if safePlain.MatchString(s) && yamlx.ResolvePlain(s) == yamlx.KindString {
		styles = append(styles, "plain")
	}
	if printableASCII(s, false) {
		styles = append(styles, "single")
	}
	if s != "" && printableASCII(s, true) && !strings.HasSuffix(s, "\n") && !strings.Contains(s, "\n\n") &&
		!regexp.MustCompile(`(^|\n)[ \t]|[ \t](\n|$)`).MatchString(s) {
		styles = append(styles, "literal")
	}
	switch rapid.SampledFrom(styles).Draw(t, label+" style") {
	case "plain":
		return s
	case "single":
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	case "literal":
		return "|-\n" + s
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r >= 0x20 && r < 0x7f:
			b.WriteRune(r)
		default:
			fmt.Fprintf(&b, `\U%08X`, r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func printableASCII(s string, newlines bool) bool {
	for _, r := range s {
		if (r < 0x20 || r >= 0x7f) && (!newlines || r != '\n') {
			return false
		}
	}
	return true
}

func yamlBool(t *rapid.T, v bool) string {
	if v {
		return rapid.SampledFrom([]string{"true", "True", "TRUE"}).Draw(t, "true")
	}
	return rapid.SampledFrom([]string{"false", "False", "FALSE"}).Draw(t, "false")
}

// yamlInt writes a non-negative integer in one of YAML 1.2's notations.
func yamlInt(t *rapid.T, v int64) string {
	switch rapid.IntRange(0, 3).Draw(t, "int notation") {
	case 1:
		return "+" + strconv.FormatInt(v, 10)
	case 2:
		return "0o" + strconv.FormatInt(v, 8)
	case 3:
		return "0x" + strconv.FormatInt(v, 16)
	}
	return strconv.FormatInt(v, 10)
}

func shuffle(t *rapid.T, entries []entry, label string) []entry {
	return rapid.Permutation(entries).Draw(t, label+" order")
}

// writeMapping writes entries as a block mapping at indent.
func writeMapping(b *strings.Builder, indent string, entries []entry) {
	for _, e := range entries {
		b.WriteString(indent + e.key + ":")
		switch {
		case e.mapping != nil:
			b.WriteString("\n")
			writeMapping(b, indent+"  ", e.mapping)
		case e.isList && len(e.list) == 0:
			b.WriteString(" []\n")
		case e.isList:
			b.WriteString("\n")
			for _, item := range e.list {
				var ib strings.Builder
				writeMapping(&ib, indent+"    ", item)
				b.WriteString(indent + "  - " + strings.TrimPrefix(ib.String(), indent+"    "))
			}
		default:
			if lines, ok := strings.CutPrefix(e.scalar, "|-\n"); ok {
				b.WriteString(" |-\n")
				for _, line := range strings.Split(lines, "\n") {
					b.WriteString(indent + "  " + line + "\n")
				}
			} else {
				b.WriteString(" " + e.scalar + "\n")
			}
		}
	}
}
