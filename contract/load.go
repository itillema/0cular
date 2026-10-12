package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/itillema/0cular/internal/yamlx"
)

// The largest files LoadFile reads.
const (
	// MaxFileBytes is the largest contract file.
	MaxFileBytes = 1 << 20
	// MaxSchemaBytes is the largest JSON Schema file a contract can name.
	MaxSchemaBytes = 4 << 20
)

// LoadFile reads, parses and validates the contract file at file, and reads
// the JSON Schema file it names into Schema.Data. An invalid contract, or a
// schema file that can't be read, returns a *Error that lists every problem
// with its line and column.
func LoadFile(file string) (*Contract, error) {
	data, err := readLimited(file, MaxFileBytes)
	if errors.Is(err, errTooLarge) {
		return nil, &Error{File: file, Problems: []Problem{{Message: "the file is larger than 1 MiB, the limit for a contract"}}}
	}
	if err != nil {
		return nil, fmt.Errorf("read contract: %w", err)
	}
	c, err := parse(file, data)
	if err != nil {
		return nil, err
	}
	if c.Schema != nil {
		path := filepath.Join(filepath.Dir(file), filepath.FromSlash(c.Schema.JSONSchema))
		data, err := readLimited(path, MaxSchemaBytes)
		if err != nil {
			message := fmt.Sprintf("can't read the schema file: %v", err)
			if errors.Is(err, errTooLarge) {
				message = "the schema file is larger than 4 MiB, the limit for a schema"
			}
			return nil, &Error{File: file, Problems: []Problem{{Pos: c.Position("schema.jsonSchema"), Path: "schema.jsonSchema", Message: message}}}
		}
		c.Schema.Data = data
	}
	return c, nil
}

var errTooLarge = errors.New("the file is too large")

// readLimited reads the file at path, refusing one larger than limit bytes.
func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errTooLarge
	}
	return data, nil
}

// Parse parses and validates a contract from the bytes of a contract file.
// An invalid contract returns a *Error that lists every problem with its
// line and column.
func Parse(data []byte) (*Contract, error) {
	return parse("", data)
}

func parse(file string, data []byte) (*Contract, error) {
	root, err := yamlx.ParseDocument(data)
	if err != nil {
		return nil, &Error{File: file, Problems: []Problem{documentProblem(err)}}
	}
	d := &decoder{pos: map[string]Pos{}, checks: map[string]string{}}
	c := d.contract(root)
	if len(d.problems) > 0 {
		sort.SliceStable(d.problems, func(i, j int) bool {
			a, b := d.problems[i].Pos, d.problems[j].Pos
			return a.Line < b.Line || a.Line == b.Line && a.Column < b.Column
		})
		return nil, &Error{File: file, Problems: d.problems}
	}
	sum := sha256.Sum256(data)
	c.Path = file
	c.Digest = "sha256:" + hex.EncodeToString(sum[:])
	c.pos = d.pos
	return c, nil
}

func documentProblem(err error) Problem {
	var syntax *yamlx.SyntaxError
	switch {
	case errors.Is(err, yamlx.ErrEmpty):
		return Problem{Message: "the contract is empty"}
	case errors.Is(err, yamlx.ErrMultipleDocuments):
		return Problem{Message: "the file holds more than one YAML document; a contract file holds exactly one"}
	case errors.As(err, &syntax):
		return Problem{Pos: Pos{Line: syntax.Line}, Message: "invalid YAML: " + syntax.Message}
	default:
		return Problem{Message: err.Error()}
	}
}

// decoder walks a contract's YAML node tree. It decodes values itself,
// rather than through yaml.Unmarshal, so that it can apply the YAML 1.2
// core schema, refuse what a contract mustn't hold, and report every
// problem with its position.
type decoder struct {
	problems []Problem
	// pos records where each value is, by path.
	pos map[string]Pos
	// checks maps each check name to the path of the check that has it.
	checks map[string]string
}

func (d *decoder) errorf(n *yaml.Node, path, format string, args ...any) {
	d.errorAt(posOf(n), path, fmt.Sprintf(format, args...))
}

func (d *decoder) errorAt(pos Pos, path, message string) {
	d.problems = append(d.problems, Problem{Pos: pos, Path: path, Message: message})
}

func posOf(n *yaml.Node) Pos {
	return Pos{Line: n.Line, Column: n.Column}
}

