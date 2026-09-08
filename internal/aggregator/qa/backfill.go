package qa

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
)

// Backfill replays the MANAPI history of the four Q&A-related protocol paths
// through the qa read model. Paths replay in dependency order (questions
// before answers before likes/comments) and pins replay oldest-first, so
// joins settle and last-state-wins like aggregation converges. Idempotent:
// re-running converges to the same state.
type BackfillOptions struct {
	Context  context.Context
	Client   *publishedcontent.BackfillClient
	Since    time.Time
	PageSize int
	// Stats, when non-nil, accumulates per-path/per-chain counters during the
	// run for the completion report.
	Stats *BackfillStats
}

// backfillPaths is the fixed replay order of the Q&A backfill.
var backfillPaths = []string{PathSimpleQuestion, PathSimpleAnswer, PathPayLike, PathPayComment}

// BackfillChainStats accumulates the counters of one (path, chainName) cell.
type BackfillChainStats struct {
	Fetched int64
	Applied int64 // pins that produced a read-model change
	Skipped int64 // out-of-scope pins (empty or question-mark-less title, orphan, non-QA target, stale)
	Errors  int64
}

// BackfillStats groups backfill counters by protocol path, then chainName.
type BackfillStats struct {
	Paths map[string]map[string]*BackfillChainStats
}

func (s *BackfillStats) chain(path, chainName string) *BackfillChainStats {
	if s == nil {
		return nil
	}
	if s.Paths == nil {
		s.Paths = make(map[string]map[string]*BackfillChainStats)
	}
	byChain := s.Paths[path]
	if byChain == nil {
		byChain = make(map[string]*BackfillChainStats)
		s.Paths[path] = byChain
	}
	chainName = strings.ToLower(strings.TrimSpace(chainName))
	if chainName == "" {
		chainName = "unknown"
	}
	stats := byChain[chainName]
	if stats == nil {
		stats = &BackfillChainStats{}
		byChain[chainName] = stats
	}
	return stats
}

func (a *Aggregator) Backfill(opts BackfillOptions) error {
	if a.store == nil {
		return errors.New("qa aggregator is not initialised")
	}
	client := opts.Client
	if client == nil {
		return errors.New("qa backfill client is required")
	}
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 100
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	// A nil Stats gets a throwaway instance so counters can be recorded
	// unconditionally.
	stats := opts.Stats
	if stats == nil {
		stats = &BackfillStats{}
	}

	for _, path := range backfillPaths {
		pins, err := fetchBackfillPins(ctx, client, path, opts.Since, pageSize)
		if err != nil {
			return err
		}
		for _, pin := range pins {
			stats.chain(path, normaliseChain(pin.ChainName)).Fetched++
		}
		// Oldest-first replay (MANAPI lists newest-first).
		for i := len(pins) - 1; i >= 0; i-- {
			pin := pins[i]
			if err := a.processPin(pin, false); err != nil {
				stats.chain(path, normaliseChain(pin.ChainName)).Errors++
				return err
			}
			stats.chain(path, normaliseChain(pin.ChainName)).Applied++
		}
	}
	return nil
}

// fetchBackfillPins pages through the MANAPI path listing until exhausted or
// older than the since cutoff.
func fetchBackfillPins(ctx context.Context, client *publishedcontent.BackfillClient, path string, since time.Time, pageSize int) ([]*aggregator.PinInscription, error) {
	cursor := ""
	seenCursors := make(map[string]struct{})
	pinsToReplay := make([]*aggregator.PinInscription, 0, pageSize)
	for {
		seenCursors[cursor] = struct{}{}
		pins, nextCursor, err := client.ListPathPins(ctx, path, cursor, pageSize)
		if err != nil {
			return nil, err
		}
		if len(pins) == 0 {
			break
		}
		allOlder := true
		for _, pin := range pins {
			if backfillTimestampBefore(pin.Timestamp, since) {
				continue
			}
			allOlder = false
			pinsToReplay = append(pinsToReplay, pin)
		}
		if allOlder {
			break
		}
		if nextCursor == "" || len(pins) < pageSize {
			break
		}
		if _, seen := seenCursors[nextCursor]; seen {
			return nil, fmt.Errorf("repeated MANAPI cursor %q for path %s", nextCursor, path)
		}
		cursor = nextCursor
	}
	return pinsToReplay, nil
}

// backfillTimestampBefore mirrors the publishedcontent cutoff rule: chain
// timestamps arrive in mixed second/millisecond granularity.
func backfillTimestampBefore(timestamp int64, since time.Time) bool {
	if since.IsZero() || timestamp <= 0 {
		return false
	}
	cutoffMillis := since.UnixMilli()
	if timestamp > 100000000000 {
		return timestamp < cutoffMillis
	}
	return timestamp < since.Unix()
}
