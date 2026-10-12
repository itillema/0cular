package eval

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/itillema/0cular/internal/yamlx"
)

// yamlDecoder decodes a YAML node tree. Aliases share the value of their
// anchor, so expanding one costs nothing, but each counts its anchor's full
// size against maxNodes and its full height against maxDepth.
type yamlDecoder struct {
	src     []byte
	lines   []int // the byte offset where each line starts
	cursor  struct{ line, col, off int }
	lim     limits
	count   counter
	aliases int
	anchors map[*yaml.Node]*anchored
	path    path
}

// anchored is the decoded value of a node with an anchor.
type anchored struct {
	value  any
	nodes  int64
	height int
	done   bool
}

func decodeYAML(ctx context.Context, data []byte, lim limits) (any, error) {
	// YAML allows a byte order mark, and the parser doesn't count it in
	// columns.
	src := bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if off, r := ambiguousBreak(src); off >= 0 {
		line, col := position(src, off)
		return nil, &DecodeError{
			Err: ErrEncoding, Line: line, Column: col,
			Message: fmt.Sprintf("the artifact holds %U, which YAML 1.1 parsers read as a line break and YAML 1.2 parsers don't; write it as an escape in a double-quoted string", r),
		}
	}
	root, err := yamlx.ParseDocument(src)
	if err != nil {
		return nil, documentError(err)
	}
	d := &yamlDecoder{
		src:     src,
		lines:   lineStarts(src),
		lim:     lim,
		count:   counter{ctx: ctx, limit: lim.maxNodes},
		anchors: map[*yaml.Node]*anchored{},
	}
	v, _, _, err := d.node(root, 0)
	return v, err
}

// ambiguousBreak returns the offset of the first U+0085, U+2028 or U+2029
// in src, and the character, or -1. go.yaml.in/yaml/v3 follows YAML 1.1,
// which reads them as line breaks even inside quoted strings; YAML 1.2
// parsers don't.
func ambiguousBreak(src []byte) (int, rune) {
	first, which := -1, rune(0)
	for _, r := range []rune{0x85, 0x2028, 0x2029} {
		if i := bytes.IndexRune(src, r); i >= 0 && (first < 0 || i < first) {
			first, which = i, r
		}
	}
	return first, which
}

func documentError(err error) error {
	var syntax *yamlx.SyntaxError
	switch {
	case errors.Is(err, yamlx.ErrEmpty):
		return &DecodeError{Err: ErrSyntax, Message: "the artifact is empty"}
	case errors.Is(err, yamlx.ErrMultipleDocuments):
		return &DecodeError{Err: ErrSyntax, Message: "the artifact holds more than one YAML document"}
	case errors.As(err, &syntax):
		return &DecodeError{Err: ErrSyntax, Line: syntax.Line, Message: "invalid YAML: " + syntax.Message}
	default:
		return &DecodeError{Err: ErrSyntax, Message: "invalid YAML: " + err.Error()}
	}
}

// lineStarts returns the byte offset where each line of src starts. Lines
// end at LF, CR LF or CR, as the YAML parser counts them.
func lineStarts(src []byte) []int {
	starts := []int{0}
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\n':
			starts = append(starts, i+1)
		case '\r':
			if i+1 < len(src) && src[i+1] == '\n' {
				i++
			}
			starts = append(starts, i+1)
		}
	}
	return starts
}

// node decodes n, which sits inside depth maps and sequences. It returns
// the value, how many values it holds with aliases expanded, and how many
// levels of maps and sequences it nests.
func (d *yamlDecoder) node(n *yaml.Node, depth int) (any, int64, int, error) {
	if n.Kind == yaml.AliasNode {
		return d.alias(n, depth)
	}
	if n.Style&yaml.TaggedStyle != 0 {
		return nil, 0, 0, d.tagError(n, n.Tag)
	}
	var a *anchored
	if n.Anchor != "" {
		a = &anchored{}
		d.anchors[n] = a
	}
	v, nodes, height, err := d.content(n, depth)
	if err != nil {
		return nil, 0, 0, err
	}
	if a != nil {
		*a = anchored{value: v, nodes: nodes, height: height, done: true}
	}
	return v, nodes, height, nil
}

