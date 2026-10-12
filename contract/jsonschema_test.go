package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"

	"github.com/itillema/0cular/internal/yamlx"
)

// compiledSchema compiles the contract JSON Schema once.
var compiledSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(JSONSchema()))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(JSONSchemaID, doc); err != nil {
		return nil, err
	}
	return c.Compile(JSONSchemaID)
})

// validateSchema checks the contract in src against the JSON Schema. It
// returns errNoJSON for YAML that has no JSON form.
func validateSchema(t fataler, src string) error {
	t.Helper()
	schema, err := compiledSchema()
	if err != nil {
		t.Fatalf("compiling the contract JSON Schema: %v", err)
	}
	v, err := yamlToJSON([]byte(src))
	if err != nil {
		return fmt.Errorf("%w: %w", errNoJSON, err)
	}
	return schema.Validate(v)
}

var errNoJSON = errors.New("the YAML has no JSON form")

// yamlToJSON converts one YAML document to the value a JSON Schema
// validates, resolving scalars under the YAML 1.2 core schema, as editors'
// YAML language servers do. YAML that JSON can't represent is an error:
// anchors, aliases, tags, merge keys, duplicate or non-scalar keys, and
// non-finite numbers.
func yamlToJSON(data []byte) (any, error) {
	root, err := yamlx.ParseDocument(data)
	if err != nil {
		return nil, err
	}
	return nodeToJSON(root)
}

func nodeToJSON(n *yaml.Node) (any, error) {
	if n.Kind == yaml.AliasNode || n.Anchor != "" || n.Style&yaml.TaggedStyle != 0 {
		return nil, fmt.Errorf("line %d: anchor, alias or tag", n.Line)
	}
	switch n.Kind {
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Kind != yaml.ScalarNode || k.Anchor != "" || k.Style&yaml.TaggedStyle != 0 || k.Value == "<<" && k.Style == 0 {
				return nil, fmt.Errorf("line %d: a key JSON can't hold", k.Line)
			}
			if _, dup := m[k.Value]; dup {
				return nil, fmt.Errorf("line %d: duplicate key %q", k.Line, k.Value)
			}
			value, err := nodeToJSON(v)
			if err != nil {
				return nil, err
			}
			m[k.Value] = value
		}
		return m, nil
	case yaml.SequenceNode:
		s := make([]any, 0, len(n.Content))
		for _, e := range n.Content {
			value, err := nodeToJSON(e)
			if err != nil {
				return nil, err
			}
			s = append(s, value)
		}
		return s, nil
	case yaml.ScalarNode:
		kind, err := yamlx.ScalarKind(n)
		if err != nil {
			return nil, err
		}
		switch kind {
		case yamlx.KindNull:
			return nil, nil
		case yamlx.KindBool:
			return yamlx.ParseBool(n.Value)
		case yamlx.KindInt:
			digits, base := n.Value, 10
			if prefix := n.Value[:min(2, len(n.Value))]; prefix == "0o" || prefix == "0x" {
				digits, base = n.Value[2:], map[string]int{"0o": 8, "0x": 16}[prefix]
			}
			i, ok := new(big.Int).SetString(digits, base)
			if !ok {
				return nil, fmt.Errorf("line %d: %q isn't an integer", n.Line, n.Value)
			}
			return json.Number(i.String()), nil
		case yamlx.KindFloat:
			f, err := yamlx.ParseFloat(n.Value)
			if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
				return nil, fmt.Errorf("line %d: JSON has no number %s", n.Line, n.Value)
			}
			return json.Number(strconv.FormatFloat(f, 'g', -1, 64)), nil
		default:
			return n.Value, nil
		}
	default:
		return nil, fmt.Errorf("line %d: unexpected YAML node", n.Line)
	}
}

func TestJSONSchema(t *testing.T) {
	if _, err := compiledSchema(); err != nil {
		t.Fatalf("the contract JSON Schema doesn't compile: %v", err)
	}
	var head struct {
		Schema string `json:"$schema"`
		ID     string `json:"$id"`
	}
	if err := json.Unmarshal(JSONSchema(), &head); err != nil {
		t.Fatal(err)
	}
	if head.Schema != "https://json-schema.org/draft/2020-12/schema" || head.ID != JSONSchemaID {
		t.Errorf("$schema = %q, $id = %q; want Draft 2020-12 and %q", head.Schema, head.ID, JSONSchemaID)
	}
	if !strings.Contains(JSONSchemaID, "/"+strings.TrimPrefix(APIVersion, "0cular/")+".schema.json") {
		t.Errorf("JSONSchemaID %q doesn't name the apiVersion %s", JSONSchemaID, APIVersion)
	}
	b := JSONSchema()
	b[0] = 'x'
	if JSONSchema()[0] == 'x' {
		t.Error("JSONSchema() returns the embedded bytes, not a copy")
	}
}

// TestJSONSchemaAcceptsValidContracts checks that the schema accepts the
// contracts Parse accepts. The round-trip property test and the fuzz target
// check the same for generated contracts.
func TestJSONSchemaAcceptsValidContracts(t *testing.T) {
	sources := map[string]string{"full contract": fullContract, "minimal contract": header}
	files, err := filepath.Glob("../replay/*/contract.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		sources[file] = string(data)
	}
	for name, src := range sources {
		if _, err := Parse([]byte(src)); err != nil {
			t.Fatalf("%s: Parse() error: %v", name, err)
		}
		if err := validateSchema(t, src); err != nil {
			t.Errorf("%s: the JSON Schema rejects a valid contract: %v", name, err)
		}
	}
}

// TestJSONSchemaAgreesWithParse checks that the schema rejects each invalid
// contract in problemCases, except where a case says why it can't. Cases
// whose YAML has no JSON form, such as an anchor or a duplicate key, are
// for Parse alone.
func TestJSONSchemaAgreesWithParse(t *testing.T) {
	for _, tt := range problemCases {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSchema(t, tt.src)
			switch {
			case errors.Is(err, errNoJSON):
				t.Skipf("only Parse can see this: %v", err)
			case tt.schemaMisses == "" && err == nil:
				t.Errorf("the JSON Schema accepts a contract that Parse rejects with:\n%s", tt.want)
			case tt.schemaMisses != "" && err != nil:
				t.Errorf("the JSON Schema now rejects this contract, so remove schemaMisses (%q): %v", tt.schemaMisses, err)
			}
		})
	}
}
