package mrc721

import (
	"context"
	"errors"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
)

// SellBackfillOptions configures one MANAPI replay of the
// /protocols/mrc721sell declaration log through the sell-intent read model.
type SellBackfillOptions struct {
	Context  context.Context
	Client   *publishedcontent.BackfillClient
	Since    time.Time
	PageSize int
	// Stats, when non-nil, accumulates the run counters for the completion
	// report.
	Stats *SellBackfillStats
}

// SellBackfillStats is the completion report of one sell-intent replay.
type SellBackfillStats struct {
	Fetched int64
	Applied int64 // pins that changed the read model
	Skipped int64 // out-of-scope (non-create op / other path), invalid payloads, and pins older than the stored latest-wins declaration
	Errors  int64
}

// BackfillSell replays the /protocols/mrc721sell MANAPI history
// oldest-first, so the latest-pin-wins overwrite settles exactly like live
// aggregation. Idempotent: re-running converges to the same state (same-pin
// re-applies are no-ops, older pins lose the chain-order race).
func (a *Aggregator) BackfillSell(opts SellBackfillOptions) error {
	if a == nil || a.store == nil {
		return errors.New("mrc721 aggregator is not initialised")
	}
	client := opts.Client
	if client == nil {
		return errors.New("mrc721 sell backfill client is required")
	}
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 100
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	stats := opts.Stats

	pins, err := fetchSellBackfillPins(ctx, client, opts.Since, pageSize)
	if err != nil {
		return err
	}
	if stats != nil {
		stats.Fetched = int64(len(pins))
	}

	// MANAPI lists newest-first; replay oldest-first so overwrites apply in
	// chain order.
	for i := len(pins) - 1; i >= 0; i-- {
		a.mu.Lock()
		applied, err := a.ingestSellPin(pins[i])
		a.mu.Unlock()
		if err != nil {
			if stats != nil {
				stats.Errors++
			}
			return err
		}
		if stats != nil {
			if applied {
				stats.Applied++
			} else {
				stats.Skipped++
			}
		}
	}
	return nil
}

// fetchSellBackfillPins pages through the MANAPI path listing until
// exhausted or older than the since cutoff (same rule as the qa backfill).
func fetchSellBackfillPins(ctx context.Context, client *publishedcontent.BackfillClient, since time.Time, pageSize int) ([]*aggregator.PinInscription, error) {
	cursor := ""
	seenCursors := make(map[string]struct{})
	pinsToReplay := make([]*aggregator.PinInscription, 0, pageSize)
	for {
		seenCursors[cursor] = struct{}{}
		pins, nextCursor, err := client.ListPathPins(ctx, PathSell, cursor, pageSize)
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
			return nil, errors.New("repeated MANAPI cursor for path " + PathSell)
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
