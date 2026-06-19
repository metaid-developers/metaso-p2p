package publishedcontent

import (
	"encoding/json"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
	"github.com/metaid-developers/metaso-p2p/pkg/idaddress"
)

func setupTestAggregator(t *testing.T) (*Aggregator, *storage.PebbleStore) {
	t.Helper()

	store := storage.NewPebbleStore(t.TempDir())
	cacheProvider := cache.New(store)
	agg := &Aggregator{}
	if err := agg.Init(store, cacheProvider); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return agg, store
}

func TestInitBackfillsHomepageMetaAppGlobalIndexes(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	if _, err := agg.HandleBlockPin(makeContentPin(contentPinOpts{
		PinId:        "metaapp-backfill:i0",
		Path:         PathMetaApp,
		Operation:    OperationCreate,
		ChainName:    "mvc",
		GlobalMetaId: "gid-user",
		MetaId:       "meta-user",
		Address:      "addr-user",
		Timestamp:    1710000300,
		ContentType:  "application/json",
		ContentBody:  []byte(`{"title":"Backfill MetaAPP"}`),
	})); err != nil {
		t.Fatalf("HandleBlockPin(metaapp): %v", err)
	}

	for _, key := range [][]byte{
		byGlobalKey(PathMetaApp, "gid-user", 1710000300, "mvc", "metaapp-backfill:i0"),
		homepageMetaAppsGlobalIdentityStateKey(),
	} {
		if err := store.Delete(Namespace, key); err != nil {
			t.Fatalf("delete %q: %v", string(key), err)
		}
	}

	reloaded := &Aggregator{}
	if err := reloaded.Init(store, cache.New(store)); err != nil {
		t.Fatalf("reloaded Init: %v", err)
	}

	if _, err := store.Get(Namespace, homepageMetaAppsGlobalIdentityStateKey()); err != nil {
		t.Fatalf("state marker missing after Init backfill: %v", err)
	}
	if _, err := store.Get(Namespace,
		byGlobalKey(PathMetaApp, "gid-user", 1710000300, "mvc", "metaapp-backfill:i0")); err != nil {
		t.Fatalf("metaapp global index missing after Init backfill: %v", err)
	}
}

func TestInitBackfillSkipsHiddenHomepageMetaApps(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	createPin := makeContentPin(contentPinOpts{
		PinId:        "metaapp-hidden-create:i0",
		Path:         PathMetaApp,
		Operation:    OperationCreate,
		ChainName:    "mvc",
		GlobalMetaId: "gid-user",
		MetaId:       "meta-user",
		Address:      "addr-user",
		Timestamp:    1710000400,
		ContentType:  "application/json",
		ContentBody:  []byte(`{"title":"Hidden MetaAPP"}`),
	})
	revokePin := makeContentPin(contentPinOpts{
		PinId:        "metaapp-hidden-revoke:i0",
		Path:         PathMetaApp + "@metaapp-hidden-create:i0",
		Operation:    OperationRevoke,
		ChainName:    "mvc",
		GlobalMetaId: "gid-user",
		MetaId:       "meta-user",
		Address:      "addr-user",
		Timestamp:    1710000500,
	})
	if _, err := agg.HandleBlockPin(createPin); err != nil {
		t.Fatalf("HandleBlockPin(create hidden metaapp seed): %v", err)
	}
	if _, err := agg.HandleBlockPin(revokePin); err != nil {
		t.Fatalf("HandleBlockPin(revoke hidden metaapp seed): %v", err)
	}

	hiddenIndexKey := byGlobalKey(PathMetaApp, "gid-user", 1710000500, "mvc", "metaapp-hidden-create:i0")
	for _, key := range [][]byte{
		hiddenIndexKey,
		homepageMetaAppsGlobalIdentityStateKey(),
	} {
		if err := store.Delete(Namespace, key); err != nil {
			t.Fatalf("delete %q: %v", string(key), err)
		}
	}

	reloaded := &Aggregator{}
	if err := reloaded.Init(store, cache.New(store)); err != nil {
		t.Fatalf("reloaded Init: %v", err)
	}

	if _, err := store.Get(Namespace, homepageMetaAppsGlobalIdentityStateKey()); err != nil {
		t.Fatalf("state marker missing after Init backfill: %v", err)
	}
	if _, err := store.Get(Namespace, hiddenIndexKey); err == nil {
		t.Fatalf("hidden metaapp global index was unexpectedly backfilled")
	}
}