func (d *decoder) contract(root *yaml.Node) *Contract {
	c := &Contract{
		Artifact:  Artifact{MaxDepth: DefaultMaxDepth, MaxNodes: DefaultMaxNodes},
		OnFailure: OnFailure{NoLastKnownGood: PolicyClosed},
	}
	if !d.plain(root, "") {
		return c
	}
	if isNull(root) {
		d.errorAt(Pos{}, "", "the contract is empty")
		return c
	}
	if root.Kind != yaml.MappingNode {
		d.errorf(root, "", "a contract must be a YAML mapping, not %s", describe(root))
		return c
	}
	if !d.header(root) {
		return c
	}
	d.mapping(root, "", root, []field{
		{name: "apiVersion", required: true, decode: func(_, _ *yaml.Node, _ string) {}},
		{name: "kind", required: true, decode: func(_, _ *yaml.Node, _ string) {}},
		{name: "metadata", required: true, decode: func(key, n *yaml.Node, path string) {
			d.mapping(n, path, key, []field{
				{name: "name", required: true, decode: func(_, n *yaml.Node, path string) { c.Metadata.Name, _ = d.name(n, path) }},
			})
		}},
		{name: "artifact", required: true, decode: func(key, n *yaml.Node, path string) { d.artifact(key, n, path, &c.Artifact) }},
		{name: "schema", decode: func(key, n *yaml.Node, path string) { c.Schema = d.schema(key, n, path) }},
		{name: "invariants", decode: func(_, n *yaml.Node, path string) { c.Invariants = d.invariants(n, path) }},
		{name: "diffBudgets", decode: func(_, n *yaml.Node, path string) { c.DiffBudgets = d.diffBudgets(n, path) }},
		{name: "consumerTests", refused: "consumer tests arrive in 0cular v0.3, and this build can't run them; remove them for now"},
		{name: "rollout", refused: "rollouts arrive in 0cular v0.4, and this build can't run them; remove it for now"},
		{name: "onFailure", decode: func(key, n *yaml.Node, path string) { d.onFailure(key, n, path, &c.OnFailure) }},
	})
	return c
}

// header checks apiVersion and kind before anything else. A file that isn't
// a 0cular contract, or is one for another version of the format, gets one
// clear problem instead of one for every field.
func (d *decoder) header(root *yaml.Node) bool {
	ok := true
	if v := lookup(root, "apiVersion"); v == nil {
		d.errorf(root, "", "missing required field apiVersion; this build of 0cular reads %s", APIVersion)
		ok = false
	} else if s, isString := d.plainString(v, "apiVersion"); !isString {
		ok = false
	} else if s != APIVersion {
		if strings.HasPrefix(s, "0cular/") {
			d.errorf(v, "apiVersion", "%s isn't supported; this build of 0cular reads %s", s, APIVersion)
		} else {
			d.errorf(v, "apiVersion", "must be %s; this file isn't a 0cular contract", APIVersion)
		}
		ok = false
	}
	if v := lookup(root, "kind"); v == nil {
		d.errorf(root, "", "missing required field kind; it must be %s", Kind)
		ok = false
	} else if s, isString := d.plainString(v, "kind"); !isString {
		ok = false
	} else if s != Kind {
		d.errorf(v, "kind", "must be %s", Kind)
		ok = false
	}
	return ok
}

// lookup returns the value of the first key called name in mapping m.
func lookup(m *yaml.Node, name string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i]; k.Kind == yaml.ScalarNode && k.Value == name {
			return m.Content[i+1]
		}
	}
	return nil
}

func (d *decoder) plainString(n *yaml.Node, path string) (string, bool) {
	if !d.plain(n, path) {
		return "", false
	}
	return d.str(n, path)
}

// field describes one key a mapping may hold.
type field struct {
	name     string
	required bool
	// decode reads the key's value. It's called only for a value without
	// anchors, aliases or tags, and with a value.
	decode func(key, value *yaml.Node, path string)
	// refused, when set, rejects the key with this message.
	refused string
}

