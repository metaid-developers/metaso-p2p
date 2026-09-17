// Command metaso-p2p-metatask-indexer runs the metatask replay indexer over
// the seven protocol paths and emits the frozen projections:
//
//	go run ./cmd/metaso-p2p-metatask-indexer --manapi https://manapi.metaid.io --out data/metatask-projections
//	go run ./cmd/metaso-p2p-metatask-indexer --snapshot <frozen-dir> --out data/metatask-projections
//	go run ./cmd/metaso-p2p-metatask-indexer --snapshot <frozen-dir> --root <pinId>
//
// Outputs (--out): taskIndex.json, panorama/<root>.json, replay/<root>.json
// (D7 canonical bytes — the fourth-party对数 dump), nodes/<root>/<node>.json.
// Without --out the task index goes to stdout.
//
// Exit codes: 0 ok; 1 fatal error; 3 incomplete enumeration (a path walk did
// not reach an explicit empty page; --allow-incomplete proceeds while
// coverage flags the partial paths in replayMeta — never silent).
//
// The command never writes on-chain.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/metatask"
)

func main() {
	var (
		snapshot        = flag.String("snapshot", "", "frozen crawl directory (pages/ + content/); deterministic replay of a recorded enumeration")
		manapiURL       = flag.String("manapi", "", "indexer base URL for a live enumeration (e.g. https://manapi.metaid.io)")
		size            = flag.Int("size", 50, "page size for the path walks")
		toBlock         = flag.Int64("to-block", 0, "evaluation boundary block height (0 = latest collected)")
		root            = flag.String("root", "", "restrict to one task root pinId (default: all)")
		out             = flag.String("out", "", "output directory for projection JSON files (default: task index to stdout)")
		allowIncomplete = flag.Bool("allow-incomplete", false, "proceed when a path walk did not reach an explicit empty page (coverage flags partial)")
	)
	flag.Parse()
	if *snapshot == "" && *manapiURL == "" {
		exitJSON(1, map[string]any{"error": "one of --snapshot / --manapi is required"})
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
		exitJSON(1, map[string]any{"error": err.Error(), "enumeration": enum})
	}
	for _, path := range metatask.EventPaths {
		metatask.LoadPayloads(ctx, events[path], payloads)
	}
	if !enum.Complete && !*allowIncomplete {
		exitJSON(3, map[string]any{"error": "enumeration incomplete; refusing to emit partial projections", "unresolved": enum.Unresolved})
	}

	boundary := deriveBoundary(events, *toBlock)
	scope := metatask.ScopeEvents(events, boundary)
	roots := scope.TaskRoots()
	if *root != "" {
		found := false
		for _, r := range roots {
			if r == *root {
				found = true
				break
			}
		}
		if !found {
			exitJSON(1, map[string]any{"error": fmt.Sprintf("task root %s not found among admitted events", *root)})
		}
		roots = []string{*root}
	}

	results := map[string]*metatask.ReplayResult{}
	for _, r := range roots {
		res, err := metatask.Replay(events, r, boundary)
		if err != nil {
			exitJSON(1, map[string]any{"error": err.Error(), "root": r})
		}
		results[r] = res
	}

	meta := metatask.BuildReplayMeta(scope, enum, boundary)
	index := metatask.BuildTaskIndex(results, scope, meta)

	if *out == "" {
		blob, err := json.MarshalIndent(index, "", "  ")
		if err != nil {
			exitJSON(1, map[string]any{"error": err.Error()})
		}
		fmt.Println(string(blob))
		return
	}

	if err := writeProjections(*out, scope, meta, index, roots, results); err != nil {
		exitJSON(1, map[string]any{"error": err.Error()})
	}
	fmt.Printf("metatask indexer: boundary block %d (ts %d), events %d (mempool excluded %d, payload rejected %d), tasks %d\n",
		boundary.Height, boundary.BlockTs, meta.EventCount, scope.Mempool, scope.Rejected, len(results))
	fmt.Printf("wrote %s/{taskIndex.json, panorama/, replay/, nodes/}\n", *out)
	if !enum.Complete {
		fmt.Printf("WARNING: enumeration incomplete for %v; coverage.partial is flagged in replayMeta\n", enum.Unresolved)
		os.Exit(3)
	}
}

// deriveBoundary picks B: an explicit --to-block, or the newest confirmed
// event height with that height's newest block timestamp (the list API
// carries a per-pin block time; an exact chain-RPC block timestamp is a
// server-mode concern beyond this skeleton).
func deriveBoundary(events map[string][]*metatask.Pin, toBlock int64) metatask.Boundary {
	height := int64(-1)
	ts := int64(0)
	for _, path := range metatask.EventPaths {
		for _, p := range events[path] {
			if p.GenesisHeight < 0 {
				continue
			}
			if toBlock > 0 && p.GenesisHeight > toBlock {
				continue
			}
			switch {
			case p.GenesisHeight > height:
				height, ts = p.GenesisHeight, p.Timestamp
			case p.GenesisHeight == height && p.Timestamp > ts:
				ts = p.Timestamp
			}
		}
	}
	if height < 0 {
		height = 0
	}
	return metatask.Boundary{Height: height, BlockTs: ts}
}

func writeProjections(out string, scope *metatask.RunScope, meta metatask.ReplayMeta, index metatask.TaskIndex, roots []string, results map[string]*metatask.ReplayResult) error {
	for _, dir := range []string{"panorama", "replay"} {
		if err := os.MkdirAll(filepath.Join(out, dir), 0o755); err != nil {
			return err
		}
	}
	blob, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "taskIndex.json"), append(blob, '\n'), 0o644); err != nil {
		return err
	}
	for _, root := range roots {
		res := results[root]
		pano := metatask.BuildPanorama(res, scope, meta)
		pb, err := json.MarshalIndent(pano, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, "panorama", root+".json"), append(pb, '\n'), 0o644); err != nil {
			return err
		}
		rb, err := metatask.CanonicalJSON(res)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, "replay", root+".json"), rb, 0o644); err != nil {
			return err
		}
		nodesDir := filepath.Join(out, "nodes", root)
		if err := os.MkdirAll(nodesDir, 0o755); err != nil {
			return err
		}
		for _, nodeID := range res.NodeOrder {
			ne, err := metatask.BuildNodeEvents(res, nodeID)
			if err != nil {
				return err
			}
			nb, err := json.MarshalIndent(ne, "", "  ")
			if err != nil {
				return err
			}
			name := strings.ReplaceAll(nodeID, string(os.PathSeparator), "_")
			if err := os.WriteFile(filepath.Join(nodesDir, name+".json"), append(nb, '\n'), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func exitJSON(code int, v map[string]any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
	os.Exit(code)
}
