package publishedcontent

import (
	"strings"
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

func TestProtocolRegistryFoldAuthoritativeAndConflicts(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	// Three valid registrations of one key, fed out of order: the earliest
	// createdAt must hold the authority no matter the arrival order.
	middle := makeContentPin(contentPinOpts{
		PinId: "proto-mid:i0", Path: PathMetaProtocol, Operation: OperationCreate,
		Timestamp: 1710000200, GlobalMetaId: "gid-mid", MetaId: "meta-mid", Address: "addr-mid",
		ContentType: "application/json",
		ContentBody: []byte(`{"title":"Game Protocol","path":"/protocols/game","protocolName":"game","version":"1.0.0"}`),
	})
	earliest := makeContentPin(contentPinOpts{
		PinId: "proto-early:i0", Path: PathMetaProtocol, Operation: OperationCreate,
		Timestamp: 1710000100, GlobalMetaId: "gid-early", MetaId: "meta-early", Address: "addr-early",
		ContentType: "application/json",
		ContentBody: []byte(`{"title":"Game Protocol","path":"/protocols/game","protocolName":"game","version":"1.0.0"}`),
	})
	latest := makeContentPin(contentPinOpts{
		PinId: "proto-late:i0", Path: PathMetaProtocol, Operation: OperationCreate,
		Timestamp: 1710000300, GlobalMetaId: "gid-late", MetaId: "meta-late", Address: "addr-late",
		ContentType: "application/json",
		ContentBody: []byte(`{"title":"Game Protocol copy","path":"/protocols/GAME","protocolName":"game","version":"1.0.0"}`),
	})
	for _, pin := range []*aggregator.PinInscription{middle, earliest, latest} {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pin.Id, err)
		}
	}

	// The registration key lower-cases the payload path: "/protocols/GAME"
	// folds into "/protocols/game".
	registration, err := agg.ProtocolRegistrationByPath("/protocols/GaMe")
	if err != nil {
		t.Fatalf("ProtocolRegistrationByPath: %v", err)
	}
	if registration == nil || registration.Authoritative == nil {
		t.Fatalf("registration missing: %+v", registration)
	}
	if registration.Authoritative.SourcePinId != "proto-early:i0" {
		t.Fatalf("authoritative = %s, want proto-early:i0", registration.Authoritative.SourcePinId)
	}
	if len(registration.Conflicts) != 2 {
		t.Fatalf("conflicts = %d, want 2", len(registration.Conflicts))
	}
	if registration.Conflicts[0].SourcePinId != "proto-mid:i0" || registration.Conflicts[1].SourcePinId != "proto-late:i0" {
		t.Fatalf("conflict order = %s, %s; want proto-mid:i0, proto-late:i0",
			registration.Conflicts[0].SourcePinId, registration.Conflicts[1].SourcePinId)
	}

	registrations, err := agg.ProtocolRegistrations()
	if err != nil {
		t.Fatalf("ProtocolRegistrations: %v", err)
	}
	if len(registrations) != 1 || registrations[0].RegKey != "/protocols/game" {
		t.Fatalf("registrations = %+v, want single /protocols/game fold", registrations)
	}
}

