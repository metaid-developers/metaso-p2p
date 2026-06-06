package skillservice

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// ratingPinOpts mirrors servicePinOpts but for /protocols/skill-service-rate.
// Each test composes one of these and lets makeRatingPin assemble the
// PinInscription so the assertion surface stays focused on aggregator
// behaviour, not boilerplate.
type ratingPinOpts struct {
	PinId         string
	ChainName     string
	RaterMetaId   string
	ServiceID     string
	ServicePaidTx string
	Rate          int
	Comment       string
	Timestamp     int64
}

func makeRatingPin(t *testing.T, opts ratingPinOpts) *aggregator.PinInscription {
	t.Helper()
	body := RatingContentSummary{
		ServiceID:     opts.ServiceID,
		ServicePaidTx: opts.ServicePaidTx,
		Rate:          opts.Rate,
		Comment:       opts.Comment,
	}
	return &aggregator.PinInscription{
		Id:            opts.PinId,
		Path:          PathSkillServiceRate,
		Operation:     OperationCreate,
		ChainName:     opts.ChainName,
		MetaId:        opts.RaterMetaId,
		CreateMetaId:  opts.RaterMetaId,
		Address:       "rater-addr-" + opts.RaterMetaId,
		CreateAddress: "rater-addr-" + opts.RaterMetaId,
		GlobalMetaId:  "idq1-" + opts.RaterMetaId,
		ContentBody:   mustMarshal(t, body),
		Timestamp:     opts.Timestamp,
	}
}

// seedService is a small helper that creates a known service so we can
// rate it. It returns the service's sourceServicePinId.
func seedService(t *testing.T, agg *Aggregator, chainName, pinId, providerMetaId string) string {
	t.Helper()
	if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
		PinId: pinId, Operation: OperationCreate,
		ChainName: chainName, ProviderMetaId: providerMetaId,
		Timestamp:   1000,
		ServiceName: "svc-" + pinId, DisplayName: "svc-" + pinId,
	})); err != nil {
		t.Fatalf("seed create %s: %v", pinId, err)
	}
	return pinId
}

// --- AC: serviceID == sourceServicePinId is the simple path ---

func TestRating_ServiceIDIsSourcePinId(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	sourceId := seedService(t, agg, "mvc", "svc:i0", "prov1")
	if _, err := agg.HandleBlockPin(makeRatingPin(t, ratingPinOpts{
		PinId: "rate1:i0", ChainName: "mvc",
		RaterMetaId: "rater1", ServiceID: sourceId, Rate: 5,
		Timestamp: 2000,
	})); err != nil {
		t.Fatal(err)
	}

	agg2, _ := agg.LoadRatingAggregate("mvc", sourceId)
	if agg2 == nil {
		t.Fatal("aggregate should exist")
	}
	if agg2.Count != 1 || agg2.Sum != 5 {
		t.Errorf("count/sum: %d/%d want 1/5", agg2.Count, agg2.Sum)
	}
	if agg2.Average() != 5.0 {
		t.Errorf("avg: got %v want 5", agg2.Average())
	}
}

// --- AC: serviceID == currentPinId of a modified service folds back to source ---

func TestRating_ServiceIDIsCurrentPinId_NormalisedToSource(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	seedService(t, agg, "mvc", "src:i0", "prov1")
	if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
		PinId: "mod:i0", Operation: OperationModify,
		ChainName: "mvc", ProviderMetaId: "prov1",
		OriginalId: "src:i0", Timestamp: 1500,
		ServiceName: "svc-modified", DisplayName: "svc-modified",
	})); err != nil {
		t.Fatal(err)
	}

	// Rate targets the current pin id, not the source.
	if _, err := agg.HandleBlockPin(makeRatingPin(t, ratingPinOpts{
		PinId: "rate:i0", ChainName: "mvc",
		RaterMetaId: "rater1", ServiceID: "mod:i0", Rate: 4,
		Timestamp: 3000,
	})); err != nil {
		t.Fatal(err)
	}

	aggSrc, _ := agg.LoadRatingAggregate("mvc", "src:i0")
	if aggSrc == nil || aggSrc.Count != 1 || aggSrc.Sum != 4 {
		t.Errorf("aggregate under source: %+v", aggSrc)
	}
	// No phantom aggregate under the modify pin id.
	aggMod, _ := agg.LoadRatingAggregate("mvc", "mod:i0")
	if aggMod != nil {
		t.Errorf("aggregate must not exist under modify pin id: %+v", aggMod)
	}
}

// --- AC: serviceID == older modify pin id (now historical) still normalises ---

func TestRating_ServiceIDIsOldVersion_NormalisedToSource(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	seedService(t, agg, "mvc", "src:i0", "prov1")
	// Two modifies: mod_a (older), mod_b (current).
	for _, m := range []struct {
		id string
		ts int64
	}{{"mod_a:i0", 1500}, {"mod_b:i0", 2000}} {
		if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
			PinId: m.id, Operation: OperationModify,
			ChainName: "mvc", ProviderMetaId: "prov1",
			OriginalId: "src:i0", Timestamp: m.ts,
			ServiceName: m.id, DisplayName: m.id,
		})); err != nil {
			t.Fatal(err)
		}
	}

	// User rated against mod_a (now historical, not the current pin).
	if _, err := agg.HandleBlockPin(makeRatingPin(t, ratingPinOpts{
		PinId: "rate:i0", ChainName: "mvc",
		RaterMetaId: "rater1", ServiceID: "mod_a:i0", Rate: 3,
		Timestamp: 3000,
	})); err != nil {
		t.Fatal(err)
	}

	aggSrc, _ := agg.LoadRatingAggregate("mvc", "src:i0")
	if aggSrc == nil || aggSrc.Sum != 3 || aggSrc.Count != 1 {
		t.Errorf("rating against historical version did not fold to source: %+v", aggSrc)
	}
}

