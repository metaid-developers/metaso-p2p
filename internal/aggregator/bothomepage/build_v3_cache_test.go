package bothomepage

import (
	"testing"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/privatechat"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/presence"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

type countingProfileLookup struct {
	profile *ProfileSnapshot
	err     error
	calls   int
}

func (f *countingProfileLookup) LookupByGlobalMetaId(globalMetaId string) (*ProfileSnapshot, error) {
	f.calls++
	return f.profile, f.err
}

type countingHomepageServiceLister struct {
	result *skillservice.HomepageListResult
	err    error
	calls  int
}

func (l *countingHomepageServiceLister) ListHomepageByProvider(params skillservice.HomepageListParams) (*skillservice.HomepageListResult, error) {
	l.calls++
	return l.result, l.err
}

type countingPublishedContentLister struct {
	results   map[string]*publishedcontent.ListResult
	errs      map[string]error
	gotParams []publishedcontent.ListParams
	calls     int
}

func (l *countingPublishedContentLister) List(params publishedcontent.ListParams) (*publishedcontent.ListResult, error) {
	l.calls++
	l.gotParams = append(l.gotParams, params)
	if err := l.errs[params.ProtocolPath]; err != nil {
		return nil, err
	}
	if result := l.results[params.ProtocolPath]; result != nil {
		return result, nil
	}
	return &publishedcontent.ListResult{}, nil
}

type countingChatInteractionLister struct {
	result *privatechat.HomepageInteractionListResult
	err    error
	calls  int
}

func (l *countingChatInteractionLister) ListOutgoingHomepageInteractions(params privatechat.HomepageInteractionListParams) (*privatechat.HomepageInteractionListResult, error) {
	l.calls++
	return l.result, l.err
}

func newCacheableV3Aggregator(t *testing.T) (*Aggregator, *countingProfileLookup, *countingHomepageServiceLister, *countingPublishedContentLister, *countingChatInteractionLister, *fakeLocalPresence) {
	t.Helper()

	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { store.Close() })

	agg := &Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	profiles := &countingProfileLookup{
		profile: &ProfileSnapshot{
			GlobalMetaId: "idqBot",
			MetaId:       "metaBot",
			Address:      "1BotAddress",
			ChainName:    "mvc",
			Name:         "Cache Bot",
		},
	}
	services := &countingHomepageServiceLister{
		result: &skillservice.HomepageListResult{
			List: []skillservice.ServiceListItem{{
				CurrentPinId:   "service-current:i0",
				ServiceName:    "fortune",
				DisplayName:    "Fortune",
				ProviderSkill:  "fortune-skill",
				OutputType:     "text",
				Price:          "1",
				Currency:       "SPACE",
				SettlementKind: "address",
				PaymentAddress: "1BotAddress",
				UpdatedAt:      1710000002,
			}},
		},
	}
	content := &countingPublishedContentLister{
		results: map[string]*publishedcontent.ListResult{
			publishedcontent.PathMetaApp: {
				Items: []publishedcontent.SectionItem{{
					SourcePinId:    "metaapp-current:i0",
					CurrentPinId:   "metaapp-current:i0",
					ProtocolPath:   publishedcontent.PathMetaApp,
					PayloadExposed: true,
					PayloadJSON: map[string]any{
						"title": "Homepage App",
						"kind":  "tool",
					},
					CreatedAt: 1710000003,
				}},
			},
			publishedcontent.PathSimpleBuzz: {
				Items: []publishedcontent.SectionItem{{
					SourcePinId:    "buzz-current:i0",
					CurrentPinId:   "buzz-current:i0",
					ProtocolPath:   publishedcontent.PathSimpleBuzz,
					PayloadExposed: true,
					PayloadText:    "hello world",
					CreatedAt:      1710000004,
				}},
			},
		},
	}
	chats := &countingChatInteractionLister{
		result: &privatechat.HomepageInteractionListResult{
			Items: []privatechat.HomepageInteraction{{
				PinId:        "chat-current:i0",
				InteractWith: "idqPeerBot",
				Timestamp:    1710000005,
			}},
		},
	}
	localPresence := &fakeLocalPresence{
		entries: []presence.OnlineEntry{{
			MetaId:      "metaBot",
			ConnectedAt: 1710000006,
			LastSeenAt:  1710000007,
		}},
	}

	agg.SetProfileLookup(profiles)
	agg.SetHomepageServiceLister(services)
	agg.SetPublishedContentLister(content)
	agg.SetChatInteractionLister(chats)
	agg.SetPresenceReaders(localPresence, nil)

	return agg, profiles, services, content, chats, localPresence
}

func TestBuildV3CachesStablePayloadAndRefreshesPresence(t *testing.T) {
	agg, profiles, services, content, chats, localPresence := newCacheableV3Aggregator(t)

	opts := defaultV3Options()
	first, err := agg.BuildV3("idqBot", opts)
	if err != nil {
		t.Fatalf("first BuildV3 returned error: %v", err)
	}
	if first.Presence.State != "online" {
		t.Fatalf("first presence state = %q, want online", first.Presence.State)
	}

	localPresence.entries = nil

	second, err := agg.BuildV3("idqBot", opts)
	if err != nil {
		t.Fatalf("second BuildV3 returned error: %v", err)
	}
	if second.Presence.State != "unknown" {
		t.Fatalf("second presence state = %q, want unknown after presence refresh", second.Presence.State)
	}
	if profiles.calls != 2 {
		t.Fatalf("profile lookup calls = %d, want 2 cache hit on second request", profiles.calls)
	}
	if services.calls != 1 {
		t.Fatalf("homepage service calls = %d, want 1 cache hit on second request", services.calls)
	}
	if content.calls != 6 {
		t.Fatalf("published content calls = %d, want 6 total alias reads from first request only", content.calls)
	}
	if chats.calls != 1 {
		t.Fatalf("chat interaction calls = %d, want 1 cache hit on second request", chats.calls)
	}
	if localPresence.calls != 2 {
		t.Fatalf("local presence calls = %d, want 2 because presence should be recomputed", localPresence.calls)
	}
}

