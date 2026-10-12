package eval

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestCheckRedacts checks that evidence hides every value read through a
// field that holds a secret, by the built-in patterns or the contract's
// report.redact, and still shows the others (ADR-0015).
func TestCheckRedacts(t *testing.T) {
	c := mustContract(t, `apiVersion: 0cular/v1alpha1
kind: Contract
metadata: {name: redaction}
artifact: {format: json, maxBytes: 1MiB}
schema: {jsonSchema: s.json}
invariants:
  - {name: token, expr: "self.auth.token == 'tok_expected'"}
  - {name: ssn, expr: "self.customer.ssn != self.customer.old_ssn"}
  - {name: user, expr: "self.auth.user == 'root'"}
  - {name: sessions, expr: "self.sessions.all(s, s.token.startsWith('tok_') || s.token == '')"}
diffBudgets:
  - {name: pin-stable, measure: self.customer.pin_number, maxChangePct: 1}
  - {name: session-churn, collection: self.sessions, key: item.token, maxAdded: 0}
report:
  redact: [ssn, pin]
`, `{"properties": {"auth": {"properties": {"token": {"pattern": "^tok_"}}}}}`)
	candidate := `{
		"auth": {"token": "abc-SECRET-1", "user": "ops"},
		"customer": {"ssn": "999-SECRET-2", "old_ssn": "999-SECRET-2", "pin_number": 4321},
		"sessions": [{"token": "dup-SECRET-3"}, {"token": "dup-SECRET-3"}]
	}`
	baseline := `{"auth": {"token": "tok_a", "user": "ops"}, "customer": {"ssn": "1", "old_ssn": "2", "pin_number": 1234}, "sessions": []}`
	r := mustCheck(t, mustEvaluator(t, c), candidate, &baseline)

	got := map[string]string{}
	for _, check := range r.Checks {
		got[check.Name] = check.Evidence
		for _, v := range check.Violations {
			got[check.Name+" "+v.Pointer] = v.Message
		}
	}
	//nolint:gosec // G101: these are evidence strings that hide secrets, not credentials.
	want := map[string]string{
		"candidate":          "1.8 KiB ≤ 1 MiB; valid JSON",
		"baseline":           "1.1 KiB ≤ 1 MiB; valid JSON",
		"s.json":             "at /auth/token: [redacted] fails the schema's pattern",
		"s.json /auth/token": "[redacted] fails the schema's pattern",
		"token":              `self.auth.token = [redacted], not == "tok_expected"`,
		"ssn":                "self.customer.ssn = [redacted], not != self.customer.old_ssn ([redacted])",
		"user":               `self.auth.user = "ops", not == "root"`,
		"sessions":           "fails for self.sessions[0]: s.token = [redacted], not == \"\"",
		"pin-stable":         "[redacted] → [redacted]; budget ±1%",
		"session-churn":      "on the candidate: two items have the key [redacted]",
	}
	for name := range want {
		if strings.HasSuffix(name, "candidate") || strings.HasSuffix(name, "baseline") {
			delete(want, name) // The sizes depend on the test's indentation.
			delete(got, name)
		}
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("evidence mismatch (-want +got):\n%s", diff)
	}
	for _, check := range r.Checks {
		if strings.Contains(check.Evidence+check.Message, "SECRET") {
			t.Errorf("check %s shows a secret: %q", check.Name, check.Evidence)
		}
		for _, v := range check.Violations {
			if strings.Contains(v.Message, "SECRET") {
				t.Errorf("check %s shows a secret: %q", check.Name, v.Message)
			}
		}
		if check.Name == "pin-stable" && (check.Budget == nil || !check.Budget.Redacted) {
			t.Errorf("pin-stable's budget values = %+v, want them marked redacted", check.Budget)
		}
	}
}

// TestDecodeEvidenceRedacts checks that a decode error hides the value it
// quotes when the value's field holds a secret, and only then.
func TestDecodeEvidenceRedacts(t *testing.T) {
	e := mustEvaluator(t, mustContract(t, "apiVersion: 0cular/v1alpha1\nkind: Contract\nmetadata: {name: d}\nartifact: {format: json, maxBytes: 1MiB}\n", ""))
	tests := []struct{ candidate, want string }{
		{
			`{"api_key": 12345678901234567890}`,
			"line 1, column 13: the integer [redacted] at /api_key is beyond ±9007199254740991 (2^53 − 1), the largest a double holds exactly; quote it if it's an identifier",
		},
		{
			`{"id": 12345678901234567890}`,
			"line 1, column 8: the integer 12345678901234567890 at /id is beyond ±9007199254740991 (2^53 − 1), the largest a double holds exactly; quote it if it's an identifier",
		},
		{`{"secrets": [1e400]}`, "line 1, column 14: the number [redacted] at /secrets/0 is too large for a double"},
	}
	for _, tt := range tests {
		r := mustCheck(t, e, tt.candidate, nil)
		if got := r.Checks[0].Evidence; got != tt.want {
			t.Errorf("Check(%s) envelope evidence = %q, want %q", tt.candidate, got, tt.want)
		}
	}
}
