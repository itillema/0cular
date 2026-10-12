// Package report renders an eval.Result as text, Markdown or JSON.
//
// Every string in a result can come from untrusted input: an artifact's
// keys and values, a contract's messages, file paths. So each format
// escapes them, as the build plan's security model (§3.12) requires:
//   - Text replaces control characters, and the Unicode characters that
//     reorder or hide text, with visible escapes: \n for a line break, and a
//     \u escape for U+202E (right-to-left override).
//   - Markdown, for GitHub Job Summaries and pull request comments, does
//     the same, then escapes Markdown's punctuation and HTML's, keeps
//     @mentions and #references from notifying anyone, and cuts long
//     fields.
//   - JSON escapes the same characters, and HTML's < > &, as \u escapes, so
//     a report is safe to show and to embed.
//
// Evidence arrives with secrets already hidden: eval shows [redacted] in
// place of values read through fields that hold secrets (ADR-0015).
package report
