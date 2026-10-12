package contract

import (
	"bytes"
	_ "embed" // for the contract file JSON Schema
)

// JSONSchemaID is the $id of the JSON Schema for contract files: the URL of
// the schema file on the main branch, which editors can fetch.
const JSONSchemaID = "https://raw.githubusercontent.com/itillema/0cular/main/contract/v1alpha1.schema.json"

//go:embed v1alpha1.schema.json
var jsonSchema []byte

// JSONSchema returns the JSON Schema (Draft 2020-12) for contract files, so
// that editors and other tools can check a contract as it's written.
//
// Parse is stricter than the schema. It also rejects what a JSON Schema
// can't express, or can't see once YAML becomes JSON: duplicate keys, YAML
// anchors, aliases and tags, numbers JSON can't hold such as .inf, check
// names used twice, and sizes too large for 64 bits.
func JSONSchema() []byte {
	return bytes.Clone(jsonSchema)
}