func (d *yamlDecoder) alias(n *yaml.Node, depth int) (any, int64, int, error) {
	d.aliases++
	if d.aliases > MaxAliases {
		return nil, 0, 0, d.errorAt(ErrTooManyAliases, n, fmt.Sprintf("the artifact uses more than %d YAML aliases", MaxAliases))
	}
	a := d.anchors[n.Alias]
	if a == nil || !a.done {
		return nil, 0, 0, d.errorAt(ErrUnsupported, n, fmt.Sprintf("the alias *%s at %s is inside the value it refers to", n.Value, d.path.where()))
	}
	if depth+a.height > d.lim.maxDepth {
		return nil, 0, 0, d.errorAt(ErrTooDeep, n, fmt.Sprintf("the alias *%s at %s nests values deeper than maxDepth (%d)", n.Value, d.path.where(), d.lim.maxDepth))
	}
	if err := d.count.add(a.nodes); err != nil {
		return nil, 0, 0, d.limitError(err, n)
	}
	return a.value, a.nodes, a.height, nil
}

func (d *yamlDecoder) content(n *yaml.Node, depth int) (any, int64, int, error) {
	if err := d.count.add(1); err != nil {
		return nil, 0, 0, d.limitError(err, n)
	}
	switch n.Kind {
	case yaml.ScalarNode:
		v, err := d.scalar(n)
		return v, 1, 0, err
	case yaml.SequenceNode:
		if depth+1 > d.lim.maxDepth {
			return nil, 0, 0, d.tooDeep(n)
		}
		s := make([]any, 0, len(n.Content))
		nodes, height := int64(1), 1
		for i, e := range n.Content {
			d.path.pushIndex(i)
			v, en, eh, err := d.node(e, depth+1)
			if err != nil {
				return nil, 0, 0, err
			}
			d.path.pop()
			s = append(s, v)
			nodes += en
			height = max(height, eh+1)
		}
		return s, nodes, height, nil
	case yaml.MappingNode:
		if depth+1 > d.lim.maxDepth {
			return nil, 0, 0, d.tooDeep(n)
		}
		m := make(map[string]any, len(n.Content)/2)
		nodes, height := int64(1), 1
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, err := d.key(n.Content[i])
			if err != nil {
				return nil, 0, 0, err
			}
			if _, dup := m[k]; dup {
				return nil, 0, 0, d.duplicateKey(n, i, k)
			}
			d.path.push(k)
			v, vn, vh, err := d.node(n.Content[i+1], depth+1)
			if err != nil {
				return nil, 0, 0, err
			}
			d.path.pop()
			m[k] = v
			nodes += vn
			height = max(height, vh+1)
		}
		return m, nodes, height, nil
	default:
		return nil, 0, 0, d.errorAt(ErrSyntax, n, "invalid YAML: unexpected node")
	}
}

// key returns the string a map key holds. Keys must be strings, written
// plain or quoted.
func (d *yamlDecoder) key(k *yaml.Node) (string, error) {
	where := d.path.where()
	switch {
	case k.Kind == yaml.AliasNode:
		return "", d.errorAt(ErrKey, k, fmt.Sprintf("a key in %s is the alias *%s; keys must be strings", where, k.Value))
	case k.Kind == yaml.SequenceNode:
		return "", d.errorAt(ErrKey, k, fmt.Sprintf("a key in %s is a sequence; keys must be strings", where))
	case k.Kind == yaml.MappingNode:
		return "", d.errorAt(ErrKey, k, fmt.Sprintf("a key in %s is a map; keys must be strings", where))
	case k.Style&yaml.TaggedStyle != 0:
		return "", d.tagError(k, k.Tag)
	case k.Value == "<<" && k.Style == 0:
		return "", d.errorAt(ErrUnsupported, k, fmt.Sprintf("the merge key (<<) in %s isn't supported: YAML 1.1 parsers merge the map it names and YAML 1.2 parsers don't", where))
	case k.Style == 0 && d.lostTag(k):
		return "", d.tagError(k, "!")
	}
	if kind, _ := yamlx.ScalarKind(k); kind != yamlx.KindString {
		return "", d.errorAt(ErrKey, k, fmt.Sprintf("the key %s in %s is %s %s; keys must be strings, so quote it", k.Value, where, article(kind), kind))
	}
	if k.Anchor != "" {
		d.anchors[k] = &anchored{value: k.Value, nodes: 1, done: true}
	}
	return k.Value, nil
}

func article(k yamlx.Kind) string {
	if k == yamlx.KindInt {
		return "an"
	}
	return "a"
}