// mapping walks mapping n at path, decoding each key's value with its
// field, in file order. It reports keys that are unknown, duplicated,
// refused or blank, and required keys that are missing; missing keys are
// reported at node at.
func (d *decoder) mapping(n *yaml.Node, path string, at *yaml.Node, fields []field) {
	if n.Kind != yaml.MappingNode {
		d.errorf(n, path, "must be a mapping, not %s", describe(n))
		return
	}
	seen := make(map[string]*yaml.Node, len(fields))
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		name, ok := d.key(key, path)
		if !ok {
			continue
		}
		keyPath := join(path, name)
		if first := seen[name]; first != nil {
			d.errorf(key, keyPath, "duplicate key; it's already set on line %d", first.Line)
			continue
		}
		seen[name] = key
		j := slices.IndexFunc(fields, func(f field) bool { return f.name == name })
		switch {
		case j < 0:
			d.errorf(key, keyPath, "unknown field%s", suggest(name, fields))
		case fields[j].refused != "":
			d.errorf(key, keyPath, "%s", fields[j].refused)
		default:
			d.pos[keyPath] = posOf(value)
			if !d.plain(value, keyPath) {
				continue
			}
			if isNull(value) {
				d.errorf(key, keyPath, "has no value")
				continue
			}
			fields[j].decode(key, value, keyPath)
		}
	}
	for _, f := range fields {
		if f.required && seen[f.name] == nil {
			d.errorf(at, path, "missing required field %s", f.name)
		}
	}
}

// key returns the name of a mapping key.
func (d *decoder) key(key *yaml.Node, path string) (string, bool) {
	if !d.plain(key, path) {
		return "", false
	}
	if key.Kind != yaml.ScalarNode {
		d.errorf(key, path, "keys must be strings, not %s", describe(key))
		return "", false
	}
	if key.Value == "<<" && key.Style == 0 {
		d.errorf(key, path, "merge keys (<<) aren't supported in contract files")
		return "", false
	}
	return key.Value, true
}

// plain reports an anchor, alias or explicit tag on n, none of which a
// contract file may use, and returns whether n is free of them.
func (d *decoder) plain(n *yaml.Node, path string) bool {
	switch {
	case n.Kind == yaml.AliasNode:
		d.errorf(n, path, "aliases (*%s) aren't supported in contract files", n.Value)
	case n.Anchor != "":
		d.errorf(n, path, "anchors (&%s) aren't supported in contract files", n.Anchor)
	case n.Style&yaml.TaggedStyle != 0:
		d.errorf(n, path, "explicit tags (%s) aren't supported in contract files", n.Tag)
	default:
		return true
	}
	return false
}

// list walks sequence n at path, calling item for each element that has a
// value and no anchor, alias or tag.
func (d *decoder) list(n *yaml.Node, path string, item func(n *yaml.Node, path string)) {
	if n.Kind != yaml.SequenceNode {
		d.errorf(n, path, "must be a list, not %s", describe(n))
		return
	}
	for i, e := range n.Content {
		p := fmt.Sprintf("%s[%d]", path, i)
		d.pos[p] = posOf(e)
		switch {
		case !d.plain(e, p):
		case isNull(e):
			d.errorf(e, p, "has no value")
		default:
			item(e, p)
		}
	}
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func isNull(n *yaml.Node) bool {
	k, err := yamlx.ScalarKind(n)
	return err == nil && k == yamlx.KindNull
}

// describe names what node n holds, for messages.
func describe(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a list"
	case yaml.AliasNode:
		return "an alias"
	case yaml.ScalarNode:
		k, err := yamlx.ScalarKind(n)
		switch {
		case err != nil:
			return "a tagged value"
		case k == yamlx.KindNull:
			return "null"
		case k == yamlx.KindBool:
			return "a boolean"
		case k == yamlx.KindInt:
			return "an integer"
		case k == yamlx.KindFloat:
			return "a number"
		default:
			return "a string"
		}
	default:
		return "a document"
	}
}

// scalar returns the kind of scalar n, or reports that n isn't the scalar
// that want describes.
func (d *decoder) scalar(n *yaml.Node, path, want string) (yamlx.Kind, bool) {
	if n.Kind != yaml.ScalarNode {
		d.errorf(n, path, "must be %s, not %s", want, describe(n))
		return 0, false
	}
	k, err := yamlx.ScalarKind(n)
	if err != nil {
		d.errorf(n, path, "%v", err)
		return 0, false
	}
	if k == yamlx.KindNull {
		d.errorf(n, path, "has no value")
		return 0, false
	}
	return k, true
}

func (d *decoder) str(n *yaml.Node, path string) (string, bool) {
	k, ok := d.scalar(n, path, "a string")
	if !ok {
		return "", false
	}
	if k != yamlx.KindString {
		d.errorf(n, path, "must be a string, not %s; quote it if you mean %q", describe(n), n.Value)
		return "", false
	}
	return n.Value, true
}

