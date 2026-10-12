// Package contract reads 0cular contracts: the rules a candidate version of
// an artifact must meet before it's published, written in YAML and reviewed
// like code.
//
// A minimal contract:
//
//	apiVersion: 0cular/v1alpha1
//	kind: Contract
//	metadata:
//	  name: bot-features
//	artifact:
//	  format: json
//	  maxBytes: 5MiB
//	invariants:
//	  - name: feature-cap
//	    expr: size(self.features) <= 200
//	    message: the proxy preallocates 200 features
//
// Loading is strict, so that a typo can't silently weaken a check (ADR-0005):
//
//   - An unknown field is an error, with the closest known field as a hint.
//     So is a duplicate key, a blank value, or a field from a later release
//     of 0cular, such as consumerTests.
//   - Scalars resolve under the YAML 1.2 core schema: only true and false
//     are booleans, so overridable: yes is an error rather than true.
//   - Anchors, aliases, merge keys and explicit tags aren't allowed.
//   - Names are 1 to 63 lowercase letters, digits and hyphens, and each
//     check's name is unique.
//   - Sizes use binary units: 5MiB, not 5MB.
//   - A diff budget bounds only change from the baseline; absolute limits
//     belong in an invariant.
//
// Parse and LoadFile report every problem in one *Error, each with its line,
// column and field path, in file order. LoadFile also reads the JSON Schema
// file a contract names. Neither compiles that schema or the CEL
// expressions; package eval does, for contract lint and the gate.
//
// JSONSchema returns a JSON Schema for contract files themselves, so that
// editors can check a contract as it's written; 0cular contract schema
// prints it. Parse remains the authority: it also checks what a JSON Schema
// can't express, such as unique check names.
package contract