func TestProtocolRegistrySuccessionOnRevoke(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	for _, opts := range []contentPinOpts{
		{PinId: "proto-a:i0", Timestamp: 1710000100, GlobalMetaId: "gid-a", MetaId: "meta-a", Address: "addr-a"},
		{PinId: "proto-b:i0", Timestamp: 1710000200, GlobalMetaId: "gid-b", MetaId: "meta-b", Address: "addr-b"},
		{PinId: "proto-c:i0", Timestamp: 1710000300, GlobalMetaId: "gid-c", MetaId: "meta-c", Address: "addr-c"},
	} {
		opts.Path = PathMetaProtocol
		opts.Operation = OperationCreate
		opts.ContentType = "application/json"
		opts.ContentBody = []byte(`{"title":"P","path":"/protocols/succ","protocolName":"succ"}`)
		if _, err := agg.HandleBlockPin(makeContentPin(opts)); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", opts.PinId, err)
		}
	}

	revoke := func(targetPinId string, ts int64) {
		t.Helper()
		if _, err := agg.HandleBlockPin(makeContentPin(contentPinOpts{
			PinId: "revoke-" + targetPinId, Path: PathMetaProtocol + "@" + targetPinId,
			Operation: OperationRevoke, Timestamp: ts,
			GlobalMetaId: "gid-a", MetaId: "meta-a", Address: "addr-a",
		})); err != nil {
			t.Fatalf("HandleBlockPin(revoke %s): %v", targetPinId, err)
		}
	}

	// Revoking the authoritative registration promotes the earliest
	// remaining valid registration.
	revoke("proto-a:i0", 1710000400)
	registration, err := agg.ProtocolRegistrationByPath("/protocols/succ")
	if err != nil {
		t.Fatalf("ProtocolRegistrationByPath: %v", err)
	}
	if registration == nil || registration.Authoritative == nil || registration.Authoritative.SourcePinId != "proto-b:i0" {
		t.Fatalf("after first revoke authoritative = %+v, want proto-b:i0", registration)
	}
	if len(registration.Conflicts) != 1 || registration.Conflicts[0].SourcePinId != "proto-c:i0" {
		t.Fatalf("after first revoke conflicts = %+v, want [proto-c:i0]", registration.Conflicts)
	}

	// Revoking everything deletes the key: the path is available again.
	revoke("proto-b:i0", 1710000500)
	revoke("proto-c:i0", 1710000600)
	registration, err = agg.ProtocolRegistrationByPath("/protocols/succ")
	if err != nil {
		t.Fatalf("ProtocolRegistrationByPath: %v", err)
	}
	if registration != nil {
		t.Fatalf("after full revoke registration = %+v, want nil (path available)", registration)
	}
	if _, err := store.Get(Namespace, protocolRegistryKeyFor("/protocols/succ")); err != pebble.ErrNotFound {
		t.Fatalf("registry key after full revoke: err = %v, want ErrNotFound", err)
	}
}

func TestProtocolRegistryForgedModifyAuditedNotChained(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	if _, err := agg.HandleBlockPin(makeContentPin(contentPinOpts{
		PinId: "proto-src:i0", Path: PathMetaProtocol, Operation: OperationCreate,
		Timestamp: 1710000100, GlobalMetaId: "gid-owner", MetaId: "meta-owner", Address: "addr-owner",
		ContentType: "application/json",
		ContentBody: []byte(`{"title":"P","path":"/protocols/owned","protocolName":"owned","version":"1.0.0"}`),
	})); err != nil {
		t.Fatalf("HandleBlockPin(create): %v", err)
	}

	// A modify from a different publisher (different globalMetaId, metaId and
	// address) is forged: it must not join the version chain.
	forged := makeContentPin(contentPinOpts{
		PinId: "proto-forge:i0", Path: PathMetaProtocol + "@proto-src:i0", Operation: OperationModify,
		Timestamp: 1710000200, GlobalMetaId: "gid-forger", MetaId: "meta-forger", Address: "addr-forger",
		ContentType: "application/json",
		ContentBody: []byte(`{"title":"Hijacked","path":"/protocols/owned","protocolName":"owned","version":"9.9.9"}`),
	})
	if _, err := agg.HandleBlockPin(forged); err != nil {
		t.Fatalf("HandleBlockPin(forged modify): %v", err)
	}

	rec, err := agg.loadRecord("mvc", PathMetaProtocol, "proto-src:i0")
	if err != nil || rec == nil {
		t.Fatalf("loadRecord: %v %v", rec, err)
	}
	if rec.CurrentPinId != "proto-src:i0" {
		t.Fatalf("currentPinId = %s, want unchanged proto-src:i0", rec.CurrentPinId)
	}
	if got := agg.sourcePinIdFor("mvc", "proto-forge:i0"); got != "proto-forge:i0" {
		t.Fatalf("forged pin mapped into chain: sourcePinIdFor = %s", got)
	}

	audits, err := agg.InvalidModifies("mvc", "proto-src:i0")
	if err != nil {
		t.Fatalf("InvalidModifies: %v", err)
	}
	if len(audits) != 1 {
		t.Fatalf("invalid modifies = %+v, want 1 entry", audits)
	}
	audit := audits[0]
	if audit.PinId != "proto-forge:i0" || audit.TargetSourcePinId != "proto-src:i0" ||
		audit.Reason != InvalidModifyReasonPublisherMismatch || audit.Timestamp != 1710000200 {
		t.Fatalf("audit = %+v", audit)
	}
	if audit.ModifierIdentity.GlobalMetaId != "gid-forger" || audit.ModifierIdentity.MetaId != "meta-forger" ||
		audit.ModifierIdentity.Address != "addr-forger" {
		t.Fatalf("modifier identity = %+v", audit.ModifierIdentity)
	}
}

