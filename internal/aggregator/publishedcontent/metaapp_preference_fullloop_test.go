package publishedcontent_test

// Full-loop wiring test: the real preference aggregator feeds the real
// publishedcontent sort=preference listing through the production adapter
// pair (SetPreferenceLookup / SetMetaAppResolver), mirroring
// cmd/metaso-p2p/main.go. External test package: publishedcontent tests
// cannot import preference (which itself imports publishedcontent).

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/preference"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// aggregatorPinForTest builds a create pin accepted by both aggregators.
func aggregatorPinForTest(t *testing.T, path, pinId string, body string, ts int64) *aggregator.PinInscription {
	t.Helper()
	return &aggregator.PinInscription{
		Id:           pinId,
		Path:         path,
		Operation:    "create",
		ChainName:    "mvc",
		ContentType:  "application/json",
		ContentBody:  []byte(body),
		GlobalMetaId: "gid-loop-author",
		MetaId:       "gid-loop-author",
		Address:      "gid-loop-author",
		Timestamp:    ts,
	}
}

func TestPreferenceListFullLoopWithRealAggregators(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()
	cacheProvider := cache.New(store)

	pub := &publishedcontent.Aggregator{}
	if err := pub.Init(store, cacheProvider); err != nil {
		t.Fatalf("publishedcontent Init: %v", err)
	}
	pref := &preference.Aggregator{}
	pref.SetMetaAppResolver(preference.NewMetaAppResolverAdapter(pub))
	if err := pref.Init(store, cacheProvider); err != nil {
		t.Fatalf("preference Init: %v", err)
	}
	pub.SetPreferenceLookup(pref)

	// Two motion-video apps on mvc (create pins through the live pipeline).
	mustPubPin := func(pinId, videoSha string, ts int64) {
		body := `{"title":"MV ` + pinId + `","tags":["motion-video"],"runtime":"browser","prompt":"p","metadata":{"series":"motion-video","videoFile":"metafile://` + pinId + `-video:i0","videoSha256":"` + videoSha + `","durationSec":10,"fps":24,"resolution":"1920x1080","engine":"e","style":"s","sourceIncluded":true}}`
		if _, err := pub.HandleBlockPin(aggregatorPinForTest(t, "/protocols/metaapp", pinId, body, ts)); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pinId, err)
		}
	}
	mustPubPin("app-loop-a:i0", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1000)
	mustPubPin("app-loop-b:i0", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 2000)

	// Three judges pick A over B (via publishedcontent-style feedback pins).
	batch := `{"category":["motion-video"],"kind":"comparison-batch","client":{"artifact":"metaapp://arena-root:i0","mode":"arena"},"items":[` +
		`{"a":{"chainName":"mvc","app":"metaapp://app-loop-a:i0","version":"metaapp://app-loop-a:i0","video":"metafile://app-loop-a:i0-video:i0","videoSha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},` +
		`"b":{"chainName":"mvc","app":"metaapp://app-loop-b:i0","version":"metaapp://app-loop-b:i0","video":"metafile://app-loop-b:i0-video:i0","videoSha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},` +
		`"pick":"a"}]}`
	for i, signer := range []string{"gid-judge-1", "gid-judge-2", "gid-judge-3"} {
		pin := aggregatorPinForTest(t, "/protocols/preference/feedback", "fb-loop-"+string(rune('0'+i))+":i0", batch, int64(3000+i))
		pin.GlobalMetaId = signer
		pin.MetaId = signer
		pin.Address = signer
		if _, err := pref.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin feedback: %v", err)
		}
	}

	result, err := pub.ListMetaApps(publishedcontent.MetaAppListParams{
		ChainName: "mvc", Tag: "motion-video",
		Sort: "preference", Order: "desc", Basis: "open",
	})
	if err != nil {
		t.Fatalf("ListMetaApps: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("len: %d", len(result.Items))
	}
	if result.Items[0].PinID != "app-loop-a:i0" {
		t.Fatalf("3 wins must rank A first: %+v", result.Items)
	}
	open := result.Items[0].Preference.Open
	// A: 3 wins of 3 → (3+1)/(3+2) = 0.8, established.
	if open.Score == nil || *open.Score < 0.799 || *open.Score > 0.801 {
		t.Fatalf("A open score: %+v", open)
	}
	if open.State != "established" || open.Judges != 3 || open.Comparisons != 3 {
		t.Fatalf("A open summary: %+v", open)
	}
	if result.Items[0].Preference.Validated.State != "unavailable" || result.Items[0].Preference.Validated.Score != nil {
		t.Fatalf("validated block: %+v", result.Items[0].Preference.Validated)
	}
	bOpen := result.Items[1].Preference.Open
	// B: 0 wins of 3 → (0+1)/(3+2) = 0.2, established.
	if bOpen.Score == nil || *bOpen.Score < 0.199 || *bOpen.Score > 0.201 {
		t.Fatalf("B open score: %+v", bOpen)
	}
}