type contentPinOpts struct {
	PinId          string
	Path           string
	Operation      string
	ChainName      string
	OriginalId     string
	Timestamp      int64
	Number         int64
	ContentBody    []byte
	ContentSummary string
	ContentType    string
	GlobalMetaId   string
	MetaId         string
	Address        string
	Host           string
}

func makeContentPin(opts contentPinOpts) *aggregator.PinInscription {
	if opts.Path == "" {
		opts.Path = PathSimpleBuzz
	}
	if opts.Operation == "" {
		opts.Operation = OperationCreate
	}
	if opts.ChainName == "" {
		opts.ChainName = "mvc"
	}
	if opts.GlobalMetaId == "" {
		opts.GlobalMetaId = "gid-user"
	}
	if opts.MetaId == "" {
		opts.MetaId = "meta-user"
	}
	if opts.Address == "" {
		opts.Address = "addr-user"
	}
	if opts.ContentType == "" {
		opts.ContentType = "text/plain"
	}
	return &aggregator.PinInscription{
		Id:             opts.PinId,
		Path:           opts.Path,
		Operation:      opts.Operation,
		ContentBody:    opts.ContentBody,
		ContentSummary: opts.ContentSummary,
		ContentType:    opts.ContentType,
		ChainName:      opts.ChainName,
		GlobalMetaId:   opts.GlobalMetaId,
		MetaId:         opts.MetaId,
		CreateMetaId:   opts.MetaId,
		Address:        opts.Address,
		CreateAddress:  opts.Address,
		Timestamp:      opts.Timestamp,
		Number:         opts.Number,
		OriginalId:     opts.OriginalId,
		Host:           opts.Host,
	}
}

func mustProcess(t *testing.T, agg *Aggregator, pin *aggregator.PinInscription) {
	t.Helper()
	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("HandleBlockPin(%s): %v", pin.Id, err)
	}
}

func mustProcessMempool(t *testing.T, agg *Aggregator, pin *aggregator.PinInscription) {
	t.Helper()
	if _, err := agg.HandleMempoolPin(pin); err != nil {
		t.Fatalf("HandleMempoolPin(%s): %v", pin.Id, err)
	}
}

func mustLoadRecord(t *testing.T, agg *Aggregator, chainName, protocolPath, sourcePinId string) *Record {
	t.Helper()
	rec, err := agg.loadRecord(chainName, protocolPath, sourcePinId)
	if err != nil {
		t.Fatalf("loadRecord(%s/%s/%s): %v", chainName, protocolPath, sourcePinId, err)
	}
	if rec == nil {
		t.Fatalf("expected record %s/%s/%s", chainName, protocolPath, sourcePinId)
	}
	return rec
}

func TestProcessCreateCanonicalizesAddressBackedGlobalMetaId(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	address := "1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za"
	canonicalGlobalMetaId := idaddress.EncodeGlobalMetaId(address, "mvc")
	if canonicalGlobalMetaId == "" {
		t.Fatal("EncodeGlobalMetaId returned empty")
	}
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:        "address-global-buzz:i0",
		GlobalMetaId: address,
		MetaId:       address,
		Address:      address,
		Timestamp:    1781252638,
		ContentBody:  []byte("address global buzz"),
	}))

	rec := mustLoadRecord(t, agg, "mvc", PathSimpleBuzz, "address-global-buzz:i0")
	if rec.PublisherGlobalMetaId != canonicalGlobalMetaId {
		t.Fatalf("PublisherGlobalMetaId = %q, want %q", rec.PublisherGlobalMetaId, canonicalGlobalMetaId)
	}

	result, err := agg.List(ListParams{
		ProtocolPath:          PathSimpleBuzz,
		PublisherGlobalMetaId: canonicalGlobalMetaId,
		Size:                  5,
	})
	if err != nil {
		t.Fatalf("List by canonical globalMetaId: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].SourcePinId != "address-global-buzz:i0" {
		t.Fatalf("List by canonical globalMetaId returned %+v, want address-global-buzz:i0", result.Items)
	}
}

