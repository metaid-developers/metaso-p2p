// Command metaso-p2p-height-reset sets one chain's persisted scan height
// (Pebble namespace "indexer_meta", key "<chain>_lastheight") so the next
// daemon start rescans blocks from height+1. Pin ingestion and transfer
// detection are idempotent by design, so a targeted rescan repairs blocks
// whose spend detection missed (e.g. the zero-conf overlay gap fixed in the
// same release).
//
// Run with the metaso-p2p daemon STOPPED (same Pebble namespace), as the
// service user. --get prints the stored height without changing anything.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/metaid-developers/metaso-p2p/internal/config"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("height-reset: %v", err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("metaso-p2p-height-reset", flag.ContinueOnError)
	dataDir := fs.String("data-dir", cfg.Pebble.DataDir, "Pebble data directory of the metaso-p2p daemon")
	chain := fs.String("chain", "mvc", "chain name (the indexer_meta key is <chain>_lastheight)")
	height := fs.Int64("height", -1, "height to store — the daemon rescans from height+1 (required without --get)")
	get := fs.Bool("get", false, "print the stored height and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	store := storage.NewPebbleStore(*dataDir)
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("WARNING: close pebble store: %v", err)
		}
	}()

	key := []byte(*chain + "_lastheight")
	if *get {
		raw, err := store.Get("indexer_meta", key)
		if err != nil || raw == nil {
			fmt.Println("unset")
			return nil
		}
		if _, err := strconv.ParseInt(string(raw), 10, 64); err != nil {
			return fmt.Errorf("stored height %q is not numeric", string(raw))
		}
		fmt.Println(string(raw))
		return nil
	}
	if *height < 0 {
		return fmt.Errorf("--height is required (the last PROCESSED height to keep; the daemon rescans from height+1)")
	}
	if err := store.Set("indexer_meta", key, []byte(strconv.FormatInt(*height, 10))); err != nil {
		return err
	}
	log.Printf("stored %s_lastheight=%d — start the daemon to rescan blocks %d..tip", *chain, *height, *height+1)
	return nil
}
