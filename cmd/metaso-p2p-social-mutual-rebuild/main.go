package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/social"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// Rebuilds the social edge:mutual reverse index from the stored edge:pair
// entries. Run once while the service is stopped after upgrading to a build
// that maintains the index incrementally (spec 2026-10-29 §4.1).
func main() {
	dataDir := flag.String("data-dir", "", "Pebble data directory (the parent containing the social namespace)")
	flag.Parse()
	if strings.TrimSpace(*dataDir) == "" {
		log.Fatal("--data-dir is required")
	}
	if err := run(*dataDir); err != nil {
		log.Fatalf("social mutual index rebuild: %v", err)
	}
}

func run(dataDir string) error {
	store := storage.NewPebbleStore(dataDir)
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("WARNING: close pebble store: %v", err)
		}
	}()
	agg := &social.Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		return fmt.Errorf("init social aggregator: %w", err)
	}
	written, err := agg.RebuildMutualIndex()
	if err != nil {
		return err
	}
	fmt.Printf("social mutual index rebuild complete: %d mutual pairs\n", written)
	return nil
}