func TestProcessCreateModifyRevokeFoldsCurrentRecord(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:       "buzz-create:i0",
		Operation:   OperationCreate,
		Timestamp:   1000,
		ContentBody: []byte("hello world"),
	}))
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:       "buzz-modify:i0",
		Path:        PathSimpleBuzz + "@buzz-create:i0",
		Operation:   OperationModify,
		Timestamp:   2000,
		ContentBody: []byte("edited world"),
	}))
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:       "buzz-revoke:i0",
		Path:        PathSimpleBuzz + "@buzz-modify:i0",
		Operation:   OperationRevoke,
		Timestamp:   3000,
		ContentBody: []byte("ignored"),
	}))

	rec := mustLoadRecord(t, agg, "mvc", PathSimpleBuzz, "buzz-create:i0")
	if rec.SourcePinId != "buzz-create:i0" {
		t.Fatalf("SourcePinId: got %q", rec.SourcePinId)
	}
	if rec.CurrentPinId != "buzz-revoke:i0" {
		t.Fatalf("CurrentPinId: got %q", rec.CurrentPinId)
	}
	if rec.Operation != OperationRevoke {
		t.Fatalf("Operation: got %q", rec.Operation)
	}
	if !rec.Hidden {
		t.Fatalf("revoked record should be hidden")
	}
	if rec.CreatedAt != 1000 || rec.UpdatedAt != 3000 {
		t.Fatalf("timestamps: createdAt=%d updatedAt=%d", rec.CreatedAt, rec.UpdatedAt)
	}
	if rec.PayloadText != "edited world" {
		t.Fatalf("revoke should preserve last exposed payload, got %q", rec.PayloadText)
	}

	result, err := agg.List(ListParams{
		ProtocolPath:          PathSimpleBuzz,
		PublisherGlobalMetaId: "gid-user",
		Size:                  5,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("revoked records should be hidden by default, got %d", len(result.Items))
	}
}

func TestMempoolCreateIsUpgradedByConfirmedBlockPin(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustProcessMempool(t, agg, makeContentPin(contentPinOpts{
		PinId:        "same-create:i0",
		Operation:    OperationCreate,
		Timestamp:    1000,
		Number:       11,
		ContentBody:  []byte("mempool body"),
		GlobalMetaId: "gid-old",
		MetaId:       "meta-old",
		Address:      "addr-old",
		Host:         "mempool-host",
	}))
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:        "same-create:i0",
		Operation:    OperationCreate,
		Timestamp:    2000,
		Number:       22,
		ContentBody:  []byte("confirmed body"),
		GlobalMetaId: "gid-new",
		MetaId:       "meta-new",
		Address:      "addr-new",
		Host:         "block-host",
	}))

	rec := mustLoadRecord(t, agg, "mvc", PathSimpleBuzz, "same-create:i0")
	if rec.IsMempool {
		t.Fatal("confirmed block replay should clear mempool state")
	}
	if rec.CreatedAt != 2000 || rec.UpdatedAt != 2000 {
		t.Errorf("confirmed create should replace timestamps, got createdAt=%d updatedAt=%d", rec.CreatedAt, rec.UpdatedAt)
	}
	if rec.SourceNumber != 22 || rec.CurrentNumber != 22 {
		t.Errorf("confirmed create should replace block numbers, got source=%d current=%d", rec.SourceNumber, rec.CurrentNumber)
	}
	if rec.SourceHost != "block-host" || rec.CurrentHost != "block-host" {
		t.Errorf("confirmed create should replace hosts, got source=%q current=%q", rec.SourceHost, rec.CurrentHost)
	}
	if rec.PublisherGlobalMetaId != "gid-new" || rec.PublisherMetaId != "meta-new" || rec.PublisherAddress != "addr-new" {
		t.Errorf("confirmed create should replace identity, got global=%q meta=%q address=%q", rec.PublisherGlobalMetaId, rec.PublisherMetaId, rec.PublisherAddress)
	}
	if rec.PayloadText != "confirmed body" {
		t.Errorf("confirmed create should replace payload, got %q", rec.PayloadText)
	}

	oldIdentity, err := agg.List(ListParams{
		ProtocolPath:          PathSimpleBuzz,
		PublisherGlobalMetaId: "gid-old",
		Size:                  5,
	})
	if err != nil {
		t.Fatalf("List old identity: %v", err)
	}
	if len(oldIdentity.Items) != 0 {
		t.Fatalf("old identity index should not list upgraded record, got %d item(s)", len(oldIdentity.Items))
	}

	newIdentity, err := agg.List(ListParams{
		ProtocolPath:          PathSimpleBuzz,
		PublisherGlobalMetaId: "gid-new",
		Size:                  5,
	})
	if err != nil {
		t.Fatalf("List new identity: %v", err)
	}
	if len(newIdentity.Items) != 1 || newIdentity.Items[0].SourcePinId != "same-create:i0" {
		t.Fatalf("new identity index should list upgraded record, got %+v", newIdentity.Items)
	}
}

