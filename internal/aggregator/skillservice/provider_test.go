package skillservice

import (
	"errors"
	"testing"
)

// fakeProfileLookup is a hand-rolled ProfileLookup used to exercise the
// three-stage fallback (MetaId → GlobalMetaId → Address) without dragging
// in the full userinfo aggregator. Each map holds the keys the test wants
// the lookup to "know"; the second-return value lets a test simulate an
// I/O error and confirm we still fall through to the next stage.
type fakeProfileLookup struct {
	byMetaId      map[string]*ProfileSnapshot
	byGlobalMeta  map[string]*ProfileSnapshot
	byAddress     map[string]*ProfileSnapshot
	errMetaId     map[string]error
	errGlobalMeta map[string]error
	errAddress    map[string]error
	calls         []string
}

func (f *fakeProfileLookup) LookupByMetaId(metaid string) (*ProfileSnapshot, error) {
	f.calls = append(f.calls, "metaid:"+metaid)
	if err, ok := f.errMetaId[metaid]; ok {
		return nil, err
	}
	return f.byMetaId[metaid], nil
}
func (f *fakeProfileLookup) LookupByGlobalMetaId(g string) (*ProfileSnapshot, error) {
	f.calls = append(f.calls, "gid:"+g)
	if err, ok := f.errGlobalMeta[g]; ok {
		return nil, err
	}
	return f.byGlobalMeta[g], nil
}
func (f *fakeProfileLookup) LookupByAddress(a string) (*ProfileSnapshot, error) {
	f.calls = append(f.calls, "addr:"+a)
	if err, ok := f.errAddress[a]; ok {
		return nil, err
	}
	return f.byAddress[a], nil
}

// --- AC: MetaId hits early-exit (no GlobalMetaId / Address probe) ---

func TestResolveProvider_MetaIdShortCircuits(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	fake := &fakeProfileLookup{
		byMetaId: map[string]*ProfileSnapshot{"m1": {Name: "Alice", Avatar: "ava.png", ChatPublicKey: "pk1"}},
	}
	agg.SetProfileLookup(fake)

	got := agg.ResolveProvider(&ServiceRecord{
		ProviderMetaId:       "m1",
		ProviderGlobalMetaId: "idq1-zzz",
		ProviderAddress:      "addr-zzz",
	})
	if got.Name != "Alice" || got.ChatPublicKey != "pk1" {
		t.Errorf("expected Alice/pk1, got %+v", got)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "metaid:m1" {
		t.Errorf("expected short-circuit on metaid lookup, calls=%v", fake.calls)
	}
}

// --- AC: MetaId miss → fall back to GlobalMetaId ---

func TestResolveProvider_FallsBackToGlobalMetaId(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	fake := &fakeProfileLookup{
		byGlobalMeta: map[string]*ProfileSnapshot{"idq1-bob": {Name: "Bob"}},
	}
	agg.SetProfileLookup(fake)

	got := agg.ResolveProvider(&ServiceRecord{
		ProviderMetaId:       "m_missing",
		ProviderGlobalMetaId: "idq1-bob",
		ProviderAddress:      "addr-ignored",
	})
	if got.Name != "Bob" {
		t.Errorf("expected Bob, got %+v", got)
	}
	if len(fake.calls) < 2 || fake.calls[1] != "gid:idq1-bob" {
		t.Errorf("expected metaid miss then gid lookup, calls=%v", fake.calls)
	}
	// Must not have probed the address tier since gid hit.
	for _, c := range fake.calls {
		if c == "addr:addr-ignored" {
			t.Errorf("unexpected address lookup after gid hit, calls=%v", fake.calls)
		}
	}
}

// --- AC: both MetaId and GlobalMetaId miss → fall back to Address ---

func TestResolveProvider_FallsBackToAddress(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	fake := &fakeProfileLookup{
		byAddress: map[string]*ProfileSnapshot{"1A1z": {Name: "Carol", ChatPublicKey: "pkC"}},
	}
	agg.SetProfileLookup(fake)

	got := agg.ResolveProvider(&ServiceRecord{
		ProviderMetaId:       "m_missing",
		ProviderGlobalMetaId: "idq1-missing",
		ProviderAddress:      "1A1z",
	})
	if got.Name != "Carol" || got.ChatPublicKey != "pkC" {
		t.Errorf("expected Carol/pkC, got %+v", got)
	}
}

// --- AC: all three miss → empty snapshot, no error ---

func TestResolveProvider_AllMissReturnsEmpty(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	fake := &fakeProfileLookup{}
	agg.SetProfileLookup(fake)

	got := agg.ResolveProvider(&ServiceRecord{
		ProviderMetaId:       "m_missing",
		ProviderGlobalMetaId: "idq1-missing",
		ProviderAddress:      "addr-missing",
	})
	if got.Name != "" || got.Avatar != "" || got.ChatPublicKey != "" {
		t.Errorf("expected zero snapshot, got %+v", got)
	}
}

// --- AC: lookup error on early tier falls through to later tier ---

func TestResolveProvider_ErrorFallsThrough(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	fake := &fakeProfileLookup{
		errMetaId:    map[string]error{"m1": errors.New("transient")},
		byGlobalMeta: map[string]*ProfileSnapshot{"idq1-x": {Name: "Recovered"}},
	}
	agg.SetProfileLookup(fake)

	got := agg.ResolveProvider(&ServiceRecord{
		ProviderMetaId:       "m1",
		ProviderGlobalMetaId: "idq1-x",
	})
	if got.Name != "Recovered" {
		t.Errorf("expected fall-through to GlobalMetaId after metaid error, got %+v", got)
	}
}

// --- AC: nil ProfileLookup is safe (no panic, empty snapshot) ---

func TestResolveProvider_NilLookupIsSafe(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()
	// Do NOT call SetProfileLookup.

	got := agg.ResolveProvider(&ServiceRecord{
		ProviderMetaId: "anything",
	})
	if got.Name != "" {
		t.Errorf("expected empty snapshot with no lookup, got %+v", got)
	}
}

// --- AC: nil record is safe ---

func TestResolveProvider_NilRecordIsSafe(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()
	agg.SetProfileLookup(&fakeProfileLookup{})

	got := agg.ResolveProvider(nil)
	if got.Name != "" {
		t.Errorf("nil record should yield zero snapshot, got %+v", got)
	}
}

// --- AC: empty/whitespace provider identifiers are skipped (no useless lookup calls) ---

func TestResolveProvider_SkipsEmptyIdentifiers(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()
	fake := &fakeProfileLookup{}
	agg.SetProfileLookup(fake)

	agg.ResolveProvider(&ServiceRecord{
		ProviderMetaId:       "   ",
		ProviderGlobalMetaId: "",
		ProviderAddress:      "\t",
	})
	if len(fake.calls) != 0 {
		t.Errorf("expected no lookup calls for blank identifiers, got %v", fake.calls)
	}
}
