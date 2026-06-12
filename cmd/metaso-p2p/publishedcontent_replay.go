package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	mvcchain "github.com/metaid-developers/metaso-p2p/internal/chain/mvc"
	"github.com/metaid-developers/metaso-p2p/internal/config"
)

type publishedContentReplayConfig struct {
	Enabled       bool
	ChainName     string
	ProtocolPaths []string
	FromHeight    int64
	ToHeight      int64
	Lookback      time.Duration
	ProgressEvery int64
}

func loadPublishedContentReplayConfig() publishedContentReplayConfig {
	cfg := publishedContentReplayConfig{
		ChainName:     "mvc",
		ProtocolPaths: []string{publishedcontent.PathSimpleBuzz},
		Lookback:      1440 * time.Hour,
		ProgressEvery: 100,
	}
	cfg.Enabled = envBool("METASO_P2P_PUBLISHEDCONTENT_REPLAY_ENABLED", false)
	if value := strings.TrimSpace(os.Getenv("METASO_P2P_PUBLISHEDCONTENT_REPLAY_CHAIN")); value != "" {
		cfg.ChainName = strings.ToLower(value)
	}
	if value := strings.TrimSpace(os.Getenv("METASO_P2P_PUBLISHEDCONTENT_REPLAY_PROTOCOLS")); value != "" {
		parts := strings.Split(value, ",")
		cfg.ProtocolPaths = cfg.ProtocolPaths[:0]
		for _, part := range parts {
			if path := strings.TrimSpace(part); path != "" {
				cfg.ProtocolPaths = append(cfg.ProtocolPaths, path)
			}
		}
	}
	cfg.FromHeight = envInt64("METASO_P2P_PUBLISHEDCONTENT_REPLAY_FROM_HEIGHT", 0)
	cfg.ToHeight = envInt64("METASO_P2P_PUBLISHEDCONTENT_REPLAY_TO_HEIGHT", 0)
	cfg.Lookback = envDuration("METASO_P2P_PUBLISHEDCONTENT_REPLAY_LOOKBACK", cfg.Lookback)
	cfg.ProgressEvery = envInt64("METASO_P2P_PUBLISHEDCONTENT_REPLAY_PROGRESS_EVERY", cfg.ProgressEvery)
	if cfg.ProgressEvery <= 0 {
		cfg.ProgressEvery = 100
	}
	if len(cfg.ProtocolPaths) == 0 {
		cfg.ProtocolPaths = []string{publishedcontent.PathSimpleBuzz}
	}
	return cfg
}

func startPublishedContentReplayIfEnabled(publishedAgg *publishedcontent.Aggregator, appCfg config.Config) {
	replayCfg := loadPublishedContentReplayConfig()
	if !replayCfg.Enabled {
		return
	}
	if publishedAgg == nil {
		log.Printf("WARNING: publishedcontent replay requested but aggregator is unavailable")
		return
	}
	if replayCfg.ChainName != "mvc" {
		log.Printf("WARNING: publishedcontent replay chain %q is not supported; supported chain=mvc", replayCfg.ChainName)
		return
	}
	if !appCfg.BlockIndex.MVC.Enabled {
		log.Printf("WARNING: publishedcontent replay requested but MVC block index config is disabled")
		return
	}

	go runMVCPublishedContentReplay(publishedAgg, appCfg.BlockIndex.MVC, replayCfg)
}

func runMVCPublishedContentReplay(publishedAgg *publishedcontent.Aggregator, chainCfg config.ChainRPCConfig, replayCfg publishedContentReplayConfig) {
	chain := mvcchain.NewChain(chainCfg)
	if err := chain.Init(); err != nil {
		log.Printf("WARNING: publishedcontent replay MVC chain init failed: %v", err)
		return
	}
	indexer := mvcchain.NewIndexer(chain, mvcchain.NetParams(""))
	if err := indexer.Init(); err != nil {
		log.Printf("WARNING: publishedcontent replay MVC indexer init failed: %v", err)
		return
	}

	toHeight := replayCfg.ToHeight
	if toHeight <= 0 {
		toHeight = chain.GetBestHeight()
	}
	fromHeight := replayCfg.FromHeight
	if fromHeight <= 0 {
		targetTime := time.Now().Add(-replayCfg.Lookback).Unix()
		fromHeight = findHeightAtOrAfter(chain, chainCfg.InitialHeight, toHeight, targetTime)
	}
	if fromHeight <= 0 || toHeight < fromHeight {
		log.Printf("WARNING: publishedcontent replay invalid range from=%d to=%d", fromHeight, toHeight)
		return
	}

	log.Printf("[publishedcontent-replay] start chain=mvc protocols=%s range=%d..%d lookback=%s",
		strings.Join(replayCfg.ProtocolPaths, ","), fromHeight, toHeight, replayCfg.Lookback)
	stats, err := publishedAgg.ReplayBlocks(publishedcontent.ReplayOptions{
		Context:       context.Background(),
		Indexer:       indexer,
		FromHeight:    fromHeight,
		ToHeight:      toHeight,
		ProtocolPaths: replayCfg.ProtocolPaths,
		ProgressEvery: replayCfg.ProgressEvery,
		OnProgress: func(stats publishedcontent.ReplayStats) {
			log.Printf("[publishedcontent-replay] progress range=%d..%d blocks=%d pinsSeen=%d matched=%d indexed=%d errors=%d elapsed=%s",
				stats.FromHeight, stats.ToHeight, stats.BlocksScanned, stats.PinsSeen, stats.PinsMatched, stats.PinsIndexed, stats.Errors, stats.Duration.Round(time.Second))
		},
	})
	if err != nil {
		log.Printf("WARNING: publishedcontent replay failed: %v; stats=%+v", err, stats)
		return
	}
	log.Printf("[publishedcontent-replay] complete range=%d..%d blocks=%d pinsSeen=%d matched=%d indexed=%d errors=%d elapsed=%s",
		stats.FromHeight, stats.ToHeight, stats.BlocksScanned, stats.PinsSeen, stats.PinsMatched, stats.PinsIndexed, stats.Errors, stats.Duration.Round(time.Second))
}

type blockTimeReader interface {
	GetBlockTime(height int64) (int64, error)
}

func findHeightAtOrAfter(chain blockTimeReader, lower, upper, targetUnix int64) int64 {
	if lower <= 0 {
		lower = 1
	}
	if upper < lower {
		return 0
	}
	left, right := lower, upper
	result := upper
	for left <= right {
		mid := left + (right-left)/2
		ts, err := chain.GetBlockTime(mid)
		if err != nil {
			return lower
		}
		if ts >= targetUnix {
			result = mid
			right = mid - 1
		} else {
			left = mid + 1
		}
	}
	return result
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
