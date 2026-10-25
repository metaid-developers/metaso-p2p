package preference

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
)

// Backfill replays the MANAPI history of /protocols/preference/feedback
// through the preference read model, oldest-first, using the shared
// publishedcontent backfill client. Idempotent: re-running converges to the
// same state (duplicate pin ids are skipped by processPin). Only create
// pins are consumed; any modify/revoke pins that appear on chain are
// ignored per the shared contract §4.1.
type BackfillOptions struct {
	Context  context.Context
	Client   *publishedcontent.BackfillClient
	Since    time.Time
	PageSize int
	// Stats, when non-nil, accumulates per-path/per-chain counters during the
	// run for the completion report.
	Stats *BackfillStats
}

// BackfillChainStats accumulates the counters of one (path, chainName) cell.
type BackfillChainStats struct {
	Fetched int64
	Applied int64 // pins replayed through the read model
	Skipped int64 // modify/revoke pins and duplicate replays
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

// RecordStatus classifies one replayed pin for the stats report.
type RecordStatus int

const (
	RecordApplied RecordStatus = iota
	RecordSkipped
)

func (a *Aggregator) Backfill(opts BackfillOptions) error {
	if a == nil || a.store == nil {
		return errors.New("preference aggregator is not initialised")
	}
	client := opts.Client
	if client == nil {
		return errors.New("preference backfill client is required")
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
	if stats == nil {
		stats = &BackfillStats{}
	}

	pins, err := fetchFeedbackPins(ctx, client, opts.Since, pageSize)
	if err != nil {
		return err
	}
	for _, pin := range pins {
		stats.chain(PathPreferenceFeedback, pin.ChainName).Fetched++
	}
	// Oldest-first replay so latest-per-signer/pair converges to the same
	// state as live confirmation order (MANAPI lists newest-first). A single
	// pin failure is counted and skipped — the run is a full idempotent
	// rescan, so the failed pin is simply retried on the next run instead of
	// aborting the whole replay; the error summary at the end keeps the
	// failure honest.
	applyPin := a.backfillApplyPin
	if applyPin == nil {
		applyPin = func(pin *aggregator.PinInscription) error { return a.processPin(pin, false) }
	}
	failed := make([]string, 0)
	for i := len(pins) - 1; i >= 0; i-- {
		pin := pins[i]
		chainStats := stats.chain(PathPreferenceFeedback, pin.ChainName)
		if err := applyPin(pin); err != nil {
			chainStats.Errors++
			failed = append(failed, fmt.Sprintf("%s: %v", pin.Id, err))
			continue
		}
		if wasReplayed(pin) {
			chainStats.Applied++
		} else {
			chainStats.Skipped++
		}
	}
	// Resolution exclusions recorded during this run (apps backfilled after
	// their feedback) self-heal here, and the score snapshot rebuilds once —
	// not per pin.
	var finalErr error
	if err := a.reevaluateResolutions(); err != nil {
		finalErr = fmt.Errorf("preference backfill: resolution re-evaluation failed: %w", err)
	}
	a.ensureFreshOpenScores()
	if len(failed) > 0 {
		pinErr := fmt.Errorf("preference backfill: %d pin(s) failed and were skipped (re-run retries them): %s", len(failed), strings.Join(failed, "; "))
		if finalErr != nil {
			finalErr = fmt.Errorf("%v; additionally %v", finalErr, pinErr)
		} else {
			finalErr = pinErr
		}
	}
	return finalErr
}

// wasReplayed reports whether the pin belongs to the consumed slice of the
// path (create pins on /protocols/preference/feedback) or was ignored
// (modify/revoke and off-path pins).
func wasReplayed(pin *aggregator.PinInscription) bool {
	if protocolPathFromPinPath(pin.Path) != PathPreferenceFeedback {
		return false
	}
	return normaliseOperation(pin.Operation) == "create"
}

// fetchFeedbackPins pages through the MANAPI path listing until exhausted or
// older than the since cutoff.
func fetchFeedbackPins(ctx context.Context, client *publishedcontent.BackfillClient, since time.Time, pageSize int) ([]*aggregator.PinInscription, error) {
	cursor := ""
	seenCursors := make(map[string]struct{})
	pinsToReplay := make([]*aggregator.PinInscription, 0, pageSize)
	for {
		seenCursors[cursor] = struct{}{}
		pins, nextCursor, err := client.ListPathPins(ctx, PathPreferenceFeedback, cursor, pageSize)
		if err != nil {
			return nil, err
		}
		if len(pins) == 0 {
			break
		}
		allOlder := true
		for _, pin := range pins {
			if feedbackTimestampBefore(pin.Timestamp, since) {
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
			return nil, fmt.Errorf("repeated MANAPI cursor %q for path %s", nextCursor, PathPreferenceFeedback)
		}
		cursor = nextCursor
	}
	return pinsToReplay, nil
}

// feedbackTimestampBefore mirrors the publishedcontent cutoff rule: chain
// timestamps arrive in mixed second/millisecond granularity.
func feedbackTimestampBefore(timestamp int64, since time.Time) bool {
	if since.IsZero() || timestamp <= 0 {
		return false
	}
	cutoffMillis := since.UnixMilli()
	if timestamp > 100000000000 {
		return timestamp < cutoffMillis
	}
	return timestamp < since.Unix()
}
