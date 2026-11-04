package report

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// visible returns s with every character that a terminal or browser
// wouldn't show as itself replaced by an escape: \n, \r and \t; \x for the
// other C0 controls, DEL and bytes that aren't UTF-8; and \u, or \U
// above U+FFFF, for the C1 controls and the format characters that
// reorder or hide text, such as U+202E (right-to-left override), U+200B
// (zero-width space) and U+2028 (line separator).
func visible(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case invisible(r) && r > 0xffff:
			fmt.Fprintf(&b, `\U%08x`, r)
		case invisible(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

// invisible reports whether r is a control or format character, or a line
// or paragraph separator: one that isn't shown as itself.
func invisible(r rune) bool {
	return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp)
}

// cut shortens s to at most n characters, ending a shortened string with an
// ellipsis.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n-1 {
			return s[:i] + "…"
		}
		count++
	}
	return s
}

// markdownPunctuation is what markdownText escapes with a backslash: every
// character that Markdown, GitHub's extensions or HTML give meaning to.
const markdownPunctuation = "\\`*_{}[]()#+-.!|<>~&$"

// zeroWidthSpace, as an HTML entity, separates @ and # from what follows
// them, so GitHub doesn't read a mention or a reference there.
const zeroWidthSpace = "&#8203;"

// markdownText returns untrusted text as Markdown that renders as the text
// itself: visible, cut to n characters, with Markdown's and HTML's
// punctuation escaped, and with mentions (@user, @org/team) and references
// (#123) broken up so that they notify no one.
func markdownText(s string, n int) string {
	s = visible(cut(s, n))
	var b strings.Builder
	for i, r := range s {
		switch {
		case r == '@':
			b.WriteString("@" + zeroWidthSpace)
		case r == '#' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9':
			b.WriteString(`\#` + zeroWidthSpace)
		case strings.ContainsRune(markdownPunctuation, r):
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
