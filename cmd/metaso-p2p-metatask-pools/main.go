// Command metaso-p2p-metatask-pools prints MetaSo's metaTask pool aggregation
// for one task root: the claim/submission/verify pools settled with the
// metatask-replay v1.2.0 semantics, the participating bots and vote counts, the
// full cursor-exhausted enumeration report, and fee/timestamp metadata.
//
//	go run ./cmd/metaso-p2p-metatask-pools --manapi https://manapi.metaid.io --root <pinId>
//	go run ./cmd/metaso-p2p-metatask-pools --snapshot <frozen-dir> --root <pinId>
//
// The command never writes on-chain.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/metatask"
)

func main() {
	var (
		snapshot   = flag.String("snapshot", "", "frozen crawl directory (pages/ + content/); deterministic replay of a recorded enumeration")
		manapiURL  = flag.String("manapi", "", "indexer base URL for a live enumeration (e.g. https://manapi.metaid.io)")
		root       = flag.String("root", "", "task root pinId to aggregate (required)")
		size       = flag.Int("size", 50, "page size for the pool walk")
		now        = flag.Int64("now", 0, "clock for the §4.7 guard view (unix seconds; default: wall clock)")
		out        = flag.String("out", "", "write the JSON report to this path (default stdout)")
		allowShort = flag.Bool("allow-incomplete", false, "print the report even when the enumeration did not reach an explicit empty page")
	)
	flag.Parse()
	if *root == "" {
		exitJSON(map[string]any{"error": "--root is required"})
	}
	if *snapshot == "" && *manapiURL == "" {
		exitJSON(map[string]any{"error": "one of --snapshot / --manapi is required"})
	}

	ctx := context.Background()
	var lister metatask.Lister
	var payloads metatask.PayloadSource

	switch {
	case *snapshot != "":
		snap := metatask.NewSnapshotLister(*snapshot)
		lister, payloads = snap, snap
	default:
		http := metatask.NewHTTPLister(*manapiURL)
		lister, payloads = http, http
	}

	events, enum, err := metatask.EnumerateAll(ctx, lister, metatask.EventPaths, *size)
	if err != nil {
		exitJSON(map[string]any{"error": err.Error(), "enumeration": enum})
	}
	for _, path := range metatask.EventPaths {
		metatask.LoadPayloads(ctx, events[path], payloads)
	}

	report, err := metatask.BuildAggregate(events, metatask.Options{
		Root:        *root,
		Now:         *now,
		GeneratedAt: time.Now(),
		Enumeration: enum,
	})
	if err != nil {
		exitJSON(map[string]any{"error": err.Error(), "enumeration": enum})
	}

	blob, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		exitJSON(map[string]any{"error": err.Error()})
	}
	if *out != "" {
		if err := os.WriteFile(*out, append(blob, '\n'), 0o644); err != nil {
			exitJSON(map[string]any{"error": err.Error()})
		}
	} else {
		fmt.Println(string(blob))
	}

	if !enum.Complete {
		fmt.Fprintf(os.Stderr, "INCOMPLETE ENUMERATION: %v\n", enum.Unresolved)
		for path, rep := range enum.Paths {
			if !rep.Complete {
				fmt.Fprintf(os.Stderr, "  %s: termination=%s pages=%d records=%d\n", path, rep.Termination, rep.Pages, rep.Records)
			}
		}
		if !*allowShort {
			fmt.Fprintln(os.Stderr, "refusing to treat a short delivery as a complete pool (charter §〇); re-run with --allow-incomplete to accept the partial view")
			os.Exit(2)
		}
	}
}

func exitJSON(v map[string]any) {
	blob, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(blob))
	os.Exit(1)
}