func TestConfirmedCreatePreservesPendingMempoolModify(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustProcessMempool(t, agg, makeContentPin(contentPinOpts{
		PinId:        "source:i0",
		Operation:    OperationCreate,
		Timestamp:    100,
		Number:       11,
		ContentBody:  []byte("source mempool"),
		GlobalMetaId: "gid-old",
		MetaId:       "meta-old",
		Address:      "addr-old",
		Host:         "mempool-host",
	}))
	mustProcessMempool(t, agg, makeContentPin(contentPinOpts{
		PinId:        "modify:i0",
		Path:         PathSimpleBuzz + "@source:i0",
		Operation:    OperationModify,
		Timestamp:    300,
		Number:       33,
		ContentBody:  []byte("pending modify"),
		GlobalMetaId: "gid-pending",
		MetaId:       "meta-pending",
		Address:      "addr-pending",
		Host:         "modify-host",
	}))
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:        "source:i0",
		Operation:    OperationCreate,
		Timestamp:    200,
		Number:       22,
		ContentBody:  []byte("source confirmed"),
		GlobalMetaId: "gid-new",
		MetaId:       "meta-new",
		Address:      "addr-new",
		Host:         "block-host",
	}))

	rec := mustLoadRecord(t, agg, "mvc", PathSimpleBuzz, "source:i0")
	if rec.CurrentPinId != "modify:i0" {
		t.Fatalf("confirmed source replay should preserve pending current pin, got %q", rec.CurrentPinId)
	}
	if !rec.IsMempool {
		t.Fatal("pending modify should keep record in mempool state")
	}
	if rec.Operation != OperationModify || rec.Hidden {
		t.Fatalf("pending modify state not preserved: operation=%q hidden=%v", rec.Operation, rec.Hidden)
	}
	if rec.PayloadText != "pending modify" {
		t.Fatalf("pending modify payload not preserved: got %q", rec.PayloadText)
	}
	if rec.CreatedAt != 200 || rec.UpdatedAt != 300 {
		t.Fatalf("timestamps: createdAt=%d updatedAt=%d", rec.CreatedAt, rec.UpdatedAt)
	}
	if rec.SourceNumber != 22 || rec.SourceHost != "block-host" {
		t.Fatalf("source metadata not upgraded: number=%d host=%q", rec.SourceNumber, rec.SourceHost)
	}
	if rec.CurrentNumber != 33 || rec.CurrentPath != PathSimpleBuzz+"@source:i0" || rec.CurrentHost != "modify-host" {
		t.Fatalf("current metadata not preserved: number=%d path=%q host=%q", rec.CurrentNumber, rec.CurrentPath, rec.CurrentHost)
	}
	if rec.PublisherGlobalMetaId != "gid-new" || rec.PublisherMetaId != "meta-new" || rec.PublisherAddress != "addr-new" {
		t.Fatalf("confirmed source identity not applied: global=%q meta=%q address=%q", rec.PublisherGlobalMetaId, rec.PublisherMetaId, rec.PublisherAddress)
	}

	pendingIdentity, err := agg.List(ListParams{
		ProtocolPath:          PathSimpleBuzz,
		PublisherGlobalMetaId: "gid-pending",
		Size:                  5,
	})
	if err != nil {
		t.Fatalf("List pending identity: %v", err)
	}
	if len(pendingIdentity.Items) != 0 {
		t.Fatalf("pending modify identity index should not list confirmed source record, got %d item(s)", len(pendingIdentity.Items))
	}

	confirmedIdentity, err := agg.List(ListParams{
		ProtocolPath:          PathSimpleBuzz,
		PublisherGlobalMetaId: "gid-new",
		Size:                  5,
	})
	if err != nil {
		t.Fatalf("List confirmed identity: %v", err)
	}
	if len(confirmedIdentity.Items) != 1 {
		t.Fatalf("confirmed identity index should list preserved pending record, got %d item(s)", len(confirmedIdentity.Items))
	}
	if confirmedIdentity.Items[0].SourcePinId != "source:i0" || confirmedIdentity.Items[0].CurrentPinId != "modify:i0" {
		t.Fatalf("confirmed identity item mismatch: %+v", confirmedIdentity.Items[0])
	}
	if confirmedIdentity.Items[0].PayloadText != "pending modify" || !confirmedIdentity.Items[0].IsMempool {
		t.Fatalf("confirmed identity item should expose pending modify state: %+v", confirmedIdentity.Items[0])
	}
}

