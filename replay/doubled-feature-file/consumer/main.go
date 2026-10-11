// Command consumer imitates a proxy module that preallocates room for 200
// features and copies the feature file into it without checking the count.
//
// It's a deliberately fragile stub for the doubled-feature-file replay
// scenario: it loads the baseline and exits 0, and it panics with an index
// out of range on the candidate, whose duplicated rows make 240 entries.
//
// Usage:
//
//	consumer <feature-file>
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// maxFeatures is the consumer's hidden limit: the room it preallocates.
const maxFeatures = 200

type feature struct {
	Name   string  `json:"name"`
	Weight float64 `json:"weight"`
}

type featureFile struct {
	Model    string    `json:"model"`
	Features []feature `json:"features"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: consumer <feature-file>")
		os.Exit(64)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "consumer:", err)
		os.Exit(1)
	}
	var file featureFile
	if err := json.Unmarshal(data, &file); err != nil {
		fmt.Fprintln(os.Stderr, "consumer:", err)
		os.Exit(1)
	}

	// Like the real proxy, the stub preallocates for its limit and trusts the
	// file to stay within it. Entry 201 indexes past the end and panics.
	var slots [maxFeatures]feature
	//nolint:staticcheck // S1001: copy would stop at 200 entries; the stub must index past its limit like the proxy.
	for i, f := range file.Features {
		slots[i] = f
	}

	var total float64
	for _, f := range slots[:len(file.Features)] {
		total += f.Weight
	}
	fmt.Fprintf(os.Stdout, "loaded %d features for %s (total weight %.3f)\n", len(file.Features), file.Model, total)
}