func TestProtocolRegistryOwnerModifyAdvancesChain(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	if _, err := agg.HandleBlockPin(makeContentPin(contentPinOpts{
		PinId: "proto-src:i0", Path: PathMetaProtocol, Operation: OperationCreate,
		Timestamp: 1710000100, GlobalMetaId: "gid-owner", MetaId: "meta-owner", Address: "addr-owner",
		ContentType: "application/json",
		ContentBody: []byte(`{"title":"P","path":"/protocols/owned","protocolName":"owned","version":"1.0.0"}`),
	})); err != nil {
		t.Fatalf("HandleBlockPin(create): %v", err)
	}
	if _, err := agg.HandleBlockPin(makeContentPin(contentPinOpts{
		PinId: "proto-v2:i0", Path: PathMetaProtocol + "@proto-src:i0", Operation: OperationModify,
		Timestamp: 1710000200, GlobalMetaId: "gid-owner", MetaId: "meta-owner", Address: "addr-owner",
		ContentType: "application/json",
		ContentBody: []byte(`{"title":"P","path":"/protocols/owned","protocolName":"owned","version":"1.0.1"}`),
	})); err != nil {
		t.Fatalf("HandleBlockPin(owner modify): %v", err)
	}

	rec, err := agg.loadRecord("mvc", PathMetaProtocol, "proto-src:i0")
	if err != nil || rec == nil {
		t.Fatalf("loadRecord: %v %v", rec, err)
	}
	if rec.CurrentPinId != "proto-v2:i0" {
		t.Fatalf("currentPinId = %s, want proto-v2:i0", rec.CurrentPinId)
	}
	if got := agg.sourcePinIdFor("mvc", "proto-v2:i0"); got != "proto-src:i0" {
		t.Fatalf("owner modify not chained: sourcePinIdFor = %s", got)
	}
	audits, err := agg.InvalidModifies("mvc", "proto-src:i0")
	if err != nil {
		t.Fatalf("InvalidModifies: %v", err)
	}
	if len(audits) != 0 {
		t.Fatalf("owner modify audited as invalid: %+v", audits)
	}

	// The record stays the authoritative registration of its key.
	registration, err := agg.ProtocolRegistrationByPath("/protocols/owned")
	if err != nil {
		t.Fatalf("ProtocolRegistrationByPath: %v", err)
	}
	if registration == nil || registration.Authoritative == nil || registration.Authoritative.SourcePinId != "proto-src:i0" {
		t.Fatalf("registration = %+v, want proto-src:i0 authoritative", registration)
	}
}

