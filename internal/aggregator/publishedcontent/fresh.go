package publishedcontent

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cockroachdb/pebble"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

// The fresh read model backs GET /api/metaweb/fresh: a newest-first,
// cross-protocol scan over the fresh-time index with inclusive `since` and a
// key-pinned cursor, so paging over the same window is gap-free and
// duplicate-free under concurrent inserts. See
// docs/specs/2026-09-13-metaweb-surf-reads-api.md §1.

const (
	defaultFreshSize = 50
	maxFreshSize     = 100
)

// ErrInvalidFreshCursor marks a cursor the fresh scan cannot decode; the
// HTTP layer maps it to 40000.
var ErrInvalidFreshCursor = errors.New("invalid cursor")

// FreshParams bounds one fresh-feed page.
type FreshParams struct {
	// ProtocolPaths restricts the scan; nil/empty means every published
	// protocol. Paths must be normalised protocol paths
	// (protocolPathFromPinPath shape).
	ProtocolPaths []string
	// SinceSec is inclusive: records with createdAt seconds >= SinceSec.
	// Zero disables the bound.
	SinceSec int64
	// Size is the page size, 1..maxFreshSize (default defaultFreshSize).
	Size int
	// Cursor is the opaque continuation returned by the previous page.
	Cursor string
}

// FreshPage is one fresh-feed page.
type FreshPage struct {
	Records    []*Record
	NextCursor string
	HasMore    bool
}

// Fresh returns the page of visible records ordered
// (createdAt DESC, sourcePinId DESC). The cursor pins the exact index key of
// the last returned record, so records inserted while paging never shift the
// window.
func (a *Aggregator) Fresh(params FreshParams) (*FreshPage, error) {
	if a == nil || a.store == nil {
		return nil, errors.New("publishedcontent store unavailable")
	}
	size := params.Size
	if size <= 0 {
		size = defaultFreshSize
	}
	if size > maxFreshSize {
		size = maxFreshSize
	}

	paths := make(map[string]struct{}, len(params.ProtocolPaths))
	for _, path := range params.ProtocolPaths {
		path = strings.ToLower(protocolPathFromPinPath(path))
		if path == "" {
			continue
		}
		if !isPublishedProtocol(path) {
			return nil, fmt.Errorf("unsupported protocol path: %s", path)
		}
		paths[path] = struct{}{}
	}

	after, err := decodeFreshCursor(params.Cursor)
	if err != nil {
		return nil, err
	}

	limit := size + 1
	page := &FreshPage{}
	keys := make([][]byte, 0, limit)
	err = a.store.ScanPrefixAfter(Namespace, []byte(keyFreshTime), after, func(key, _ []byte) error {
		tsSec, chainName, protocolPath, sourcePinId, ok := parseFreshTimeKey(key)
		if !ok {
			return nil
		}
		if len(paths) > 0 {
			if _, allowed := paths[protocolPath]; !allowed {
				return nil
			}
		}
		// Index keys descend by createdAt seconds; once below the inclusive
		// bound nothing further can match.
		if params.SinceSec > 0 && tsSec < params.SinceSec {
			return errStopScan
		}
		rec, err := a.loadRecord(chainName, protocolPath, sourcePinId)
		if err != nil {
			return err
		}
		if rec == nil || rec.Hidden || rec.Operation == OperationRevoke {
			return nil
		}
		page.Records = append(page.Records, rec)
		keys = append(keys, append([]byte(nil), key...))
		if len(page.Records) >= limit {
			return errStopScan
		}
		return nil
	})
	if err != nil && err != errStopScan {
		return nil, err
	}

	page.HasMore = len(page.Records) > size
	if page.HasMore {
		page.Records = page.Records[:size]
		keys = keys[:size]
	}
	if page.HasMore && len(keys) > 0 {
		page.NextCursor = encodeFreshCursor(keys[len(keys)-1])
	}
	return page, nil
}

func encodeFreshCursor(key []byte) string {
	if len(key) == 0 {
		return ""
	}
	return "k:" + base64.RawURLEncoding.EncodeToString(key)
}

func decodeFreshCursor(cursor string) ([]byte, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return nil, nil
	}
	if !strings.HasPrefix(cursor, "k:") {
		return nil, ErrInvalidFreshCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(cursor, "k:"))
	if err != nil || len(raw) == 0 {
		return nil, ErrInvalidFreshCursor
	}
	return raw, nil
}

// ensureFreshTimeIndex backfills the fresh-time index for records indexed
// before it existed. Mirrors ensureMetaAppTimeIndexes: state-key gated,
// single-flight via indexMu, one batch over the record namespace.
func (a *Aggregator) ensureFreshTimeIndex() error {
	if a == nil || a.store == nil {
		return nil
	}

	ready, err := a.freshTimeIndexStateReady()
	if err != nil || ready {
		return err
	}

	a.indexMu.Lock()
	defer a.indexMu.Unlock()

	ready, err = a.freshTimeIndexStateReady()
	if err != nil || ready {
		return err
	}

	db, err := a.store.OpenDB(Namespace)
	if err != nil {
		return err
	}

	batch := db.NewBatch()
	defer batch.Close()

	if err := a.store.ScanPrefix(Namespace, []byte(keyRecord), func(_, value []byte) error {
		var rec Record
		if e := json.Unmarshal(value, &rec); e != nil {
			return nil
		}
		if rec.Hidden || rec.CreatedAt <= 0 || rec.ChainName == "" || rec.SourcePinId == "" || !isPublishedProtocol(rec.ProtocolPath) {
			return nil
		}
		return batch.Set(
			freshTimeKey(metawebdoc.NormalizeUnixSeconds(rec.CreatedAt), rec.ChainName, rec.ProtocolPath, rec.SourcePinId),
			[]byte{},
			pebble.Sync,
		)
	}); err != nil {
		return err
	}

	if err := batch.Set(freshTimeIndexStateKey(), []byte("done"), pebble.Sync); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (a *Aggregator) freshTimeIndexStateReady() (bool, error) {
	if a == nil || a.store == nil {
		return false, nil
	}
	if _, err := a.store.Get(Namespace, freshTimeIndexStateKey()); err == nil {
		return true, nil
	} else if !errors.Is(err, pebble.ErrNotFound) {
		return false, err
	}
	return false, nil
}
