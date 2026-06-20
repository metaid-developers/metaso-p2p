package main

import (
	"context"
	"net/http"
	"time"

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