func TestProtocolRegistryMempoolOccupiesAndConfirmedWinsSecond(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	// A mempool registration occupies the path (anti-squatting precheck).
	if _, err := agg.HandleMempoolPin(makeContentPin(contentPinOpts{
		PinId: "proto-pending:i0", Path: PathMetaProtocol, Operation: OperationCreate,
		Timestamp: 1710000100, GlobalMetaId: "gid-p", MetaId: "meta-p", Address: "addr-p",
		ContentType: "application/json",
		ContentBody: []byte(`{"title":"P","path":"/protocols/pending","protocolName":"pending"}`),
	})); err != nil {
		t.Fatalf("HandleMempoolPin: %v", err)
	}
	registration, err := agg.ProtocolRegistrationByPath("/protocols/pending")
	if err != nil {
		t.Fatalf("ProtocolRegistrationByPath: %v", err)
	}
	if registration == nil || registration.Authoritative == nil || !registration.Authoritative.IsMempool {
		t.Fatalf("mempool registration not occupying: %+v", registration)
	}

	// Within the same second a confirmed registration outranks the mempool
	// one (requirement §2 sort: createdAt asc, confirmed first, pinId asc).
	if _, err := agg.HandleBlockPin(makeContentPin(contentPinOpts{
		PinId: "proto-confirmed:i0", Path: PathMetaProtocol, Operation: OperationCreate,
		Timestamp: 1710000100, GlobalMetaId: "gid-c", MetaId: "meta-c", Address: "addr-c",
		ContentType: "application/json",
		ContentBody: []byte(`{"title":"P","path":"/protocols/pending","protocolName":"pending"}`),
	})); err != nil {
		t.Fatalf("HandleBlockPin: %v", err)
	}
	registration, err = agg.ProtocolRegistrationByPath("/protocols/pending")
	if err != nil {
		t.Fatalf("ProtocolRegistrationByPath: %v", err)
	}
	if registration.Authoritative.SourcePinId != "proto-confirmed:i0" {
		t.Fatalf("authoritative = %s, want confirmed proto-confirmed:i0", registration.Authoritative.SourcePinId)
	}
	if len(registration.Conflicts) != 1 || registration.Conflicts[0].SourcePinId != "proto-pending:i0" {
		t.Fatalf("conflicts = %+v, want [proto-pending:i0]", registration.Conflicts)
	}

	// An earlier-second mempool registration still beats a later confirmed
	// one (createdAt dominates the confirmed tiebreak).
	if _, err := agg.HandleMempoolPin(makeContentPin(contentPinOpts{
		PinId: "proto-earlier:i0", Path: PathMetaProtocol, Operation: OperationCreate,
		Timestamp: 1710000050, GlobalMetaId: "gid-e", MetaId: "meta-e", Address: "addr-e",
		ContentType: "application/json",
		ContentBody: []byte(`{"title":"P","path":"/protocols/pending","protocolName":"pending"}`),
	})); err != nil {
		t.Fatalf("HandleMempoolPin(earlier): %v", err)
	}
	registration, err = agg.ProtocolRegistrationByPath("/protocols/pending")
	if err != nil {
		t.Fatalf("ProtocolRegistrationByPath: %v", err)
	}
	if registration.Authoritative.SourcePinId != "proto-earlier:i0" {
		t.Fatalf("authoritative = %s, want earlier mempool proto-earlier:i0", registration.Authoritative.SourcePinId)
	}
}

