package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
)

// jsonDecoder decodes JSON from encoding/json's token stream, which lets it
// catch what json.Unmarshal accepts silently: duplicate keys, and integers
// a double can't hold.
type jsonDecoder struct {
	data  []byte
	dec   *json.Decoder
	lim   limits
	count counter
	path  path
}

func decodeJSON(ctx context.Context, data []byte, lim limits) (any, error) {
	if bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
		return nil, &DecodeError{Err: ErrSyntax, Line: 1, Column: 1, Message: "the JSON starts with a byte order mark (U+FEFF), which JSON doesn't allow"}
	}
	d := &jsonDecoder{
		data:  data,
		dec:   json.NewDecoder(bytes.NewReader(data)),
		lim:   lim,
		count: counter{ctx: ctx, limit: lim.maxNodes},
	}
	d.dec.UseNumber()
	tok, start, err := d.next()
	if errors.Is(err, io.EOF) {
		return nil, &DecodeError{Err: ErrSyntax, Message: "the artifact is empty"}
	}
	if err != nil {
		return nil, d.syntaxError(err)
	}
	v, err := d.value(tok, start, 0)
	if err != nil {
		return nil, err
	}
	end := int(d.dec.InputOffset())
	if _, err := d.dec.Token(); !errors.Is(err, io.EOF) {
		off := end + len(data[end:]) - len(bytes.TrimLeft(data[end:], " \t\r\n"))
		return nil, d.errorAt(ErrSyntax, off, "there's more data after the JSON value")
	}
	if off := loneSurrogate(data); off >= 0 {
		d.path = nil
		return nil, d.errorAt(ErrUnsupported, off, fmt.Sprintf("the string escape %s is half of a UTF-16 surrogate pair without the other half, which isn't valid Unicode", data[off:off+6]))
	}
	return v, nil
}

// next reads the next token, and returns it with the offset where it
// starts.
func (d *jsonDecoder) next() (json.Token, int, error) {
	before := int(d.dec.InputOffset())
	tok, err := d.dec.Token()
	if err != nil {
		return nil, 0, err
	}
	// Token skips white space and the commas and colons between tokens.
	start := before
	for start < len(d.data) && strings.IndexByte(" \t\r\n,:", d.data[start]) >= 0 {
		start++
	}
	return tok, start, nil
}

// value decodes the value that starts with tok, at offset start, inside
// depth objects and arrays.
func (d *jsonDecoder) value(tok json.Token, start, depth int) (any, error) {
	if err := d.count.add(1); err != nil {
		return nil, d.limitError(err, start)
	}
	switch t := tok.(type) {
	case json.Delim:
		if depth+1 > d.lim.maxDepth {
			return nil, d.errorAt(ErrTooDeep, start, fmt.Sprintf("values at %s nest deeper than maxDepth (%d)", d.path.where(), d.lim.maxDepth))
		}
		switch t {
		case '{':
			return d.object(depth + 1)
		case '[':
			return d.array(depth + 1)
		}
	case json.Number:
		return d.number(t, start)
	case string, bool, nil:
		return t, nil
	}
	return nil, d.errorAt(ErrSyntax, start, fmt.Sprintf("invalid JSON: unexpected %v", tok))
}

func (d *jsonDecoder) object(depth int) (any, error) {
	m := map[string]any{}
	for d.dec.More() {
		tok, start, err := d.next()
		if err != nil {
			return nil, d.syntaxError(err)
		}
		key, ok := tok.(string)
		if !ok {
			return nil, d.errorAt(ErrSyntax, start, "invalid JSON: expected an object key")
		}
		if _, dup := m[key]; dup {
			return nil, d.errorAt(ErrDuplicateKey, start, fmt.Sprintf("duplicate key %q in %s", key, d.path.where()))
		}
		d.path.push(key)
		if tok, start, err = d.next(); err != nil {
			return nil, d.syntaxError(err)
		}
		v, err := d.value(tok, start, depth)
		if err != nil {
			return nil, err
		}
		d.path.pop()
		m[key] = v
	}
	if _, _, err := d.next(); err != nil {
		return nil, d.syntaxError(err)
	}
	return m, nil
}

