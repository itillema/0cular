// Package redact decides which fields of an artifact hold secrets, so that
// reports can show [redacted] instead of their values (ADR-0015).
//
// A field holds a secret when its name contains a pattern, comparing both
// as Normalize does: ignoring case, and ignoring everything but letters and
// digits. So the pattern apikey matches apiKey, api_key and X-API-KEY.
package redact

import (
	"strings"
	"unicode"
)

// Marker is what a report shows in place of a secret value.
const Marker = "[redacted]"

// Builtin are the patterns that always apply. A contract's report.redact
// adds to them; nothing removes them.
var Builtin = []string{"password", "passwd", "secret", "token", "credential", "apikey", "privatekey", "authorization"}

// Normalize returns a field name or a pattern as matching compares it: its
// letters and digits, in lower case.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// HasLetter reports whether a pattern can match a field name: it needs a
// letter, since a pattern of digits alone would match array indexes.
func HasLetter(pattern string) bool {
	return strings.IndexFunc(pattern, unicode.IsLetter) >= 0
}

// Matcher decides which field names hold secrets. The zero Matcher matches
// none; New returns one with the built-in patterns.
type Matcher struct {
	patterns []string
}

// New returns a Matcher for the built-in patterns and the extra ones, such
// as a contract's report.redact. Extra patterns without a letter are
// ignored.
func New(extra []string) *Matcher {
	m := &Matcher{}
	for _, p := range append(append([]string(nil), Builtin...), extra...) {
		if n := Normalize(p); HasLetter(n) {
			m.patterns = append(m.patterns, n)
		}
	}
	return m
}

// Field reports whether a field with this name holds a secret.
func (m *Matcher) Field(name string) bool {
	if m == nil || len(m.patterns) == 0 {
		return false
	}
	n := Normalize(name)
	for _, p := range m.patterns {
		if strings.Contains(n, p) {
			return true
		}
	}
	return false
}

// Pointer reports whether a JSON Pointer (RFC 6901) passes through a field
// that holds a secret: whether any of its tokens does.
func (m *Matcher) Pointer(pointer string) bool {
	if pointer == "" {
		return false
	}
	for _, token := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		if m.Field(strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")) {
			return true
		}
	}
	return false
}