// text returns a string that isn't empty or only white space.
func (d *decoder) text(n *yaml.Node, path string) (string, bool) {
	s, ok := d.str(n, path)
	if ok && strings.TrimSpace(s) == "" {
		d.errorf(n, path, "must not be empty")
		return "", false
	}
	return s, ok
}

// yaml11Bools are YAML 1.1 booleans, which are plain strings under YAML 1.2.
var yaml11Bools = []string{"y", "Y", "yes", "Yes", "YES", "n", "N", "no", "No", "NO", "on", "On", "ON", "off", "Off", "OFF"}

func (d *decoder) boolean(n *yaml.Node, path string) (bool, bool) {
	k, ok := d.scalar(n, path, "true or false")
	if !ok {
		return false, false
	}
	if k != yamlx.KindBool {
		if n.Style == 0 && slices.Contains(yaml11Bools, n.Value) {
			d.errorf(n, path, "must be true or false; YAML 1.1 forms such as %s aren't booleans in 0cular", n.Value)
		} else {
			d.errorf(n, path, "must be true or false, not %s", describe(n))
		}
		return false, false
	}
	b, err := yamlx.ParseBool(n.Value)
	return b, err == nil
}

// integer returns an integer from lowest to highest.
func (d *decoder) integer(n *yaml.Node, path string, lowest, highest int64) (int64, bool) {
	k, ok := d.scalar(n, path, "an integer")
	if !ok {
		return 0, false
	}
	if k != yamlx.KindInt {
		d.errorf(n, path, "must be an integer, not %s", describe(n))
		return 0, false
	}
	v, err := yamlx.ParseInt(n.Value)
	switch {
	case err != nil && strings.HasPrefix(n.Value, "-"), err == nil && v < lowest:
		d.errorf(n, path, "must be at least %d", lowest)
	case err != nil, v > highest:
		d.errorf(n, path, "must be at most %d", highest)
	default:
		return v, true
	}
	return 0, false
}

// limit returns a finite number that isn't negative.
func (d *decoder) limit(n *yaml.Node, path string) (float64, bool) {
	k, ok := d.scalar(n, path, "a number")
	if !ok {
		return 0, false
	}
	if k != yamlx.KindInt && k != yamlx.KindFloat {
		d.errorf(n, path, "must be a number, not %s", describe(n))
		return 0, false
	}
	v, err := yamlx.ParseFloat(n.Value)
	switch {
	case err != nil || math.IsInf(v, 0) || math.IsNaN(v):
		d.errorf(n, path, "must be a finite number")
	case v < 0:
		d.errorf(n, path, "must not be negative")
	default:
		return v + 0, true // + 0 turns -0 into 0.
	}
	return 0, false
}

// namePattern matches a name: lowercase letters, digits and hyphens,
// starting and ending with a letter or digit, like a DNS label.
var namePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

const maxNameLen = 63

func (d *decoder) name(n *yaml.Node, path string) (string, bool) {
	s, ok := d.str(n, path)
	if !ok {
		return "", false
	}
	if len(s) > maxNameLen || !namePattern.MatchString(s) {
		d.errorf(n, path, "must be 1 to %d lowercase letters, digits and hyphens, starting and ending with a letter or digit, such as bot-features", maxNameLen)
		return "", false
	}
	return s, true
}

// checkName returns the name of a check, which must differ from every other
// check's name.
func (d *decoder) checkName(n *yaml.Node, path string) string {
	name, ok := d.name(n, path)
	if !ok {
		return ""
	}
	check := strings.TrimSuffix(path, ".name")
	if other, taken := d.checks[name]; taken {
		d.errorf(n, path, "%s already has the name %s; each check needs its own name", other, name)
		return name
	}
	d.checks[name] = check
	return name
}

func (d *decoder) artifact(key, n *yaml.Node, path string, a *Artifact) {
	d.mapping(n, path, key, []field{
		{name: "format", required: true, decode: func(_, n *yaml.Node, path string) { a.Format = d.format(n, path) }},
		{name: "maxBytes", required: true, decode: func(_, n *yaml.Node, path string) { a.MaxBytes = d.size(n, path) }},
		{name: "maxDepth", decode: func(_, n *yaml.Node, path string) {
			if v, ok := d.integer(n, path, 1, MaxDepthCeiling); ok {
				a.MaxDepth = int(v)
			}
		}},
		{name: "maxNodes", decode: func(_, n *yaml.Node, path string) {
			if v, ok := d.integer(n, path, 1, math.MaxInt32); ok {
				a.MaxNodes = int(v)
			}
		}},
		{name: "csv", refused: "CSV artifacts aren't supported yet"},
	})
}