func (d *jsonDecoder) array(depth int) (any, error) {
	s := []any{}
	for i := 0; d.dec.More(); i++ {
		tok, start, err := d.next()
		if err != nil {
			return nil, d.syntaxError(err)
		}
		d.path.pushIndex(i)
		v, err := d.value(tok, start, depth)
		if err != nil {
			return nil, err
		}
		d.path.pop()
		s = append(s, v)
	}
	if _, _, err := d.next(); err != nil {
		return nil, d.syntaxError(err)
	}
	return s, nil
}

// number converts a JSON number to a float64. An integer literal must fit a
// double exactly; any other number must be finite.
func (d *jsonDecoder) number(n json.Number, start int) (any, error) {
	s := string(n)
	if !strings.ContainsAny(s, ".eE") {
		i, err := strconv.ParseInt(s, 10, 64)
		if err != nil || i > MaxSafeInteger || i < -MaxSafeInteger {
			return nil, quoting(d.errorAt(ErrNumber, start, integerMessage(s, d.path)), s)
		}
		return float64(i), nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, quoting(d.errorAt(ErrNumber, start, fmt.Sprintf("the number %s at %s is too large for a double", s, d.path.where())), s)
	}
	return f + 0, nil // + 0 turns -0 into 0.
}

// quoting records the value that an error's message quotes.
func quoting(err *DecodeError, value string) *DecodeError {
	err.Value = value
	return err
}

func integerMessage(text string, p path) string {
	return fmt.Sprintf("the integer %s at %s is beyond ±%d (2^53 − 1), the largest a double holds exactly; quote it if it's an identifier",
		text, p.where(), MaxSafeInteger)
}

func (d *jsonDecoder) errorAt(kind error, off int, message string) *DecodeError {
	line, col := position(d.data, off)
	return &DecodeError{Err: kind, Pointer: d.path.pointer(), Line: line, Column: col, Message: message}
}

func (d *jsonDecoder) limitError(err error, off int) error {
	if !errors.Is(err, ErrTooManyNodes) {
		return err // The context ended.
	}
	return d.errorAt(ErrTooManyNodes, off, fmt.Sprintf("the artifact holds more than maxNodes (%d) values", d.lim.maxNodes))
}

func (d *jsonDecoder) syntaxError(err error) error {
	var syntax *json.SyntaxError
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF),
		errors.As(err, &syntax) && syntax.Error() == "unexpected end of JSON input":
		return d.errorAt(ErrSyntax, len(d.data), "invalid JSON: the text ends before the value does")
	case syntax != nil:
		// The bad byte is the last one the decoder read.
		return d.errorAt(ErrSyntax, int(syntax.Offset)-1, "invalid JSON: "+syntax.Error())
	default:
		return d.errorAt(ErrSyntax, int(d.dec.InputOffset()), "invalid JSON: "+err.Error())
	}
}

// loneSurrogate returns the offset of the first \u escape in a valid JSON
// text that stands for half of a UTF-16 surrogate pair without the other
// half, or -1 if there's none. encoding/json turns such an escape into
// U+FFFD while other parsers keep it, so the gate and a consumer would read
// the string differently.
func loneSurrogate(data []byte) int {
	inString := false
	for i := 0; i < len(data); i++ {
		switch c := data[i]; {
		case !inString:
			inString = c == '"'
		case c == '"':
			inString = false
		case c == '\\' && i+1 < len(data) && data[i+1] == 'u':
			if r := hex4(data[i+2:]); utf16.IsSurrogate(r) {
				if r >= 0xDC00 || i+7 >= len(data) || data[i+6] != '\\' || data[i+7] != 'u' {
					return i
				}
				if low := hex4(data[i+8:]); low < 0xDC00 || low > 0xDFFF {
					return i
				}
				i += 6 // The low half follows the high half.
			}
			i += 5
		case c == '\\':
			i++
		}
	}
	return -1
}

// hex4 decodes the four hex digits at the start of b, or returns -1.
func hex4(b []byte) rune {
	if len(b) < 4 {
		return -1
	}
	var r rune
	for _, c := range b[:4] {
		switch {
		case '0' <= c && c <= '9':
			r = r<<4 | rune(c-'0')
		case 'a' <= c && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case 'A' <= c && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return -1
		}
	}
	return r
}
