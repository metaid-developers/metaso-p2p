package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/privatechat"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
	"github.com/metaid-developers/metaso-p2p/internal/config"
)

func TestParseOptionsUsesExplicitSinceOverLookback(t *testing.T) {
	cfg := config.Default()
	cfg.Pebble.DataDir = "/data/prod"
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

	opts, err := parseOptions(cfg, []string{
		"--since", "2025-11-01",
		"--lookback", "24h",
		"--manapi-base-url", "https://manapi.example/",
		"--sections", "metaapps",
	}, now)
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}

	wantSince := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	if !opts.Since.Equal(wantSince) {
		t.Fatalf("Since = %s, want %s", opts.Since, wantSince)
	}
	if opts.DataDir != "/data/prod" {
		t.Fatalf("DataDir = %q, want /data/prod", opts.DataDir)
	}
	if opts.MANAPIBaseURL != "https://manapi.example" {
		t.Fatalf("MANAPIBaseURL = %q, want trimmed override", opts.MANAPIBaseURL)
	}
}

func TestParseOptionsDefaultsToAllHomepageBackfillSections(t *testing.T) {
	cfg := config.Default()
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

	opts, err := parseOptions(cfg, nil, now)
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}

	if !opts.Since.Equal(now.Add(-defaultLookback)) {
		t.Fatalf("Since = %s, want %s", opts.Since, now.Add(-defaultLookback))
	}
	assertStringSlicesEqual(t, opts.UserInfoPaths, userinfo.DefaultBackfillPaths())
	assertStringSlicesEqual(t, opts.SkillServicePaths, skillservice.DefaultBackfillPaths())
	assertStringSlicesEqual(t, opts.PublishedContentPaths, []string{
		publishedcontent.PathMetaApp,
		publishedcontent.PathSimpleBuzz,
		publishedcontent.PathMetaBotSkill,
	})
	assertStringSlicesEqual(t, opts.PrivateChatPaths, []string{privatechat.HomepageSimpleMsgProtocolPath})
}

func TestParseOptionsNarrowsBySections(t *testing.T) {
	cfg := config.Default()
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

	opts, err := parseOptions(cfg, []string{"--sections", "metaapps,chats"}, now)
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}

	assertStringSlicesEqual(t, opts.UserInfoPaths, nil)
	assertStringSlicesEqual(t, opts.SkillServicePaths, nil)
	assertStringSlicesEqual(t, opts.PublishedContentPaths, []string{publishedcontent.PathMetaApp})
	assertStringSlicesEqual(t, opts.PrivateChatPaths, []string{privatechat.HomepageSimpleMsgProtocolPath})
}

func TestParseOptionsNarrowsByPaths(t *testing.T) {
	cfg := config.Default()
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

	opts, err := parseOptions(cfg, []string{
		"--paths", "/protocols/metaapp,/protocols/skill-service,/info/homepage,/protocols/simplemsg",
	}, now)
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}

	assertStringSlicesEqual(t, opts.UserInfoPaths, []string{"/info/homepage"})
	assertStringSlicesEqual(t, opts.SkillServicePaths, []string{skillservice.PathSkillService})
	assertStringSlicesEqual(t, opts.PublishedContentPaths, []string{publishedcontent.PathMetaApp})
	assertStringSlicesEqual(t, opts.PrivateChatPaths, []string{privatechat.HomepageSimpleMsgProtocolPath})
}

func TestParseOptionsRejectsUnknownSection(t *testing.T) {
	cfg := config.Default()
	_, err := parseOptions(cfg, []string{"--sections", "payments"}, time.Now())
	if err == nil {
		t.Fatal("parseOptions returned nil error, want unsupported section error")
	}
}

func assertStringSlicesEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(want) == 0 {
		want = nil
	}
	if len(got) == 0 {
		got = nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("slice = %#v, want %#v", got, want)
	}
}