// --- AC: out-of-range rate is discarded (no aggregate change) ---

func TestRating_OutOfRangeDiscarded(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	src := seedService(t, agg, "mvc", "svc:i0", "prov1")
	cases := []int{0, 6, -1, 100}
	for i, rate := range cases {
		_, err := agg.HandleBlockPin(makeRatingPin(t, ratingPinOpts{
			PinId: "bad:" + intToStr(i), ChainName: "mvc",
			RaterMetaId: "rater", ServiceID: src, Rate: rate,
			Timestamp: int64(i + 1),
		}))
		if err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := agg.LoadRatingAggregate("mvc", src); got != nil {
		t.Errorf("expected no aggregate after invalid ratings, got %+v", got)
	}
}

// --- AC: duplicate rating pin id is deduplicated (no double count) ---

func TestRating_DuplicatePinIdDeduplicated(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	src := seedService(t, agg, "mvc", "svc:i0", "prov1")
	for i := 0; i < 3; i++ {
		if _, err := agg.HandleBlockPin(makeRatingPin(t, ratingPinOpts{
			PinId: "rate:i0", ChainName: "mvc",
			RaterMetaId: "rater", ServiceID: src, Rate: 5,
			Timestamp: int64(i + 1),
		})); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := agg.LoadRatingAggregate("mvc", src)
	if got == nil || got.Count != 1 {
		t.Errorf("dedup failed: %+v", got)
	}
}

// --- AC: rating against unknown service is persisted but not aggregated ---

func TestRating_UnknownServiceNotAggregated(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	if _, err := agg.HandleBlockPin(makeRatingPin(t, ratingPinOpts{
		PinId: "rate:i0", ChainName: "mvc",
		RaterMetaId: "rater", ServiceID: "unknown:i0", Rate: 5,
		Timestamp: 1000,
	})); err != nil {
		t.Fatal(err)
	}
	if got, _ := agg.LoadRatingAggregate("mvc", "unknown:i0"); got != nil {
		t.Errorf("unknown service must not have an aggregate yet: %+v", got)
	}
	// The rating pin itself IS persisted so a future backfill can re-aggregate.
	rp, _ := agg.loadRatingPin("mvc", "rate:i0")
	if rp == nil {
		t.Fatal("rating pin should still be persisted for backfill")
	}
	if rp.SourceServicePinId != "" {
		t.Errorf("source must remain unresolved: %q", rp.SourceServicePinId)
	}
}

// --- AC: cross-chain rating (rate on chain A, service on chain B) is rejected ---

func TestRating_CrossChainRejected(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	// Service is on MVC.
	seedService(t, agg, "mvc", "svc:i0", "prov1")

	// Rating is on DOGE but points at the MVC service id. Per spec the
	// version chain doesn't cross chains; the rating should not bump the
	// MVC aggregate.
	if _, err := agg.HandleBlockPin(makeRatingPin(t, ratingPinOpts{
		PinId: "rate:i0", ChainName: "doge",
		RaterMetaId: "rater", ServiceID: "svc:i0", Rate: 5,
		Timestamp: 1000,
	})); err != nil {
		t.Fatal(err)
	}
	if got, _ := agg.LoadRatingAggregate("mvc", "svc:i0"); got != nil {
		t.Errorf("cross-chain rating must not bump MVC aggregate: %+v", got)
	}
}

// --- AC: multiple distinct ratings accumulate correctly (avg / sum / count) ---

func TestRating_MultipleAccumulate(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	src := seedService(t, agg, "mvc", "svc:i0", "prov1")
	rates := []int{5, 4, 5, 3}
	for i, r := range rates {
		if _, err := agg.HandleBlockPin(makeRatingPin(t, ratingPinOpts{
			PinId: "rate:" + intToStr(i), ChainName: "mvc",
			RaterMetaId: "rater" + intToStr(i), ServiceID: src, Rate: r,
			Timestamp: int64(2000 + i),
		})); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := agg.LoadRatingAggregate("mvc", src)
	if got == nil {
		t.Fatal("aggregate missing")
	}
	if got.Count != 4 {
		t.Errorf("count: %d want 4", got.Count)
	}
	if got.Sum != 17 {
		t.Errorf("sum: %d want 17", got.Sum)
	}
	want := float64(17) / 4
	if got.Average() != want {
		t.Errorf("avg: %v want %v", got.Average(), want)
	}
}

// --- AC: malformed rating pin body skipped ---

func TestRating_MalformedSkipped(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	cases := []*aggregator.PinInscription{
		{Path: PathSkillServiceRate, Id: "x", ChainName: "mvc", ContentBody: []byte("not json")},
		{Path: PathSkillServiceRate, Id: "x", ChainName: "mvc", ContentBody: []byte(`{"rate":3}`)},        // no serviceID
		{Path: PathSkillServiceRate, Id: "x", ChainName: "mvc", ContentBody: []byte(`{"serviceID":"y"}`)}, // no rate
	}
	for i, p := range cases {
		if _, err := agg.HandleBlockPin(p); err != nil {
			t.Errorf("case %d errored: %v", i, err)
		}
	}
}

// --- AC: nil aggregate's Average is 0 (used by handlers that read missing data) ---

func TestRatingAggregate_NilAverage(t *testing.T) {
	var a *RatingAggregate
	if a.Average() != 0 {
		t.Errorf("nil Average should be 0, got %v", a.Average())
	}
	zero := &RatingAggregate{}
	if zero.Average() != 0 {
		t.Errorf("empty Average should be 0, got %v", zero.Average())
	}
}
