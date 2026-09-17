// Command metaso-p2p-metatask-indexer runs the metatask replay indexer over
// the seven protocol paths and emits the frozen projections:
//
//	go run ./cmd/metaso-p2p-metatask-indexer --manapi https://manapi.metaid.io --out data/metatask-projections
//	go run ./cmd/metaso-p2p-metatask-indexer --snapshot <frozen-dir> --out data/metatask-projections
//	go run ./cmd/metaso-p2p-metatask-indexer --snapshot <frozen-dir> --root <pinId>
//
// Serve mode exposes the four frozen §7 endpoints over HTTP with periodic
// collection refreshes:
//
//	go run ./cmd/metaso-p2p-metatask-indexer --manapi https://manapi.metaid.io --serve --listen 127.0.0.1:8088 \
//	  --refresh-seconds 30 --chain-rpc-host <host:port> --chain-rpc-user ... --chain-rpc-pass ...
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
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/chain/mvc"
	"github.com/metaid-developers/metaso-p2p/internal/config"
	"github.com/metaid-developers/metaso-p2p/internal/metatask"
)

// version is stamped at release build time (-X main.version=${VERSION}) and
// reported in the X-Metatask-Indexer-Build response header only — never in a
// projection body (D7/red line: deployment stamps stay out of contract bodies).
var version = "dev"

func main() {
	var (
		snapshot        = flag.String("snapshot", "", "frozen crawl directory (pages/ + content/); deterministic replay of a recorded enumeration")
		manapiURL       = flag.String("manapi", "", "indexer base URL for a live enumeration (e.g. https://manapi.metaid.io)")
		size            = flag.Int("size", 50, "page size for the path walks")
		toBlock         = flag.Int64("to-block", 0, "evaluation boundary block height (0 = latest collected)")
		root            = flag.String("root", "", "restrict to one task root pinId (default: all)")
		out             = flag.String("out", "", "output directory for projection JSON files (default: task index to stdout)")
		allowIncomplete = flag.Bool("allow-incomplete", false, "proceed when a path walk did not reach an explicit empty page (coverage flags partial)")
		serve           = flag.Bool("serve", false, "run as an HTTP service exposing the four §7 endpoints")
		listen          = flag.String("listen", "127.0.0.1:8088", "listen address in --serve mode")
		refreshSeconds  = flag.Int("refresh-seconds", 30, "collection refresh cadence in --serve mode (freshness SLO: keep head−evaluated ≤ maxLagBlocks)")
		chainRPCHost    = flag.String("chain-rpc-host", "", "MVC RPC host for the X-Metatask-Chain-Head freshness header (optional; without it the header falls back to the evaluated block)")
		chainRPCUser    = flag.String("chain-rpc-user", "", "MVC RPC user")
		chainRPCPass    = flag.String("chain-rpc-pass", "", "MVC RPC password")
	)
	flag.Parse()
	if *snapshot == "" && *manapiURL == "" {
		exitJSON(1, map[string]any{"error": "one of --snapshot / --manapi is required"})
	}
	if *serve {
		serveMode(*manapiURL, *snapshot, *size, *listen, *refreshSeconds, *chainRPCHost, *chainRPCUser, *chainRPCPass)
		return
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

	boundary := metatask.DeriveBoundary(events, *toBlock)
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

// serveMode runs the HTTP service: one initial refresh, then periodic
// re-collection so the freshness SLO (chain head − evaluated block ≤
// maxLagBlocks) holds between cycles. Without --chain-rpc-host the
// chain-head header falls back to the evaluated block and the process says
// so loudly — C6 must be judged against a real chain source.
func serveMode(manapiURL, snapshotDir string, size int, listen string, refreshSeconds int, chainHost, chainUser, chainPass string) {
	ctx := context.Background()
	srv := &metatask.Server{BaseURL: manapiURL, Size: size, Version: version}
	if snapshotDir != "" {
		snap := metatask.NewSnapshotLister(snapshotDir)
		srv.Lister, srv.Payloads = snap, snap
	}
	if chainHost != "" {
		chain := mvc.NewChain(config.ChainRPCConfig{
			RPCHost: chainHost, RPCUser: chainUser, RPCPass: chainPass, RPCHTTPPostMode: true,
		})
		if err := chain.Init(); err != nil {
			exitJSON(1, map[string]any{"error": err.Error()})
		}
		srv.ChainHead = chain.GetBestHeight
	} else {
		fmt.Println("[metatask-indexer] WARNING: no --chain-rpc-host; X-Metatask-Chain-Head falls back to the evaluated block")
	}
	if err := srv.Refresh(ctx); err != nil {
		exitJSON(1, map[string]any{"error": "initial refresh: " + err.Error()})
	}
	go func() {
		t := time.NewTicker(time.Duration(refreshSeconds) * time.Second)
		defer t.Stop()
		for range t.C {
			if err := srv.Refresh(ctx); err != nil {
				fmt.Println("[metatask-indexer] refresh error:", err)
			}
		}
	}()
	fmt.Printf("[metatask-indexer] serving on %s (version %s, refresh %ds)\n", listen, version, refreshSeconds)
	if err := http.ListenAndServe(listen, srv.Handler()); err != nil {
		exitJSON(1, map[string]any{"error": err.Error()})
	}
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