func TestProtocolRegistryRejectsInvalidPayloads(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	pins := []*aggregator.PinInscription{
		makeContentPin(contentPinOpts{
			PinId: "proto-nopath:i0", Path: PathMetaProtocol, Operation: OperationCreate, Timestamp: 1710000100,
			ContentType: "application/json", ContentBody: []byte(`{"title":"no path field"}`),
		}),
		makeContentPin(contentPinOpts{
			PinId: "proto-badpath:i0", Path: PathMetaProtocol, Operation: OperationCreate, Timestamp: 1710000200,
			ContentType: "application/json",
			ContentBody: []byte(`{"title":"/protocols/when requiretype is mrc721, a value is required.","path":"/protocols/when requiretype is mrc721, a value is required."}`),
		}),
		makeContentPin(contentPinOpts{
			PinId: "proto-text:i0", Path: PathMetaProtocol, Operation: OperationCreate, Timestamp: 1710000300,
			ContentType: "text/plain", ContentBody: []byte("plain text, no json"),
		}),
	}
	for _, pin := range pins {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pin.Id, err)
		}
	}

	registrations, err := agg.ProtocolRegistrations()
	if err != nil {
		t.Fatalf("ProtocolRegistrations: %v", err)
	}
	if len(registrations) != 0 {
		t.Fatalf("invalid payloads entered the registry: %+v", registrations)
	}

	rejected, err := agg.RejectedProtocolRecords()
	if err != nil {
		t.Fatalf("RejectedProtocolRecords: %v", err)
	}
	reasons := map[string]string{}
	for _, entry := range rejected {
		reasons[entry.PinId] = entry.Reason
	}
	if len(rejected) != 3 {
		t.Fatalf("rejected = %+v, want 3 entries", rejected)
	}
	if reasons["proto-nopath:i0"] != "missing path" {
		t.Errorf("nopath reason = %q", reasons["proto-nopath:i0"])
	}
	if !strings.HasPrefix(reasons["proto-badpath:i0"], "invalid path:") {
		t.Errorf("badpath reason = %q", reasons["proto-badpath:i0"])
	}
	if reasons["proto-text:i0"] != "missing or non-JSON payload" {
		t.Errorf("text reason = %q", reasons["proto-text:i0"])
	}
}

