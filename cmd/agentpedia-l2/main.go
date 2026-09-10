// Command agentpedia-l2 serves MetaSo's Agentpedia L2 aggregation API:
//
//	GET /api/agentpedia/entry?lang=&slug=      entry detail (head/status/disputed/history/backlinks/editorBreakdown)
//	GET /api/agentpedia/entry_list?lang=&sort= paginated entry list (cursor = next offset)
//	GET /api/agentpedia/backlinks?lang=&slug=  inbound wikilinks
//	GET /api/agentpedia/sync                   manapi incremental pull; returns the per-path lastCursor map
//	GET /healthz
//
// Read-only by design (human reader contract): this service never writes pins.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/metaid-developers/metaso-p2p/internal/agentpedia"
)

func main() {
	addr := flag.String("addr", ":8088", "listen address")
	manapi := flag.String("manapi", "https://manapi.metaid.io", "manapi base URL")
	seedFile := flag.String("seed", "", "optional JSON file with a demo/fixture event stream (array of events)")
	flag.Parse()

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

	log.Printf("agentpedia L2 listening on %s (manapi %s)", *addr, *manapi)
	if err := http.ListenAndServe(*addr, l2.ServeMux()); err != nil {
		log.Fatal(err)
	}
}