func TestConfirmedModifyPreservesNewerPendingMempoolModify(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:        "source:i0",
		Operation:    OperationCreate,
		Timestamp:    100,
		Number:       11,
		ContentBody:  []byte("source confirmed"),
		GlobalMetaId: "gid-source",
		MetaId:       "meta-source",
		Address:      "addr-source",
		Host:         "source-host",
	}))
	mustProcessMempool(t, agg, makeContentPin(contentPinOpts{
		PinId:        "modify-pending:i0",
		Path:         PathSimpleBuzz + "@source:i0",
		Operation:    OperationModify,
		Timestamp:    300,
		Number:       33,
		ContentBody:  []byte("pending modify"),
		GlobalMetaId: "gid-pending",
		MetaId:       "meta-pending",
		Address:      "addr-pending",
		Host:         "pending-host",
	}))
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:        "modify-confirmed:i0",
		Path:         PathSimpleBuzz + "@source:i0",
		Operation:    OperationModify,
		Timestamp:    200,
		Number:       22,
		ContentBody:  []byte("older confirmed modify"),
		GlobalMetaId: "gid-confirmed",
		MetaId:       "meta-confirmed",
		Address:      "addr-confirmed",
		Host:         "confirmed-host",
	}))

	rec := mustLoadRecord(t, agg, "mvc", PathSimpleBuzz, "source:i0")
	if rec.CurrentPinId != "modify-pending:i0" {
		t.Fatalf("confirmed modify replay should preserve pending current pin, got %q", rec.CurrentPinId)
	}
	if !rec.IsMempool {
		t.Fatal("pending modify should keep record in mempool state")
	}
	if rec.Operation != OperationModify || rec.Hidden {
		t.Fatalf("pending modify state not preserved: operation=%q hidden=%v", rec.Operation, rec.Hidden)
	}
	if rec.PayloadText != "pending modify" || !rec.PayloadExposed {
		t.Fatalf("pending modify payload not preserved: text=%q exposed=%v", rec.PayloadText, rec.PayloadExposed)
	}
	if rec.CreatedAt != 100 || rec.UpdatedAt != 300 {
		t.Fatalf("timestamps: createdAt=%d updatedAt=%d", rec.CreatedAt, rec.UpdatedAt)
	}
	if rec.SourceNumber != 11 || rec.SourceHost != "source-host" {
		t.Fatalf("source metadata changed: number=%d host=%q", rec.SourceNumber, rec.SourceHost)
	}
	if rec.CurrentNumber != 33 || rec.CurrentPath != PathSimpleBuzz+"@source:i0" || rec.CurrentHost != "pending-host" {
		t.Fatalf("current metadata not preserved: number=%d path=%q host=%q", rec.CurrentNumber, rec.CurrentPath, rec.CurrentHost)
	}
	if rec.PublisherGlobalMetaId != "gid-confirmed" || rec.PublisherMetaId != "meta-confirmed" || rec.PublisherAddress != "addr-confirmed" {
		t.Fatalf("confirmed modify identity not applied: global=%q meta=%q address=%q", rec.PublisherGlobalMetaId, rec.PublisherMetaId, rec.PublisherAddress)
	}

	pendingIdentity, err := agg.List(ListParams{
		ProtocolPath:          PathSimpleBuzz,
		PublisherGlobalMetaId: "gid-pending",
		Size:                  5,
	})
	if err != nil {
		t.Fatalf("List pending identity: %v", err)
	}
	if len(pendingIdentity.Items) != 0 {
		t.Fatalf("pending modify identity index should not list confirmed modify record, got %d item(s)", len(pendingIdentity.Items))
	}

	confirmedIdentity, err := agg.List(ListParams{
		ProtocolPath:          PathSimpleBuzz,
		PublisherGlobalMetaId: "gid-confirmed",
		Size:                  5,
	})
	if err != nil {
		t.Fatalf("List confirmed identity: %v", err)
	}
	if len(confirmedIdentity.Items) != 1 {
		t.Fatalf("confirmed modify identity index should list preserved pending record, got %d item(s)", len(confirmedIdentity.Items))
	}
	if confirmedIdentity.Items[0].SourcePinId != "source:i0" || confirmedIdentity.Items[0].CurrentPinId != "modify-pending:i0" {
		t.Fatalf("confirmed modify identity item mismatch: %+v", confirmedIdentity.Items[0])
	}
	if confirmedIdentity.Items[0].PayloadText != "pending modify" || !confirmedIdentity.Items[0].IsMempool {
		t.Fatalf("confirmed modify identity item should expose pending modify state: %+v", confirmedIdentity.Items[0])
	}
}