// TestProtocolOwnerCascade covers the requirement §2 identity cascade:
// globalMetaId when both sides carry it, else metaId, else address.
func TestProtocolOwnerCascade(t *testing.T) {
	cases := []struct {
		name     string
		source   PublisherIdentity
		modifier PublisherIdentity
		want     bool
	}{
		{
			name:     "globalMetaId decides when both present (match)",
			source:   PublisherIdentity{GlobalMetaId: "gid-a", MetaId: "meta-a", Address: "addr-a"},
			modifier: PublisherIdentity{GlobalMetaId: "gid-a", MetaId: "meta-x", Address: "addr-x"},
			want:     true,
		},
		{
			name:     "globalMetaId decides when both present (mismatch beats same address)",
			source:   PublisherIdentity{GlobalMetaId: "gid-a", MetaId: "meta-a", Address: "addr-a"},
			modifier: PublisherIdentity{GlobalMetaId: "gid-b", MetaId: "meta-a", Address: "addr-a"},
			want:     false,
		},
		{
			name:     "metaId fallback",
			source:   PublisherIdentity{MetaId: "meta-a", Address: "addr-a"},
			modifier: PublisherIdentity{MetaId: "META-A", Address: "addr-x"},
			want:     true,
		},
		{
			name:     "metaId fallback mismatch",
			source:   PublisherIdentity{MetaId: "meta-a", Address: "addr-a"},
			modifier: PublisherIdentity{MetaId: "meta-b", Address: "addr-a"},
			want:     false,
		},
		{
			name:     "address-only comparison",
			source:   PublisherIdentity{Address: "addr-a"},
			modifier: PublisherIdentity{Address: "addr-a"},
			want:     true,
		},
		{
			name:     "address-only mismatch",
			source:   PublisherIdentity{Address: "addr-a"},
			modifier: PublisherIdentity{Address: "addr-b"},
			want:     false,
		},
		{
			name:     "one-sided globalMetaId falls through to metaId",
			source:   PublisherIdentity{GlobalMetaId: "gid-a", MetaId: "meta-a", Address: "addr-a"},
			modifier: PublisherIdentity{MetaId: "meta-a", Address: "addr-x"},
			want:     true,
		},
		{
			name:     "no comparable identity rejects",
			source:   PublisherIdentity{},
			modifier: PublisherIdentity{},
			want:     false,
		},
	}
	for _, tc := range cases {
		if got := publisherIdentityMatches(tc.source, tc.modifier); got != tc.want {
			t.Errorf("%s: publisherIdentityMatches = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestProtocolRegistryInitRebuild seeds records, dirties the index, and
// re-inits on the same store: the startup rebuild must refold correctly
// (requirement §3.2 / §8.8).
func TestProtocolRegistryInitRebuild(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	pins := []*aggregator.PinInscription{
		makeContentPin(contentPinOpts{
			PinId: "proto-r1:i0", Path: PathMetaProtocol, Operation: OperationCreate, Timestamp: 1710000100,
			GlobalMetaId: "gid-1", MetaId: "meta-1", Address: "addr-1",
			ContentType: "application/json",
			ContentBody: []byte(`{"title":"P","path":"/protocols/rebuild","protocolName":"rebuild"}`),
		}),
		makeContentPin(contentPinOpts{
			PinId: "proto-r2:i0", Path: PathMetaProtocol, Operation: OperationCreate, Timestamp: 1710000200,
			GlobalMetaId: "gid-2", MetaId: "meta-2", Address: "addr-2",
			ContentType: "application/json",
			ContentBody: []byte(`{"title":"P","path":"/protocols/rebuild","protocolName":"rebuild"}`),
		}),
		makeContentPin(contentPinOpts{
			PinId: "proto-gone:i0", Path: PathMetaProtocol, Operation: OperationCreate, Timestamp: 1710000050,
			GlobalMetaId: "gid-g", MetaId: "meta-g", Address: "addr-g",
			ContentType: "application/json",
			ContentBody: []byte(`{"title":"P","path":"/protocols/rebuild","protocolName":"rebuild"}`),
		}),
		// Revoke the earliest registration: the rebuild must not resurrect it.
		makeContentPin(contentPinOpts{
			PinId: "proto-gone-revoke:i0", Path: PathMetaProtocol + "@proto-gone:i0", Operation: OperationRevoke,
			Timestamp: 1710000300, GlobalMetaId: "gid-g", MetaId: "meta-g", Address: "addr-g",
		}),
	}
	for _, pin := range pins {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pin.Id, err)
		}
	}

	// Dirty the index: drop every registry key and poison one extra entry.
	if err := store.DeleteByPrefix(Namespace, []byte(keyByProtocolPath)); err != nil {
		t.Fatalf("DeleteByPrefix: %v", err)
	}
	if err := store.Set(Namespace, protocolRegistryKeyFor("/protocols/stale"), []byte(`{"members":[{"sourcePinId":"ghost:i0","chainName":"mvc","createdAt":1,"confirmed":true}]}`)); err != nil {
		t.Fatalf("seed stale key: %v", err)
	}

	reloaded := &Aggregator{}
	if err := reloaded.Init(store, cache.New(store)); err != nil {
		t.Fatalf("reloaded Init: %v", err)
	}

	registration, err := reloaded.ProtocolRegistrationByPath("/protocols/rebuild")
	if err != nil {
		t.Fatalf("ProtocolRegistrationByPath: %v", err)
	}
	if registration == nil || registration.Authoritative == nil || registration.Authoritative.SourcePinId != "proto-r1:i0" {
		t.Fatalf("after rebuild authoritative = %+v, want proto-r1:i0", registration)
	}
	if len(registration.Conflicts) != 1 || registration.Conflicts[0].SourcePinId != "proto-r2:i0" {
		t.Fatalf("after rebuild conflicts = %+v, want [proto-r2:i0]", registration.Conflicts)
	}
	stale, err := reloaded.ProtocolRegistrationByPath("/protocols/stale")
	if err != nil {
		t.Fatalf("ProtocolRegistrationByPath(stale): %v", err)
	}
	if stale != nil {
		t.Fatalf("stale poisoned key survived rebuild: %+v", stale)
	}
}

// TestProtocolRegistryBlocklist excludes blocklisted pins from the
// projection while the path stays re-registerable (requirement §3.4).
func TestProtocolRegistryBlocklist(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { store.Close() })
	agg := &Aggregator{}
	agg.SetProtocolRegistryBlocklist([]string{"proto-testpin:i0"})
	if err := agg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if _, err := agg.HandleBlockPin(makeContentPin(contentPinOpts{
		PinId: "proto-testpin:i0", Path: PathMetaProtocol, Operation: OperationCreate, Timestamp: 1710000100,
		ContentType: "application/json",
		ContentBody: []byte(`{"title":"test","path":"/protocols/blocked","protocolName":"blocked"}`),
	})); err != nil {
		t.Fatalf("HandleBlockPin: %v", err)
	}
	registration, err := agg.ProtocolRegistrationByPath("/protocols/blocked")
	if err != nil {
		t.Fatalf("ProtocolRegistrationByPath: %v", err)
	}
	if registration != nil {
		t.Fatalf("blocklisted pin entered the registry: %+v", registration)
	}
	// Blocklisted pins are excluded by policy, not audited as rejected.
	rejected, err := agg.RejectedProtocolRecords()
	if err != nil {
		t.Fatalf("RejectedProtocolRecords: %v", err)
	}
	if len(rejected) != 0 {
		t.Fatalf("blocklisted pin showed up as rejected: %+v", rejected)
	}
}

// TestProtocolRegistryOwnerCheckToggle documents the env-gated escape hatch
// (METASO_P2P_PROTOCOL_OWNER_BOUND_MODIFIES=false): foreign modifies join
// the chain again, and the default (unset) keeps the check on.
func TestProtocolRegistryOwnerCheckToggle(t *testing.T) {
	newAgg := func(t *testing.T, configure func(*Aggregator)) (*Aggregator, *storage.PebbleStore) {
		t.Helper()
		store := storage.NewPebbleStore(t.TempDir())
		t.Cleanup(func() { store.Close() })
		agg := &Aggregator{}
		if configure != nil {
			configure(agg)
		}
		if err := agg.Init(store, cache.New(store)); err != nil {
			t.Fatalf("Init: %v", err)
		}
		return agg, store
	}
	seed := func(t *testing.T, agg *Aggregator) {
		t.Helper()
		if _, err := agg.HandleBlockPin(makeContentPin(contentPinOpts{
			PinId: "proto-src:i0", Path: PathMetaProtocol, Operation: OperationCreate, Timestamp: 1710000100,
			GlobalMetaId: "gid-owner", MetaId: "meta-owner", Address: "addr-owner",
			ContentType: "application/json",
			ContentBody: []byte(`{"title":"P","path":"/protocols/toggle","protocolName":"toggle"}`),
		})); err != nil {
			t.Fatalf("seed create: %v", err)
		}
	}
	forge := func(t *testing.T, agg *Aggregator) {
		t.Helper()
		if _, err := agg.HandleBlockPin(makeContentPin(contentPinOpts{
			PinId: "proto-forge:i0", Path: PathMetaProtocol + "@proto-src:i0", Operation: OperationModify,
			Timestamp: 1710000200, GlobalMetaId: "gid-forger", MetaId: "meta-forger", Address: "addr-forger",
			ContentType: "application/json",
			ContentBody: []byte(`{"title":"Hijacked","path":"/protocols/toggle","protocolName":"toggle"}`),
		})); err != nil {
			t.Fatalf("forged modify: %v", err)
		}
	}

	// Default (no setter call): the check is on.
	agg, _ := newAgg(t, nil)
	seed(t, agg)
	forge(t, agg)
	rec, _ := agg.loadRecord("mvc", PathMetaProtocol, "proto-src:i0")
	if rec == nil || rec.CurrentPinId != "proto-src:i0" {
		t.Fatalf("default: forged modify joined the chain: %+v", rec)
	}

	// Explicitly disabled: the forged modify merges (pre-fix behavior).
	aggOff, _ := newAgg(t, func(a *Aggregator) { a.SetProtocolOwnerBoundModifies(false) })
	seed(t, aggOff)
	forge(t, aggOff)
	rec, _ = aggOff.loadRecord("mvc", PathMetaProtocol, "proto-src:i0")
	if rec == nil || rec.CurrentPinId != "proto-forge:i0" {
		t.Fatalf("disabled: forged modify did not join the chain: %+v", rec)
	}
}
