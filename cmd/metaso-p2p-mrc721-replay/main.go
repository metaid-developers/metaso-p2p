// Command metaso-p2p-mrc721-replay re-ingests confirmed MRC-721 pins by id
// through the live block-ingest path. Its purpose is the R-F-11 repair
// replay: item_desc pins that a previous (strict-metadata) binary dropped
// whole — first-write-wins ingest keeps re-application safe, and refs that
// never wrote any field backfill now under the tolerant decode.
//
// The pin is re-parsed from the MVC node with the same confirmed CatchPins
// path the engine uses (ledger pin ids, TxIndex included), so the replayed
// facts are identical to a fresh block scan.
//
// Run with the metaso-p2p daemon STOPPED (same Pebble namespace), as the
// service user.
package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/mrc721nft"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/chain/mvc"
	"github.com/metaid-developers/metaso-p2p/internal/config"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("mrc721-replay: %v", err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("metaso-p2p-mrc721-replay", flag.ContinueOnError)
	dataDir := fs.String("data-dir", cfg.Pebble.DataDir, "Pebble data directory of the metaso-p2p daemon")
	pinIds := fs.String("pinIds", "", "comma-separated pin ids to replay (confirmed pins only)")
	if err := fs.Parse(flag.Args()); err != nil {
		return err
	}
	var ids []string
	for _, id := range strings.Split(*pinIds, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("--pinIds is required")
	}

	chain := mvc.NewChain(cfg.BlockIndex.MVC)
	if err := chain.Init(); err != nil {
		return fmt.Errorf("mvc rpc connect: %w", err)
	}
	indexer := mvc.NewIndexer(chain, mvc.NetParams(""))

	store := storage.NewPebbleStore(*dataDir)
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("WARNING: close pebble store: %v", err)
		}
	}()
	agg := &mrc721nft.Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		return err
	}

	blockCache := make(map[int64][]pinMatch)
	replayed := 0
	for _, id := range ids {
		height, err := pinHeight(chain, id)
		if err != nil {
			log.Printf("SKIP %s: %v", id, err)
			continue
		}
		pins, ok := blockCache[height]
		if !ok {
			raw, _, err := indexer.CatchPins(height)
			if err != nil {
				log.Printf("SKIP %s: block %d parse error: %v", id, height, err)
				continue
			}
			for _, pin := range raw {
				if pin != nil {
					pins = append(pins, pinMatch{id: pin.Id, pin: pin})
				}
			}
			blockCache[height] = pins
		}
		var match *pinMatch
		for i := range blockCache[height] {
			if blockCache[height][i].id == id {
				match = &blockCache[height][i]
				break
			}
		}
		if match == nil {
			log.Printf("SKIP %s: not a parsed pin of block %d (witness-format MVC pins stay out of the aggregate by design)", id, height)
			continue
		}
		if err := agg.IngestConfirmedPin(match.pin); err != nil {
			log.Printf("FAIL %s: %v", id, err)
			continue
		}
		replayed++
		log.Printf("REPLAYED %s (block %d)", id, height)
	}
	log.Printf("done: %d/%d replayed", replayed, len(ids))
	return nil
}

// pinMatch pairs a parsed pin with its id for the per-height cache.
type pinMatch struct {
	id  string
	pin *aggregator.PinInscription
}

// pinHeight resolves the confirmation height of a pin's transaction; the
// pinId's leading 64 hex chars are the ledger txid (the same convention the
// MVC adapter derives pin ids from).
func pinHeight(chain *mvc.Chain, pinId string) (int64, error) {
	if len(pinId) < 65 || pinId[64] != 'i' {
		return 0, fmt.Errorf("pin id is not a <64hex>i<n> shape")
	}
	txid := pinId[:64]
	confirmed, _, _, height, err := chain.GetTransactionStatus(txid)
	if err != nil {
		return 0, fmt.Errorf("tx status %s: %w", txid, err)
	}
	if !confirmed {
		return 0, fmt.Errorf("tx %s is not confirmed yet — nothing to replay", txid)
	}
	return height, nil
}
