package main

import (
	"context"
	"net/http"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/block"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/social"
	"github.com/metaid-developers/metaso-p2p/internal/config"
)

type socialBackfiller interface {
	Backfill(opts social.BackfillOptions) error
}

func runSocialBackfillIfEnabled(agg socialBackfiller, cfg config.SocialBackfillConfig) error {
	if !cfg.Enabled || agg == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	return agg.Backfill(social.BackfillOptions{
		Context:  ctx,
		Client:   social.NewBackfillClient(cfg.MANAPIBaseURL, http.DefaultClient),
		Since:    time.Now().Add(-cfg.Lookback),
		PageSize: cfg.PageSize,
	})
}

type blockBackfiller interface {
	Backfill(opts block.BackfillOptions) error
}

// runBlockBackfillIfEnabled replays the Simpleblock history through the
// normal ingest path (disabled by default, spec §4.4).
func runBlockBackfillIfEnabled(agg blockBackfiller, cfg config.BlockBackfillConfig) error {
	if !cfg.Enabled || agg == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	return agg.Backfill(block.BackfillOptions{
		Context:  ctx,
		Client:   block.NewBackfillClient(cfg.MANAPIBaseURL, http.DefaultClient),
		Since:    time.Now().Add(-cfg.Lookback),
		PageSize: cfg.PageSize,
	})
}