func (d *decoder) format(n *yaml.Node, path string) Format {
	s, ok := d.str(n, path)
	switch {
	case !ok:
	case s == string(FormatJSON), s == string(FormatYAML):
		return Format(s)
	case s == "toml", s == "csv", s == "bytes":
		d.errorf(n, path, "the %s format isn't supported yet; this build reads json and yaml", s)
	default:
		d.errorf(n, path, "must be json or yaml")
	}
	return ""
}

func (d *decoder) size(n *yaml.Node, path string) int64 {
	k, ok := d.scalar(n, path, "a size")
	if !ok {
		return 0
	}
	if k != yamlx.KindInt && k != yamlx.KindString {
		d.errorf(n, path, "must be a size such as 5MiB, not %s", describe(n))
		return 0
	}
	v, err := parseSize(n.Value)
	if err != nil {
		d.errorf(n, path, "%v", err)
		return 0
	}
	return v
}

var (
	urlPattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)
	drivePattern = regexp.MustCompile(`^[A-Za-z]:`)
)

func (d *decoder) schema(key, n *yaml.Node, path string) *Schema {
	s := &Schema{}
	d.mapping(n, path, key, []field{
		{name: "jsonSchema", required: true, decode: func(_, n *yaml.Node, path string) { s.JSONSchema = d.relativePath(n, path) }},
	})
	return s
}

// relativePath returns a file path relative to the contract file, written
// with forward slashes so the contract works on every OS.
func (d *decoder) relativePath(n *yaml.Node, path string) string {
	s, ok := d.text(n, path)
	switch {
	case !ok:
	case urlPattern.MatchString(s):
		d.errorf(n, path, "must be a file path, not a URL; 0cular doesn't fetch schemas")
	case strings.ContainsRune(s, '\\'):
		d.errorf(n, path, "must use forward slashes, such as schemas/bot-features.schema.json")
	case strings.HasPrefix(s, "/"), drivePattern.MatchString(s):
		d.errorf(n, path, "must be relative to the contract file, such as schemas/bot-features.schema.json")
	case strings.ContainsRune(s, 0):
		d.errorf(n, path, "must not contain a NUL character")
	default:
		return s
	}
	return ""
}

func (d *decoder) invariants(n *yaml.Node, path string) []Invariant {
	var out []Invariant
	d.list(n, path, func(item *yaml.Node, path string) {
		var inv Invariant
		d.mapping(item, path, item, []field{
			{name: "name", required: true, decode: func(_, n *yaml.Node, path string) { inv.Name = d.checkName(n, path) }},
			{name: "expr", required: true, decode: func(_, n *yaml.Node, path string) { inv.Expr, _ = d.text(n, path) }},
			{name: "message", decode: func(_, n *yaml.Node, path string) { inv.Message, _ = d.str(n, path) }},
		})
		out = append(out, inv)
	})
	return out
}

const absoluteLimit = "budgets bound only change from the baseline; put an absolute limit in an invariant, such as size(self.items) <= 200"

var (
	measureLimits    = []string{"maxChangePct", "maxIncrease", "maxDecrease"}
	collectionLimits = []string{"maxRemoved", "maxAdded", "maxChangedPct"}
)

func (d *decoder) diffBudgets(n *yaml.Node, path string) []DiffBudget {
	var out []DiffBudget
	d.list(n, path, func(item *yaml.Node, path string) {
		b := DiffBudget{Overridable: true}
		expr := func(dst *string) func(_, n *yaml.Node, path string) {
			return func(_, n *yaml.Node, path string) { *dst, _ = d.text(n, path) }
		}
		limit := func(dst **float64) func(_, n *yaml.Node, path string) {
			return func(_, n *yaml.Node, path string) {
				if v, ok := d.limit(n, path); ok {
					*dst = &v
				}
			}
		}
		count := func(dst **int64) func(_, n *yaml.Node, path string) {
			return func(_, n *yaml.Node, path string) {
				if v, ok := d.integer(n, path, 0, math.MaxInt64); ok {
					*dst = &v
				}
			}
		}
		d.mapping(item, path, item, []field{
			{name: "name", required: true, decode: func(_, n *yaml.Node, path string) { b.Name = d.checkName(n, path) }},
			{name: "measure", decode: expr(&b.Measure)},
			{name: "maxChangePct", decode: limit(&b.MaxChangePct)},
			{name: "maxIncrease", decode: limit(&b.MaxIncrease)},
			{name: "maxDecrease", decode: limit(&b.MaxDecrease)},
			{name: "collection", decode: expr(&b.Collection)},
			{name: "key", decode: expr(&b.Key)},
			{name: "maxRemoved", decode: count(&b.MaxRemoved)},
			{name: "maxAdded", decode: count(&b.MaxAdded)},
			{name: "maxChangedPct", decode: limit(&b.MaxChangedPct)},
			{name: "overridable", decode: func(_, n *yaml.Node, path string) { b.Overridable, _ = d.boolean(n, path) }},
			{name: "minValue", refused: absoluteLimit},
			{name: "maxValue", refused: absoluteLimit},
		})
		d.budgetShape(item, path)
		out = append(out, b)
	})
	return out
}