func TestConfirmedRevokePreservesNewerPendingMempoolModify(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:        "source:i0",
		Operation:    OperationCreate,
		Timestamp:    100,
		Number:       11,
		ContentBody:  []byte("source confirmed"),
		GlobalMetaId: "gid-source",
		MetaId:       "meta-source",
		Address:      "addr-source",
		Host:         "source-host",
	}))
	mustProcessMempool(t, agg, makeContentPin(contentPinOpts{
		PinId:        "modify-pending:i0",
		Path:         PathSimpleBuzz + "@source:i0",
		Operation:    OperationModify,
		Timestamp:    300,
		Number:       33,
		ContentBody:  []byte("pending modify"),
		GlobalMetaId: "gid-pending",
		MetaId:       "meta-pending",
		Address:      "addr-pending",
		Host:         "pending-host",
	}))
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:        "revoke-confirmed:i0",
		Path:         PathSimpleBuzz + "@source:i0",
		Operation:    OperationRevoke,
		Timestamp:    200,
		Number:       22,
		ContentType:  "application/json",
		GlobalMetaId: "gid-source",
		MetaId:       "meta-source",
		Address:      "addr-source",
		Host:         "confirmed-host",
	}))

	rec := mustLoadRecord(t, agg, "mvc", PathSimpleBuzz, "source:i0")
	if rec.CurrentPinId != "modify-pending:i0" {
		t.Fatalf("confirmed revoke replay should preserve pending current pin, got %q", rec.CurrentPinId)
	}
	if !rec.IsMempool {
		t.Fatal("pending modify should keep record in mempool state")
	}
	if rec.Operation != OperationModify || rec.Hidden {
		t.Fatalf("pending modify state not preserved: operation=%q hidden=%v", rec.Operation, rec.Hidden)
	}
	if rec.PayloadText != "pending modify" || !rec.PayloadExposed {
		t.Fatalf("pending modify payload not preserved: text=%q exposed=%v", rec.PayloadText, rec.PayloadExposed)
	}
	if rec.UpdatedAt != 300 || rec.CurrentNumber != 33 || rec.CurrentHost != "pending-host" {
		t.Fatalf("pending current metadata not preserved: updatedAt=%d currentNumber=%d host=%q", rec.UpdatedAt, rec.CurrentNumber, rec.CurrentHost)
	}
	if rec.ContentType != "text/plain" {
		t.Fatalf("pending content type should be preserved, got %q", rec.ContentType)
	}
}