func (d *yamlDecoder) scalar(n *yaml.Node) (any, error) {
	if n.Style == 0 && d.lostTag(n) {
		return nil, d.tagError(n, "!")
	}
	kind, err := yamlx.ScalarKind(n)
	if err != nil {
		return nil, d.tagError(n, n.Tag)
	}
	switch kind {
	case yamlx.KindNull:
		return nil, nil
	case yamlx.KindBool:
		return yamlx.ParseBool(n.Value)
	case yamlx.KindInt:
		i, err := yamlx.ParseInt(n.Value)
		if err != nil || i > MaxSafeInteger || i < -MaxSafeInteger {
			return nil, quoting(d.errorAt(ErrNumber, n, integerMessage(n.Value, d.path)), n.Value)
		}
		return float64(i), nil
	case yamlx.KindFloat:
		f, err := yamlx.ParseFloat(n.Value)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, quoting(d.errorAt(ErrNumber, n, fmt.Sprintf("the number %s at %s isn't finite", n.Value, d.path.where())), n.Value)
		}
		return f + 0, nil // + 0 turns -0 into 0.
	default:
		return n.Value, nil
	}
}

// lostTag reports whether plain scalar n is written after a tag.
// go.yaml.in/yaml/v3 drops the non-specific tag "!" without a trace, though
// under YAML 1.2 it makes "! 12" the string "12" rather than a number. The
// node's position still points at the tag, or at an anchor before it.
func (d *yamlDecoder) lostTag(n *yaml.Node) bool {
	off, ok := d.offset(n.Line, n.Column)
	if !ok {
		return false
	}
	s := d.src[off:]
	if n.Anchor != "" && len(s) > 0 && s[0] == '&' {
		s = bytes.TrimLeft(s[min(len(s), 1+len(n.Anchor)):], " \t")
	}
	return len(s) > 0 && s[0] == '!'
}

// offset returns the byte offset of a line and column, with columns counted
// in characters as the YAML parser counts them. Nodes are decoded in file
// order, so it resumes from the last position when it can.
func (d *yamlDecoder) offset(line, col int) (int, bool) {
	if line < 1 || line > len(d.lines) || col < 1 {
		return 0, false
	}
	c := &d.cursor
	if line != c.line || col < c.col {
		c.line, c.col, c.off = line, 1, d.lines[line-1]
	}
	for c.col < col {
		if c.off >= len(d.src) || d.src[c.off] == '\n' || d.src[c.off] == '\r' {
			return 0, false
		}
		_, size := utf8.DecodeRune(d.src[c.off:])
		c.off += size
		c.col++
	}
	return c.off, true
}

func (d *yamlDecoder) errorAt(kind error, n *yaml.Node, message string) *DecodeError {
	return &DecodeError{Err: kind, Pointer: d.path.pointer(), Line: n.Line, Column: n.Column, Message: message}
}

func (d *yamlDecoder) tagError(n *yaml.Node, tag string) *DecodeError {
	return d.errorAt(ErrUnsupported, n, fmt.Sprintf("the tag %s at %s isn't supported: parsers read YAML tags differently, so quote the value instead", tag, d.path.where()))
}

func (d *yamlDecoder) tooDeep(n *yaml.Node) *DecodeError {
	return d.errorAt(ErrTooDeep, n, fmt.Sprintf("values at %s nest deeper than maxDepth (%d)", d.path.where(), d.lim.maxDepth))
}

func (d *yamlDecoder) limitError(err error, n *yaml.Node) error {
	if !errors.Is(err, ErrTooManyNodes) {
		return err // The context ended.
	}
	return d.errorAt(ErrTooManyNodes, n, fmt.Sprintf("the artifact holds more than maxNodes (%d) values, with YAML aliases expanded", d.lim.maxNodes))
}

// duplicateKey reports that the key at index i of map n repeats an earlier
// key.
func (d *yamlDecoder) duplicateKey(n *yaml.Node, i int, key string) *DecodeError {
	first := n.Content[0]
	for j := 0; j < i; j += 2 {
		if n.Content[j].Value == key {
			first = n.Content[j]
			break
		}
	}
	return d.errorAt(ErrDuplicateKey, n.Content[i], fmt.Sprintf("duplicate key %q in %s; it's first on line %d", key, d.path.where(), first.Line))
}
