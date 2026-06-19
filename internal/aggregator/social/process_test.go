package social

import "testing"

type fakeTargetLookup struct {
	byMetaId       map[string]*TargetRef
	byGlobalMetaId map[string]*TargetRef
	byAddress      map[string]*TargetRef
	calls          []string
}

func (f *fakeTargetLookup) LookupByMetaId(metaId string) (*TargetRef, error) {
	f.calls = append(f.calls, "metaid:"+metaId)
	return f.byMetaId[metaId], nil
}

func (f *fakeTargetLookup) LookupByGlobalMetaId(globalMetaId string) (*TargetRef, error) {
	f.calls = append(f.calls, "global:"+globalMetaId)
	return f.byGlobalMetaId[globalMetaId], nil
}

func (f *fakeTargetLookup) LookupByAddress(address string) (*TargetRef, error) {
	f.calls = append(f.calls, "address:"+address)
	return f.byAddress[address], nil
}

func newTargetRef(metaId, globalMetaId, address string) *TargetRef {
	return &TargetRef{
		MetaId:       metaId,
		GlobalMetaId: globalMetaId,
		Address:      address,
	}
}

func TestAggregatorNameAndEmptyLookup(t *testing.T) {
	agg := &Aggregator{}
	if got := agg.Name(); got != "social" {
		t.Fatalf("Name() = %q, want social", got)
	}

	target, err := agg.lookupTargetRef("anything")
	if err != nil {
		t.Fatalf("lookupTargetRef returned error: %v", err)
	}
	if target != nil {
		t.Fatalf("lookupTargetRef returned %+v, want nil", target)
	}
}

func TestLookupSubjectAcceptsGlobalMetaIdMetaIdAndAddress(t *testing.T) {
	t.Run("global meta id", func(t *testing.T) {
		lookup := &fakeTargetLookup{
			byGlobalMetaId: map[string]*TargetRef{
				"gid-1": newTargetRef("meta-1", "gid-1", "addr-1"),
			},
		}
		agg := &Aggregator{}
		agg.SetProfileLookup(lookup)

		target, err := agg.lookupTargetRef("gid-1")
		if err != nil {
			t.Fatalf("lookupTargetRef returned error: %v", err)
		}
		if target == nil || target.GlobalMetaId != "gid-1" {
			t.Fatalf("lookupTargetRef = %+v, want globalMetaId gid-1", target)
		}
		assertCalls(t, lookup.calls, []string{"global:gid-1"})
	})

	t.Run("meta id", func(t *testing.T) {
		lookup := &fakeTargetLookup{
			byMetaId: map[string]*TargetRef{
				"meta-2": newTargetRef("meta-2", "gid-2", "addr-2"),
			},
		}
		agg := &Aggregator{}
		agg.SetProfileLookup(lookup)

		target, err := agg.lookupTargetRef("meta-2")
		if err != nil {
			t.Fatalf("lookupTargetRef returned error: %v", err)
		}
		if target == nil || target.MetaId != "meta-2" {
			t.Fatalf("lookupTargetRef = %+v, want metaId meta-2", target)
		}
		assertCalls(t, lookup.calls, []string{"global:meta-2", "metaid:meta-2"})
	})

	t.Run("address", func(t *testing.T) {
		lookup := &fakeTargetLookup{
			byAddress: map[string]*TargetRef{
				"addr-3": newTargetRef("meta-3", "gid-3", "addr-3"),
			},
		}
		agg := &Aggregator{}
		agg.SetProfileLookup(lookup)

		target, err := agg.lookupTargetRef("addr-3")
		if err != nil {
			t.Fatalf("lookupTargetRef returned error: %v", err)
		}
		if target == nil || target.Address != "addr-3" {
			t.Fatalf("lookupTargetRef = %+v, want address addr-3", target)
		}
		assertCalls(t, lookup.calls, []string{"global:addr-3", "metaid:addr-3", "address:addr-3"})
	})
}

func assertCalls(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls len = %d, want %d; got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls[%d] = %q, want %q; got=%v want=%v", i, got[i], want[i], got, want)
		}
	}
}
