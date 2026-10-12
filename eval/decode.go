package eval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/itillema/0cular/contract"
)

// MaxAliases is how many aliases a YAML artifact may use.
const MaxAliases = 100

// MaxSafeInteger is the largest integer a double holds exactly, 2^53 − 1.
// An integer literal beyond ±MaxSafeInteger is a decode error (ADR-0013).
const MaxSafeInteger = 1<<53 - 1

// Errors that a *DecodeError wraps, so that errors.Is can tell the reasons
// apart.
var (
	// ErrTooLarge means the artifact is bigger than maxBytes.
	ErrTooLarge = errors.New("artifact too large")
	// ErrEncoding means the artifact isn't valid UTF-8, or holds characters
	// that parsers disagree about.
	ErrEncoding = errors.New("invalid text")
	// ErrSyntax means the artifact isn't one well-formed document.
	ErrSyntax = errors.New("syntax error")
	// ErrDuplicateKey means a map holds the same key twice.
	ErrDuplicateKey = errors.New("duplicate key")
	// ErrKey means a map key isn't a string.
	ErrKey = errors.New("map key isn't a string")
	// ErrNumber means a number has no exact double form: an integer beyond
	// ±MaxSafeInteger, or a number too large to be finite.
	ErrNumber = errors.New("number out of range")
	// ErrUnsupported means a YAML feature that parsers read differently,
	// such as a tag or a merge key, or a JSON string that isn't valid
	// Unicode.
	ErrUnsupported = errors.New("unsupported construct")
	// ErrTooDeep means the values nest deeper than maxDepth.
	ErrTooDeep = errors.New("nesting too deep")
	// ErrTooManyNodes means the artifact holds more values than maxNodes,
	// after YAML aliases are expanded.
	ErrTooManyNodes = errors.New("too many values")
	// ErrTooManyAliases means a YAML artifact uses more than MaxAliases
	// aliases.
	ErrTooManyAliases = errors.New("too many YAML aliases")
)

// DecodeError reports why an artifact failed to decode.
type DecodeError struct {
	// Err is one of the Err variables above.
	Err error
	// Pointer is a JSON Pointer (RFC 6901) to the value where decoding
	// failed. It's empty for the top-level value and for problems with the
	// whole artifact.
	Pointer string
	// Line and Column locate the problem in the artifact, counting from 1.
	// Columns count characters. Either is 0 if it's unknown. For a JSON
	// syntax error they come from encoding/json, whose position can differ
	// by a character from one Go release to the next.
	Line, Column int
	// Message describes the problem.
	Message string
}

// Error returns the message, after the line and column when they're known.
func (e *DecodeError) Error() string {
	switch {
	case e.Line > 0 && e.Column > 0:
		return fmt.Sprintf("line %d, column %d: %s", e.Line, e.Column, e.Message)
	case e.Line > 0:
		return fmt.Sprintf("line %d: %s", e.Line, e.Message)
	default:
		return e.Message
	}
}

// Unwrap returns Err.
func (e *DecodeError) Unwrap() error {
	return e.Err
}

// Document is a decoded artifact.
type Document struct {
	// Value is the artifact's content: nil, a bool, a float64, a string, a
	// []any or a map[string]any, nested. YAML aliases share one value, so
	// treat Value as read-only.
	Value any
	// Size is the artifact's length in bytes.
	Size int64
	// Digest is "sha256:" followed by the hex SHA-256 of the artifact.
	Digest string
}

// limits are the caps that apply while decoding one artifact.
type limits struct {
	maxDepth int
	maxNodes int64
}

