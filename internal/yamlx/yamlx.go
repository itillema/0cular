// Package yamlx parses YAML strictly, for contract files and artifacts.
//
// ParseDocument reads exactly one document into a node tree and never
// panics. The resolve helpers read scalars under the YAML 1.2 core schema
// (ADR-0005): only true and false are booleans, octal integers need the 0o
// prefix, and a plain scalar that isn't a null, a boolean or a number is a
// string. go.yaml.in/yaml/v3 applies some YAML 1.1 rules when it decodes
// into Go values (yes becomes true, 0777 becomes 511), so callers walk the
// node tree with these helpers instead of decoding into Go types.
package yamlx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Errors that this package returns.
var (
	// ErrEmpty means the input holds no YAML document.
	ErrEmpty = errors.New("the YAML document is empty")
	// ErrMultipleDocuments means the input holds more than one YAML document.
	ErrMultipleDocuments = errors.New("the input holds more than one YAML document")
	// ErrTag means a node carries an explicit tag, such as !!str or !custom.
	ErrTag = errors.New("explicit YAML tags aren't supported")
	// ErrRange means a number doesn't fit the type it's parsed into.
	ErrRange = errors.New("out of range")
)

// SyntaxError is a YAML syntax error.
type SyntaxError struct {
	// Line is the 1-based line the parser reported, or 0 if it reported none.
	Line int
	// Message is the parser's description of the problem.
	Message string
}

// Error returns the error's text.
func (e *SyntaxError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("YAML syntax error on line %d: %s", e.Line, e.Message)
	}
	return "YAML syntax error: " + e.Message
}

// lineMessage splits go.yaml.in/yaml/v3's "yaml: line N: message" errors.
var lineMessage = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)

func syntaxError(err error) *SyntaxError {
	text := err.Error()
	if m := lineMessage.FindStringSubmatch(text); m != nil {
		line, _ := strconv.Atoi(m[1])
		return &SyntaxError{Line: line, Message: m[2]}
	}
	text, _ = strings.CutPrefix(text, "yaml: ")
	return &SyntaxError{Message: text}
}

// ParseDocument parses data as exactly one YAML document and returns the
// document's root node. It returns ErrEmpty for input without a document,
// ErrMultipleDocuments for more than one, and a *SyntaxError for malformed
// YAML. A panic inside the YAML parser is returned as an error, so a hostile
// input can't crash the caller.
func ParseDocument(data []byte) (root *yaml.Node, err error) {
	defer func() {
		if r := recover(); r != nil {
			root, err = nil, fmt.Errorf("internal YAML parser error: %v", r)
		}
	}()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, ErrEmpty
		}
		return nil, syntaxError(err)
	}
	var next yaml.Node
	switch err := dec.Decode(&next); {
	case errors.Is(err, io.EOF):
	case err != nil:
		return nil, syntaxError(err)
	default:
		return nil, ErrMultipleDocuments
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, ErrEmpty
	}
	return doc.Content[0], nil
}

// Kind is what a scalar resolves to under the YAML 1.2 core schema.
type Kind int

// The kinds of scalar.
const (
	KindString Kind = iota
	KindNull
	KindBool
	KindInt
	KindFloat
)

// String returns the kind's name, for messages.
func (k Kind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindBool:
		return "boolean"
	case KindInt:
		return "integer"
	case KindFloat:
		return "number"
	default:
		return "string"
	}
}

// The YAML 1.2 core schema's patterns for integers and floats.
var (
	intDecimal = regexp.MustCompile(`^[-+]?[0-9]+$`)
	intOctal   = regexp.MustCompile(`^0o[0-7]+$`)
	intHex     = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)
	floatNum   = regexp.MustCompile(`^[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?$`)
	floatInf   = regexp.MustCompile(`^[-+]?\.(inf|Inf|INF)$`)
	floatNaN   = regexp.MustCompile(`^\.(nan|NaN|NAN)$`)
)

// ResolvePlain reports what the text of a plain (unquoted) scalar means
// under the YAML 1.2 core schema.
func ResolvePlain(text string) Kind {
	switch text {
	case "", "~", "null", "Null", "NULL":
		return KindNull
	case "true", "True", "TRUE", "false", "False", "FALSE":
		return KindBool
	}
	// Only a digit, a sign or a dot can start a number, so most strings
	// skip the patterns below.
	if c := text[0]; (c < '0' || c > '9') && c != '-' && c != '+' && c != '.' {
		return KindString
	}
	switch {
	case intDecimal.MatchString(text), intOctal.MatchString(text), intHex.MatchString(text):
		return KindInt
	case floatNum.MatchString(text), floatInf.MatchString(text), floatNaN.MatchString(text):
		return KindFloat
	}
	return KindString
}

// ScalarKind resolves a scalar node. Quoted and block scalars are strings,
// plain scalars resolve with ResolvePlain, and a scalar with an explicit tag
// returns ErrTag.
func ScalarKind(n *yaml.Node) (Kind, error) {
	if n.Kind != yaml.ScalarNode {
		return 0, fmt.Errorf("not a scalar")
	}
	if n.Style&yaml.TaggedStyle != 0 {
		return 0, ErrTag
	}
	if n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return KindString, nil
	}
	return ResolvePlain(n.Value), nil
}

// ParseBool parses the text of a boolean scalar.
func ParseBool(text string) (bool, error) {
	switch text {
	case "true", "True", "TRUE":
		return true, nil
	case "false", "False", "FALSE":
		return false, nil
	}
	return false, fmt.Errorf("%q isn't a boolean", text)
}

// ParseInt parses the text of an integer scalar: decimal with an optional
// sign, 0o octal, or 0x hexadecimal. Leading zeros don't make a decimal
// integer octal. An integer that doesn't fit in an int64 returns an error
// that wraps ErrRange.
func ParseInt(text string) (int64, error) {
	var (
		digits string
		base   int
	)
	switch {
	case intOctal.MatchString(text):
		digits, base = text[2:], 8
	case intHex.MatchString(text):
		digits, base = text[2:], 16
	case intDecimal.MatchString(text):
		digits, base = text, 10
	default:
		return 0, fmt.Errorf("%q isn't an integer", text)
	}
	n, err := strconv.ParseInt(digits, base, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is %w for a 64-bit integer", text, ErrRange)
	}
	return n, nil
}

// ParseFloat parses the text of an integer or float scalar, including .inf,
// -.inf and .nan. Callers that need finite numbers must check the result. A
// number too large for a float64 returns an error that wraps ErrRange; one
// too small to represent becomes zero.
func ParseFloat(text string) (float64, error) {
	switch {
	case floatInf.MatchString(text):
		if text[0] == '-' {
			return math.Inf(-1), nil
		}
		return math.Inf(1), nil
	case floatNaN.MatchString(text):
		return math.NaN(), nil
	case intOctal.MatchString(text), intHex.MatchString(text):
		n, err := ParseInt(text)
		return float64(n), err
	case floatNum.MatchString(text):
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0, fmt.Errorf("%q is %w for a number", text, ErrRange)
		}
		return f, nil
	}
	return 0, fmt.Errorf("%q isn't a number", text)
}
