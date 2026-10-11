// Command consumer imitates a quota service that loads a policy file and
// derives each policy's per-second rate, reading the limit without checking
// for null.
//
// It's a deliberately fragile stub for the blank-policy-fields replay
// scenario: it loads the baseline and exits 0, and on the candidate, where a
// new policy's limit is blank, it dereferences a nil pointer and crashes.
//
// Usage:
//
//	consumer <policy-file>
package main

import (
	"fmt"
	"os"
	"time"

	"go.yaml.in/yaml/v3"
)

type policy struct {
	ID       string `yaml:"id"`
	Resource string `yaml:"resource"`
	Limit    *int64 `yaml:"limit"`
	Period   string `yaml:"period"`
}

type policyFile struct {
	Service  string   `yaml:"service"`
	Policies []policy `yaml:"policies"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: consumer <policy-file>")
		os.Exit(64)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "consumer:", err)
		os.Exit(1)
	}
	var file policyFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		fmt.Fprintln(os.Stderr, "consumer:", err)
		os.Exit(1)
	}

	for _, p := range file.Policies {
		period, err := time.ParseDuration(p.Period)
		if err != nil {
			fmt.Fprintf(os.Stderr, "consumer: policy %s: %v\n", p.ID, err)
			os.Exit(1)
		}
		// Like the code path that crashed Service Control, this one trusts
		// every policy to carry a limit. A blank limit decodes to nil.
		rate := float64(*p.Limit) / period.Seconds()
		fmt.Fprintf(os.Stdout, "%s: %.2f %s per second\n", p.ID, rate, p.Resource)
	}
	fmt.Fprintf(os.Stdout, "loaded %d policies for %s\n", len(file.Policies), file.Service)
}
