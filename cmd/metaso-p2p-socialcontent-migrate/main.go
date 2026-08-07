package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/socialcontent"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	keyPostRecord    = "post:record:"
	keyPostTime      = "post:time:"
	keyPostAuthor    = "post:author:"
	keyCommentRecord = "comment:record:"
	keyCommentTarget = "comment:target:"
)

func main() {
	dataDir := flag.String("data-dir", "", "Pebble data directory (the parent containing the socialcontent namespace)")
	dryRun := flag.Bool("dry-run", false, "print planned rewrites without writing")
	flag.Parse()
	if strings.TrimSpace(*dataDir) == "" {
		log.Fatal("--data-dir is required")
	}
	if err := run(*dataDir, *dryRun); err != nil {
		log.Fatalf("socialcontent migrate: %v", err)
	}
}

func run(dataDir string, dryRun bool) error {
	store := storage.NewPebbleStore(dataDir)
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("WARNING: close pebble store: %v", err)
		}
	}()

	// This migration rewrites timestamp-bearing index keys from the old
	// base64 inverted encoding to an order-preserving hex encoding. It must
	// run while the service is stopped.
	var total int
	var deletes [][]byte
	var sets []storage.KeyValue
	flush := func() error {
		if len(deletes) == 0 && len(sets) == 0 {
			return nil
		}
		if dryRun {
			fmt.Printf("dry-run: would apply %d deletes and %d sets\n", len(deletes), len(sets))
			deletes = deletes[:0]
			sets = sets[:0]
			return nil
		}
		if err := store.DeleteBatch(socialcontent.Namespace, deletes); err != nil {
			return err
		}
		if err := store.SetBatch(socialcontent.Namespace, sets); err != nil {
			return err
		}
		deletes = deletes[:0]
		sets = sets[:0]
		return nil
	}

	if err := store.ScanPrefix(socialcontent.Namespace, []byte(keyPostRecord), func(key, _ []byte) error {
		total++
		var record socialcontent.PostRecord
		if err := loadJSON(store, key, &record); err != nil {
			return err
		}
		if record.SourcePinId == "" || record.ChainName == "" {
			return nil
		}
		chain := strings.ToLower(record.ChainName)
		// Remove the old-encoded time and author index keys.
		deletes = append(deletes, oldPostTimeKey(record.CreatedAt, chain, record.SourcePinId))
		for _, identity := range []string{record.AuthorGlobalMetaId, record.AuthorMetaId, record.AuthorAddress} {
			if identity != "" {
				deletes = append(deletes, oldPostAuthorKey(identity, record.CreatedAt, chain, record.SourcePinId))
			}
		}
		// Write the new-encoded index keys for visible posts.
		if !record.Hidden {
			sets = append(sets, storage.KeyValue{Key: newPostTimeKey(record.CreatedAt, chain, record.SourcePinId), Value: []byte(record.SourcePinId)})
			for _, identity := range []string{record.AuthorGlobalMetaId, record.AuthorMetaId, record.AuthorAddress} {
				if identity != "" {
					sets = append(sets, storage.KeyValue{Key: newPostAuthorKey(identity, record.CreatedAt, chain, record.SourcePinId), Value: []byte(record.SourcePinId)})
				}
			}
		}
		if len(deletes) >= 20000 || len(sets) >= 20000 {
			if err := flush(); err != nil {
				return err
			}
			fmt.Printf("posts migrated: %d\n", total)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	fmt.Printf("post index migration complete: %d records\n", total)

	commentTotal := 0
	if err := store.ScanPrefix(socialcontent.Namespace, []byte(keyCommentRecord), func(key, _ []byte) error {
		commentTotal++
		var comment socialcontent.CommentRecord
		if err := loadJSON(store, key, &comment); err != nil {
			return err
		}
		if comment.PinId == "" || comment.TargetPinId == "" || comment.ChainName == "" {
			return nil
		}
		chain := strings.ToLower(comment.ChainName)
		deletes = append(deletes, oldCommentTargetKey(chain, comment.TargetPinId, comment.Timestamp, comment.PinId))
		sets = append(sets, storage.KeyValue{Key: newCommentTargetKey(chain, comment.TargetPinId, comment.Timestamp, comment.PinId), Value: []byte(comment.PinId)})
		if len(deletes) >= 20000 || len(sets) >= 20000 {
			if err := flush(); err != nil {
				return err
			}
			fmt.Printf("comments migrated: %d\n", commentTotal)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	fmt.Printf("comment index migration complete: %d records\n", commentTotal)
	return nil
}

func loadJSON(store *storage.PebbleStore, key []byte, out any) error {
	raw, err := store.Get(socialcontent.Namespace, key)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Old encoding helpers reproduce the previous base64 inverted timestamp so
// stale keys can be deleted after the encoding change.
func oldInvertedTimestamp(ts int64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, ^uint64(ts))
	return base64.RawURLEncoding.EncodeToString(buf)
}

func oldPostTimeKey(ts int64, chain, source string) []byte {
	return []byte(keyPostTime + oldInvertedTimestamp(ts) + ":" + chain + ":" + source)
}

func oldPostAuthorKey(identity string, ts int64, chain, source string) []byte {
	return []byte(keyPostAuthor + strings.ToLower(identity) + ":" + oldInvertedTimestamp(ts) + ":" + chain + ":" + source)
}

func oldCommentTargetKey(chain, target string, ts int64, pinID string) []byte {
	return []byte(keyCommentTarget + chain + ":" + target + ":" + oldInvertedTimestamp(ts) + ":" + pinID)
}

// New encoding helpers use the order-preserving hex inverted timestamp that
// the socialcontent package now uses for timestamp-bearing index keys.
func newInvertedTimestamp(ts int64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, ^uint64(ts))
	return hex.EncodeToString(buf)
}

func newPostTimeKey(ts int64, chain, source string) []byte {
	return []byte(keyPostTime + newInvertedTimestamp(ts) + ":" + chain + ":" + source)
}

func newPostAuthorKey(identity string, ts int64, chain, source string) []byte {
	return []byte(keyPostAuthor + strings.ToLower(identity) + ":" + newInvertedTimestamp(ts) + ":" + chain + ":" + source)
}

func newCommentTargetKey(chain, target string, ts int64, pinID string) []byte {
	return []byte(keyCommentTarget + chain + ":" + target + ":" + newInvertedTimestamp(ts) + ":" + pinID)
}