func TestConfirmedModifySamePendingPinClearsMempoolState(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:       "source:i0",
		Operation:   OperationCreate,
		Timestamp:   100,
		Number:      11,
		ContentBody: []byte("source confirmed"),
	}))
	mustProcessMempool(t, agg, makeContentPin(contentPinOpts{
		PinId:       "modify-same:i0",
		Path:        PathSimpleBuzz + "@source:i0",
		Operation:   OperationModify,
		Timestamp:   200,
		Number:      22,
		ContentBody: []byte("pending modify"),
		Host:        "pending-host",
	}))
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:       "modify-same:i0",
		Path:        PathSimpleBuzz + "@source:i0",
		Operation:   OperationModify,
		Timestamp:   300,
		Number:      33,
		ContentBody: []byte("confirmed modify"),
		Host:        "confirmed-host",
	}))

	rec := mustLoadRecord(t, agg, "mvc", PathSimpleBuzz, "source:i0")
	if rec.CurrentPinId != "modify-same:i0" {
		t.Fatalf("same-pin confirmation should keep current pin, got %q", rec.CurrentPinId)
	}
	if rec.IsMempool {
		t.Fatal("same-pin confirmation should clear mempool state")
	}
	if rec.PayloadText != "confirmed modify" || rec.UpdatedAt != 300 {
		t.Fatalf("same-pin confirmation should apply confirmed current payload: text=%q updatedAt=%d", rec.PayloadText, rec.UpdatedAt)
	}
	if rec.CurrentNumber != 33 || rec.CurrentHost != "confirmed-host" {
		t.Fatalf("same-pin confirmation should apply confirmed metadata: number=%d host=%q", rec.CurrentNumber, rec.CurrentHost)
	}
}

func TestPayloadFallsBackToContentSummary(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:          "summary-create:i0",
		Operation:      OperationCreate,
		Timestamp:      1000,
		ContentBody:    []byte(" \n\t "),
		ContentSummary: `{"title":"from summary","count":2}`,
		ContentType:    "application/json",
	}))

	rec := mustLoadRecord(t, agg, "mvc", PathSimpleBuzz, "summary-create:i0")
	if !rec.PayloadExposed {
		t.Fatalf("expected fallback payload to be exposed")
	}
	if rec.PayloadText != "" {
		t.Fatalf("JSON object fallback should not populate PayloadText: %q", rec.PayloadText)
	}
	if rec.PayloadJSON == nil {
		t.Fatal("expected PayloadJSON from contentSummary fallback")
	}
	raw, _ := json.Marshal(rec.PayloadJSON)
	if string(raw) != `{"count":2,"title":"from summary"}` {
		t.Fatalf("PayloadJSON: %s", raw)
	}
}

func TestBinaryPayloadIsNotExposed(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:       "binary-create:i0",
		Operation:   OperationCreate,
		Timestamp:   1000,
		ContentType: "image/png",
		ContentBody: []byte{0x89, 'P', 'N', 'G', 0x00},
	}))

	rec := mustLoadRecord(t, agg, "mvc", PathSimpleBuzz, "binary-create:i0")
	if rec.PayloadExposed {
		t.Fatal("binary payload should not be exposed")
	}
	if rec.PayloadText != "" || rec.PayloadJSON != nil {
		t.Fatalf("binary payload leaked: text=%q json=%v", rec.PayloadText, rec.PayloadJSON)
	}
}
