package publishedcontent

import (
	"context"
	"errors"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

type ReplayIndexer interface {
	CatchPins(height int64) ([]*aggregator.PinInscription, []string, error)
}

type ReplayOptions struct {
	Context       context.Context
	Indexer       ReplayIndexer
	FromHeight    int64
	ToHeight      int64
	ProtocolPaths []string
	OnProgress    func(ReplayStats)
	ProgressEvery int64
}

type ReplayStats struct {
	FromHeight    int64
	ToHeight      int64
	BlocksScanned int64
	PinsSeen      int64
	PinsMatched   int64
	PinsIndexed   int64
	Errors        int64
	StartedAt     time.Time
	FinishedAt    time.Time
	Duration      time.Duration
}

func (a *Aggregator) ReplayBlocks(opts ReplayOptions) (ReplayStats, error) {
	stats := ReplayStats{
		FromHeight: opts.FromHeight,
		ToHeight:   opts.ToHeight,
		StartedAt:  time.Now(),
	}
	if a == nil {
		return stats, errors.New("publishedcontent replay aggregator is required")
	}
	if opts.Indexer == nil {
		return stats, errors.New("publishedcontent replay indexer is required")
	}
	if opts.FromHeight <= 0 || opts.ToHeight < opts.FromHeight {
		return stats, errors.New("publishedcontent replay requires a valid height range")
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	progressEvery := opts.ProgressEvery
	if progressEvery <= 0 {
		progressEvery = 100
	}
	allowed := replayProtocolSet(opts.ProtocolPaths)

	for height := opts.FromHeight; height <= opts.ToHeight; height++ {
		select {
		case <-ctx.Done():
			stats.FinishedAt = time.Now()
			stats.Duration = stats.FinishedAt.Sub(stats.StartedAt)
			return stats, ctx.Err()
		default:
		}

		pins, _, err := opts.Indexer.CatchPins(height)
		if err != nil {
			stats.Errors++
			continue
		}
		stats.BlocksScanned++
		for _, pin := range pins {
			stats.PinsSeen++
			if !replayProtocolAllowed(pin, allowed) {
				continue
			}
			stats.PinsMatched++
			if _, err := a.HandleBlockPin(pin); err != nil {
				stats.Errors++
				continue
			}
			stats.PinsIndexed++
		}
		if opts.OnProgress != nil && stats.BlocksScanned%progressEvery == 0 {
			current := stats
			current.FinishedAt = time.Now()
			current.Duration = current.FinishedAt.Sub(current.StartedAt)
			opts.OnProgress(current)
		}
	}

	stats.FinishedAt = time.Now()
	stats.Duration = stats.FinishedAt.Sub(stats.StartedAt)
	if opts.OnProgress != nil {
		opts.OnProgress(stats)
	}
	return stats, nil
}

func replayProtocolSet(paths []string) map[string]struct{} {
	if len(paths) == 0 {
		paths = []string{PathSimpleBuzz, PathMetaApp, PathMetaBotSkill}
	}
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if normalised := protocolPathFromPinPath(path); normalised != "" {
			out[normalised] = struct{}{}
		}
	}
	return out
}

func replayProtocolAllowed(pin *aggregator.PinInscription, allowed map[string]struct{}) bool {
	if pin == nil || len(allowed) == 0 {
		return false
	}
	_, ok := allowed[protocolPathFromPinPath(pin.Path)]
	return ok
}