func TestBuildV3CacheIgnoresIncludePresenceInStablePayloadKey(t *testing.T) {
	agg, profiles, services, content, chats, localPresence := newCacheableV3Aggregator(t)

	firstOpts := defaultV3Options()
	if _, err := agg.BuildV3("idqBot", firstOpts); err != nil {
		t.Fatalf("first BuildV3 returned error: %v", err)
	}

	secondOpts := defaultV3Options()
	secondOpts.IncludePresence = false
	second, err := agg.BuildV3("idqBot", secondOpts)
	if err != nil {
		t.Fatalf("second BuildV3 returned error: %v", err)
	}
	if second.Presence.State != "unknown" {
		t.Fatalf("second presence state = %q, want unknown when includePresence=false", second.Presence.State)
	}
	if profiles.calls != 2 {
		t.Fatalf("profile lookup calls = %d, want 2 shared stable cache entry", profiles.calls)
	}
	if services.calls != 1 {
		t.Fatalf("homepage service calls = %d, want 1 shared stable cache entry", services.calls)
	}
	if content.calls != 6 {
		t.Fatalf("published content calls = %d, want 6 total alias reads from first request only", content.calls)
	}
	if chats.calls != 1 {
		t.Fatalf("chat interaction calls = %d, want 1 shared stable cache entry", chats.calls)
	}
	if localPresence.calls != 1 {
		t.Fatalf("local presence calls = %d, want 1 because includePresence=false should skip recompute", localPresence.calls)
	}
}

func TestBuildV3CacheExpiresAndFallsBackToReload(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { store.Close() })

	agg := &Aggregator{v3ResultCacheTTL: 10 * time.Millisecond}
	if err := agg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	profiles := &countingProfileLookup{
		profile: &ProfileSnapshot{
			GlobalMetaId: "idqBot",
			MetaId:       "metaBot",
			Address:      "1BotAddress",
			ChainName:    "mvc",
			Name:         "Cache Bot",
		},
	}
	services := &countingHomepageServiceLister{
		result: &skillservice.HomepageListResult{
			List: []skillservice.ServiceListItem{{
				CurrentPinId:   "service-current:i0",
				ServiceName:    "fortune",
				DisplayName:    "Fortune",
				ProviderSkill:  "fortune-skill",
				OutputType:     "text",
				Price:          "1",
				Currency:       "SPACE",
				SettlementKind: "address",
				PaymentAddress: "1BotAddress",
				UpdatedAt:      1710000002,
			}},
		},
	}
	content := &countingPublishedContentLister{
		results: map[string]*publishedcontent.ListResult{
			publishedcontent.PathMetaApp: {
				Items: []publishedcontent.SectionItem{{
					SourcePinId:    "metaapp-current:i0",
					CurrentPinId:   "metaapp-current:i0",
					ProtocolPath:   publishedcontent.PathMetaApp,
					PayloadExposed: true,
					PayloadJSON: map[string]any{
						"title": "Homepage App",
						"kind":  "tool",
					},
					CreatedAt: 1710000003,
				}},
			},
			publishedcontent.PathSimpleBuzz: {
				Items: []publishedcontent.SectionItem{{
					SourcePinId:    "buzz-current:i0",
					CurrentPinId:   "buzz-current:i0",
					ProtocolPath:   publishedcontent.PathSimpleBuzz,
					PayloadExposed: true,
					PayloadText:    "hello world",
					CreatedAt:      1710000004,
				}},
			},
		},
	}
	chats := &countingChatInteractionLister{
		result: &privatechat.HomepageInteractionListResult{
			Items: []privatechat.HomepageInteraction{{
				PinId:        "chat-current:i0",
				InteractWith: "idqPeerBot",
				Timestamp:    1710000005,
			}},
		},
	}

	agg.SetProfileLookup(profiles)
	agg.SetHomepageServiceLister(services)
	agg.SetPublishedContentLister(content)
	agg.SetChatInteractionLister(chats)

	opts := defaultV3Options()
	if _, err := agg.BuildV3("idqBot", opts); err != nil {
		t.Fatalf("first BuildV3 returned error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	if _, err := agg.BuildV3("idqBot", opts); err != nil {
		t.Fatalf("second BuildV3 returned error: %v", err)
	}
	if profiles.calls != 4 {
		t.Fatalf("profile lookup calls = %d, want 4 after TTL expiry reload", profiles.calls)
	}
	if services.calls != 2 {
		t.Fatalf("homepage service calls = %d, want 2 after TTL expiry reload", services.calls)
	}
	if content.calls != 12 {
		t.Fatalf("published content calls = %d, want 12 after TTL expiry reload", content.calls)
	}
	if chats.calls != 2 {
		t.Fatalf("chat interaction calls = %d, want 2 after TTL expiry reload", chats.calls)
	}
}
