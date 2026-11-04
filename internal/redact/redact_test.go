package redact

import (
	"testing"

	"pgregory.net/rapid"
)

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"apiKey":        "apikey",
		"X-API-KEY":     "xapikey",
		"private_key_2": "privatekey2",
		"Contraseña":    "contraseña",
		"--":            "",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatcherField(t *testing.T) {
	m := New([]string{"SSN", "pin_code", "123"})
	for name, want := range map[string]bool{
		"password":        true,
		"db_password":     true,
		"PASSWD":          true,
		"clientSecret":    true,
		"accessToken":     true,
		"credentials":     true,
		"X-Api-Key":       true,
		"private-key":     true,
		"Authorization":   true,
		"ssn":             true,
		"customer_SSN":    true,
		"pinCode":         true,
		"limit":           false,
		"key":             false,
		"author":          false,
		"code123":         false, // the pattern 123 has no letter, so it's ignored
		"pin":             false,
		"":                false,
		"tokenizer_model": true, // over-redaction is the safe side
	} {
		if got := m.Field(name); got != want {
			t.Errorf("Field(%q) = %v, want %v", name, got, want)
		}
	}
	var none *Matcher
	if none.Field("password") || (&Matcher{}).Field("password") {
		t.Error("a nil or zero Matcher matches password; want it to match nothing")
	}
}

func TestMatcherPointer(t *testing.T) {
	m := New(nil)
	for pointer, want := range map[string]bool{
		"":                       false,
		"/policies/3/limit":      false,
		"/auth/token":            true,
		"/credentials/0/value":   true,
		"/a~1password/x":         true,
		"/headers/Authorization": true,
		"/tokens~0list":          true,
	} {
		if got := m.Pointer(pointer); got != want {
			t.Errorf("Pointer(%q) = %v, want %v", pointer, got, want)
		}
	}
}

// TestMatcherProperties checks that a name matches whatever its
// normalization matches, and that adding patterns never unmatches a name.
func TestMatcherProperties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.StringMatching(`[A-Za-z0-9_\-. ]{0,24}`).Draw(t, "name")
		extra := rapid.SliceOfN(rapid.StringMatching(`[A-Za-z][A-Za-z0-9_\-]{0,8}`), 0, 4).Draw(t, "extra")
		m, more := New(nil), New(extra)
		if m.Field(name) != m.Field(Normalize(name)) {
			t.Fatalf("Field(%q) and Field(%q) disagree", name, Normalize(name))
		}
		if m.Field(name) && !more.Field(name) {
			t.Fatalf("extra patterns %q unmatched %q", extra, name)
		}
		for _, p := range extra {
			if !more.Field("x_" + p + "_y") {
				t.Fatalf("pattern %q doesn't match a name that contains it", p)
			}
		}
	})
}
