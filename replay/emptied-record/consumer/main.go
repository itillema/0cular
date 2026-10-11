// Command consumer imitates a client that looks up an endpoint's DNS record
// and picks an address to connect to.
//
// It's a deliberately fragile stub for the emptied-record replay scenario: it
// resolves the baseline and exits 0, and it fails on the candidate, whose
// address list is empty, the way clients failed to reach the real endpoint.
//
// Usage:
//
//	consumer <record-file>
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type record struct {
	Endpoint  string   `json:"endpoint"`
	Plan      int      `json:"plan"`
	TTL       int      `json:"ttl"`
	Addresses []string `json:"addresses"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: consumer <record-file>")
		os.Exit(64)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "consumer:", err)
		os.Exit(1)
	}
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		fmt.Fprintln(os.Stderr, "consumer:", err)
		os.Exit(1)
	}

	// With no addresses there's nothing to connect to: every client of the
	// endpoint fails the same way.
	if len(rec.Addresses) == 0 {
		fmt.Fprintf(os.Stderr, "consumer: no addresses for %s\n", rec.Endpoint)
		os.Exit(1)
	}

	// The client connects to the first address the record lists.
	fmt.Fprintf(os.Stdout, "%s resolves to %s (plan %d, %d addresses)\n", rec.Endpoint, rec.Addresses[0], rec.Plan, len(rec.Addresses))
}