// Decode decodes an artifact under the decode model that every format
// shares (ADR-0005, ADR-0013):
//
//   - The size is checked against maxBytes before anything else, and the
//     text must be valid UTF-8.
//   - Maps may not repeat a key, and their keys must be strings.
//   - Numbers become float64. An integer literal beyond ±MaxSafeInteger is
//     an error, since a double can't hold it exactly, and so is a number too
//     large to be finite.
//   - Values may nest at most maxDepth deep: {"a": [1]} nests 2 deep. The
//     artifact may hold at most maxNodes values, counting objects, arrays
//     and scalars, after YAML aliases are expanded.
//   - YAML scalars resolve under the YAML 1.2 core schema, so timestamps
//     stay strings. A YAML artifact may use at most MaxAliases aliases, and
//     may not use tags, merge keys, or the characters U+0085, U+2028 and
//     U+2029, which YAML 1.1 and 1.2 parsers read differently.
//
// A problem with the artifact returns a *DecodeError. An artifact that
// fails to decode is blocked at the gate, and rejected by a loader. Decode
// returns ctx's error if ctx ends first, and another error if a's format or
// caps aren't valid.
func Decode(ctx context.Context, data []byte, a contract.Artifact) (*Document, error) {
	if a.MaxBytes < 1 || a.MaxDepth < 1 || a.MaxNodes < 1 {
		return nil, fmt.Errorf("eval: invalid artifact caps: maxBytes %d, maxDepth %d and maxNodes %d must each be at least 1",
			a.MaxBytes, a.MaxDepth, a.MaxNodes)
	}
	if size := int64(len(data)); size > a.MaxBytes {
		return nil, &DecodeError{
			Err:     ErrTooLarge,
			Message: fmt.Sprintf("the artifact is %d bytes, more than maxBytes (%d)", size, a.MaxBytes),
		}
	}
	if !utf8.Valid(data) {
		off := invalidUTF8(data)
		line, col := position(data, off)
		return nil, &DecodeError{Err: ErrEncoding, Line: line, Column: col, Message: "the artifact isn't valid UTF-8"}
	}
	lim := limits{maxDepth: min(a.MaxDepth, contract.MaxDepthCeiling), maxNodes: int64(a.MaxNodes)}
	var (
		v   any
		err error
	)
	switch a.Format {
	case contract.FormatJSON:
		v, err = decodeJSON(ctx, data, lim)
	case contract.FormatYAML:
		v, err = decodeYAML(ctx, data, lim)
	default:
		return nil, fmt.Errorf("eval: unsupported artifact format %q", a.Format)
	}
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	return &Document{Value: v, Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

// invalidUTF8 returns the offset of the first byte in data that isn't part
// of a valid UTF-8 sequence.
func invalidUTF8(data []byte) int {
	for off := 0; off < len(data); {
		r, size := utf8.DecodeRune(data[off:])
		if r == utf8.RuneError && size == 1 {
			return off
		}
		off += size
	}
	return len(data)
}

// position returns the line and column of byte offset off in data. Lines
// end at LF; columns count characters.
func position(data []byte, off int) (line, col int) {
	off = max(0, min(off, len(data)))
	line = 1 + bytes.Count(data[:off], []byte{'\n'})
	start := bytes.LastIndexByte(data[:off], '\n') + 1
	return line, 1 + utf8.RuneCount(data[start:off])
}

// counter counts the values decoded so far, and checks the context every
// so often.
type counter struct {
	ctx   context.Context
	nodes int64
	limit int64
}

// add counts n more values. It returns ErrTooManyNodes, wrapped by the
// caller into a *DecodeError, or the context's error.
func (c *counter) add(n int64) error {
	before := c.nodes
	c.nodes += n
	if c.nodes > c.limit {
		return ErrTooManyNodes
	}
	// Check the context about every 4096 values.
	if before>>12 != c.nodes>>12 {
		return c.ctx.Err()
	}
	return nil
}

// path tracks the location of the value being decoded, for JSON Pointers.
// It formats array indexes only when a pointer is needed.
type path []segment

// segment is a map key, or an array index when index isn't negative.
type segment struct {
	key   string
	index int
}

func (p *path) push(key string) { *p = append(*p, segment{key: key, index: -1}) }
func (p *path) pushIndex(i int) { *p = append(*p, segment{index: i}) }
func (p *path) pop()            { *p = (*p)[:len(*p)-1] }

var pointerEscaper = strings.NewReplacer("~", "~0", "/", "~1")

// pointer returns the path as a JSON Pointer (RFC 6901).
func (p path) pointer() string {
	var b strings.Builder
	for _, s := range p {
		b.WriteByte('/')
		if s.index >= 0 {
			b.WriteString(strconv.Itoa(s.index))
		} else {
			b.WriteString(pointerEscaper.Replace(s.key))
		}
	}
	return b.String()
}

// where describes the path's location for a message.
func (p path) where() string {
	if len(p) == 0 {
		return "the top level"
	}
	return p.pointer()
}
