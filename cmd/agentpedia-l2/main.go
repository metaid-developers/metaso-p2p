// Command agentpedia-l2 serves MetaSo's Agentpedia L2 aggregation API:
//
//	GET /api/agentpedia/entry?lang=&slug=      entry detail (head/status/disputed/history/backlinks/editorBreakdown)
//	GET /api/agentpedia/entry_list?lang=&sort= paginated entry list (cursor = next offset)
//	GET /api/agentpedia/backlinks?lang=&slug=  inbound wikilinks
//	GET /api/agentpedia/sync                   manapi incremental pull; returns the per-path lastCursor map
//	GET /healthz                               ok + sync freshness (lastSyncAt/events/lastSync)
//
// The view is kept fresh by a self-driven sync loop (RunSyncLoop): one manapi
// sweep at startup, then one per -sync-interval (default 2m). The loop exists
// because relying on external GET /sync callers froze the production view for
// days while healthz stayed green.
//
// Read-only by design (human reader contract): this service never writes pins.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/agentpedia"
)

func main() {
	addr := flag.String("addr", ":8088", "listen address")
	manapi := flag.String("manapi", "https://manapi.metaid.io", "manapi base URL")
	seedFile := flag.String("seed", "", "optional JSON file with a demo/fixture event stream (array of events)")
	syncEvery := flag.Duration("sync-interval", 2*time.Minute,
		"self-driven sync loop period; <=0 disables the loop (env AGENTPEDIA_SYNC_INTERVAL overrides, Go duration syntax)")
	flag.Parse()
	if env := os.Getenv("AGENTPEDIA_SYNC_INTERVAL"); env != "" {
		parsed, err := time.ParseDuration(env)
		if err != nil {
			log.Fatalf("invalid AGENTPEDIA_SYNC_INTERVAL %q: %v", env, err)
		}
		*syncEvery = parsed
	}

	l2 := agentpedia.NewL2(*manapi)
	if *seedFile != "" {
		raw, err := os.ReadFile(*seedFile)
		if err != nil {
			log.Fatalf("read seed file: %v", err)
		}
		var events []agentpedia.Event
		if err := json.Unmarshal(raw, &events); err != nil {
			log.Fatalf("parse seed file: %v", err)
		}
		l2.Seed(events)
		log.Printf("seeded %d events from %s", len(events), *seedFile)
	}

	if *syncEvery > 0 {
		log.Printf("agentpedia periodic sync every %s (first round at startup)", *syncEvery)
		go l2.RunSyncLoop(context.Background(), *syncEvery)
	} else {
		log.Printf("agentpedia periodic sync disabled (sync-interval <= 0); view advances only via GET /api/agentpedia/sync")
	}

	log.Printf("agentpedia L2 listening on %s (manapi %s)", *addr, *manapi)
	if err := http.ListenAndServe(*addr, l2.Handler()); err != nil {
		log.Fatal(err)
	}
}