// budgetShape checks that the budget at path is a measure budget or a
// collection budget, with the fields of its kind and at least one limit. It
// goes by which keys are present, so a key with a bad value doesn't also
// count as missing.
func (d *decoder) budgetShape(item *yaml.Node, path string) {
	has := func(name string) bool {
		_, ok := d.pos[path+"."+name]
		return ok
	}
	only := func(names []string, kind string) {
		for _, name := range names {
			if has(name) {
				d.errorAt(d.pos[path+"."+name], path+"."+name, "applies only to "+kind+" budgets")
			}
		}
	}
	switch measure, collection := has("measure"), has("collection"); {
	case measure && collection:
		d.errorf(item, path, "set measure or collection, not both")
	case measure:
		only(append([]string{"key"}, collectionLimits...), "collection")
		if !slices.ContainsFunc(measureLimits, has) {
			d.errorf(item, path, "a measure budget needs a limit: maxChangePct, maxIncrease or maxDecrease")
		}
	case collection:
		only(measureLimits, "measure")
		if !has("key") {
			d.errorf(item, path, "a collection budget needs key, an expression over item that identifies each element, such as item.id")
		}
		if !slices.ContainsFunc(collectionLimits, has) {
			d.errorf(item, path, "a collection budget needs a limit: maxRemoved, maxAdded or maxChangedPct")
		}
	default:
		d.errorf(item, path, "set measure, a value to compare, or collection, a keyed list to compare")
	}
}

func (d *decoder) onFailure(key, n *yaml.Node, path string, o *OnFailure) {
	stale := func(_, n *yaml.Node, path string) {
		s, ok := d.str(n, path)
		switch {
		case !ok, s == "stale":
		case s == string(PolicyOpen), s == string(PolicyClosed):
			d.errorf(n, path, "must be stale: with a last known good, every failure keeps it; open and closed apply only to noLastKnownGood")
		default:
			d.errorf(n, path, "must be stale")
		}
	}
	d.mapping(n, path, key, []field{
		{name: "noLastKnownGood", decode: func(_, n *yaml.Node, path string) {
			s, ok := d.str(n, path)
			switch {
			case !ok:
			case s == string(PolicyClosed), s == string(PolicyOpen):
				o.NoLastKnownGood = Policy(s)
			case s == "stale":
				d.errorf(n, path, "must be closed or open: without a last known good, there's nothing to keep")
			default:
				d.errorf(n, path, "must be closed or open")
			}
		}},
		{name: "trustLastKnownGood", decode: func(_, n *yaml.Node, path string) { o.TrustLastKnownGood, _ = d.boolean(n, path) }},
		{name: "parseError", decode: stale},
		{name: "invariantViolation", decode: stale},
		{name: "fetchError", decode: stale},
	})
}

// suggest returns a hint for an unknown key: the closest field name, or
// the list of fields.
func suggest(name string, fields []field) string {
	var names []string
	for _, f := range fields {
		if f.refused == "" {
			names = append(names, f.name)
		}
	}
	if len(name) <= 64 {
		best, bestDistance := "", 3
		for _, f := range names {
			if dist := distance(strings.ToLower(name), strings.ToLower(f)); dist < bestDistance {
				best, bestDistance = f, dist
			}
		}
		if best != "" {
			return "; did you mean " + best + "?"
		}
	}
	if len(names) == 1 {
		return "; the only field here is " + names[0]
	}
	return "; the fields here are " + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// distance returns the Levenshtein distance between a and b.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
