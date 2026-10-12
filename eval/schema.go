package eval

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/itillema/0cular/contract"
)

// MaxViolations is how many schema violations Validate lists.
const MaxViolations = 20

// maxMessageRunes bounds a violation's message, which can quote the
// artifact.
const maxMessageRunes = 200

// ErrSchema is wrapped by every error CompileSchema returns.
var ErrSchema = errors.New("invalid JSON Schema")

// Schema is a compiled JSON Schema that an artifact must match.
type Schema struct {
	name   string
	schema *jsonschema.Schema
}

// Violation is one way a value fails its JSON Schema.
type Violation struct {
	// Pointer is a JSON Pointer to the value that fails. It's empty for the
	// top-level value.
	Pointer string
	// Keyword is a JSON Pointer to the schema keyword that fails, such as
	// /properties/features/maxItems.
	Keyword string
	// Message describes the failure. It can quote the artifact, so reports
	// must escape it.
	Message string
}

// CompileSchema compiles the JSON Schema that c names, from the file
// contract.LoadFile read into c.Schema.Data. It returns nil and no error
// for a contract without a schema.
//
// The schema defaults to Draft 2020-12, and it asserts formats: a value
// must be a valid ipv4, date-time and so on, not just labeled one. It must
// be one self-contained file: a reference to another file or a URL is an
// error, so the schema that reviewers see is the whole schema. Its JSON is
// read as strictly as an artifact's, so a duplicate key is an error too.
func CompileSchema(c *contract.Contract) (s *Schema, err error) {
	if c.Schema == nil {
		return nil, nil
	}
	name := c.Schema.JSONSchema
	if c.Schema.Data == nil {
		return nil, fmt.Errorf("%w: %s wasn't read; load the contract with contract.LoadFile", ErrSchema, name)
	}
	defer func() {
		if r := recover(); r != nil {
			s, err = nil, fmt.Errorf("%w: %s: internal JSON Schema compiler error: %v", ErrSchema, name, r)
		}
	}()
	doc, err := decodeSchemaFile(c.Schema.Data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrSchema, name, err)
	}
	url := "file:///" + name[strings.LastIndexByte(name, '/')+1:]
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseLoader(selfContained{})
	if err := compiler.AddResource(url, doc); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrSchema, name, err)
	}
	schema, err := compiler.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %s", ErrSchema, name, compileMessage(err))
	}
	return &Schema{name: name, schema: schema}, nil
}

// compileMessage describes on one line why a schema doesn't compile. For a
// schema that breaks the JSON Schema rules, it names the first specific
// violation, as Validate does for an artifact.
func compileMessage(err error) string {
	var invalid *jsonschema.SchemaValidationError
	var verr *jsonschema.ValidationError
	if errors.As(err, &invalid) && errors.As(invalid.Err, &verr) {
		if all := violationsOf(verr); len(all) > 0 {
			message := describeViolation(all[0])
			if len(all) > 1 {
				message += fmt.Sprintf("; and %d more", len(all)-1)
			}
			return message
		}
	}
	return strings.Join(strings.Fields(err.Error()), " ")
}

// describeViolation says where a violation is and what it is.
func describeViolation(v Violation) string {
	if v.Pointer == "" {
		return "at the top level: " + v.Message
	}
	return "at " + v.Pointer + ": " + v.Message
}

// decodeSchemaFile decodes a JSON Schema file with the artifact decoder,
// within the size the contract package allows for a schema.
func decodeSchemaFile(data []byte) (any, error) {
	if !utf8.Valid(data) {
		line, col := position(data, invalidUTF8(data))
		return nil, &DecodeError{Err: ErrEncoding, Line: line, Column: col, Message: "the schema isn't valid UTF-8"}
	}
	return decodeJSON(context.Background(), data, limits{maxDepth: contract.MaxDepthCeiling, maxNodes: contract.MaxSchemaBytes})
}

// selfContained refuses every schema resource but the one a contract names.
type selfContained struct{}

// Load refuses url.
func (selfContained) Load(url string) (any, error) {
	return nil, fmt.Errorf("the schema refers to %s, but 0cular reads only the schema file the contract names; bundle the other schemas into it", url)
}

// Name returns the schema's path, as the contract gives it.
func (s *Schema) Name() string {
	return s.name
}

// Validate checks v, the Value of a decoded Document, against the schema.
// It returns the first MaxViolations violations, with the most specific
// failures only, in document order; and how many violations there are in
// all. A value that matches the schema returns no violations.
func (s *Schema) Validate(v any) (violations []Violation, total int) {
	defer func() {
		if r := recover(); r != nil {
			violations, total = []Violation{{Message: fmt.Sprintf("internal JSON Schema validator error: %v", r)}}, 1
		}
	}()
	err := s.schema.Validate(v)
	if err == nil {
		return nil, 0
	}
	var verr *jsonschema.ValidationError
	if !errors.As(err, &verr) {
		return []Violation{{Message: truncate(err.Error())}}, 1
	}
	all := violationsOf(verr)
	return all[:min(len(all), MaxViolations)], len(all)
}

// violationsOf returns the most specific failures in a validation error,
// in document order and without repeats.
func violationsOf(verr *jsonschema.ValidationError) []Violation {
	var all []Violation
	var walk func(u *jsonschema.OutputUnit)
	walk = func(u *jsonschema.OutputUnit) {
		if len(u.Errors) == 0 {
			message := "fails the schema"
			if u.Error != nil {
				message = u.Error.String()
			}
			all = append(all, Violation{Pointer: u.InstanceLocation, Keyword: u.KeywordLocation, Message: truncate(message)})
			return
		}
		for i := range u.Errors {
			walk(&u.Errors[i])
		}
	}
	walk(verr.DetailedOutput())
	// The validator visits an object's properties in no fixed order.
	slices.SortStableFunc(all, func(a, b Violation) int {
		return cmp.Or(comparePointers(a.Pointer, b.Pointer), comparePointers(a.Keyword, b.Keyword), strings.Compare(a.Message, b.Message))
	})
	return slices.CompactFunc(all, func(a, b Violation) bool { return a == b })
}

// comparePointers orders JSON Pointers segment by segment, comparing array
// indexes as numbers, so /items/2 comes before /items/10.
func comparePointers(a, b string) int {
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, y := as[i], bs[i]
		if isIndex(x) && isIndex(y) {
			if c := cmp.Or(cmp.Compare(len(x), len(y)), strings.Compare(x, y)); c != 0 {
				return c
			}
			continue
		}
		if c := strings.Compare(x, y); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(as), len(bs))
}

// isIndex reports whether a JSON Pointer segment is an array index: digits,
// without a leading zero.
func isIndex(s string) bool {
	if s == "" || len(s) > 1 && s[0] == '0' {
		return false
	}
	return strings.Trim(s, "0123456789") == ""
}

// truncate shortens s to at most maxMessageRunes characters, ending a
// shortened message with an ellipsis.
func truncate(s string) string {
	count := 0
	for i := range s {
		if count == maxMessageRunes-1 && utf8.RuneCountInString(s[i:]) > 1 {
			return s[:i] + "…"
		}
		count++
	}
	return s
}
