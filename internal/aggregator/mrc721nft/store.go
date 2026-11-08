package mrc721nft

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cockroachdb/pebble"

	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// Pebble key families of the mrc721nft namespace. Layout follows the
// issuer-only protocol revision (docs/mrc721-protocol-revision-issuer-only.md):
// an album's identity is its collectionid (the collection_desc pinid) with
// (collectionName, issuer address) as the human-level pair — collection
// names are NOT unique and confer no ownership, so every multi-record index
// is keyed by the album identity, never by the name alone:
//
//	c:{collectionName}&{createAddress}                    → Collection JSON (album main table)
//	cp:{collectionDescPinId}                              → "{collectionName}&{createAddress}" (collectionid reverse lookup)
//	i:{itemPinId}                                         → Item JSON (NFT main table)
//	ci:{collectionDescPinId}{seq %020d}{itemPinId}        → empty (in-album mint order; keyed by collectionid —
//	                                                        fixed-length hex, no separator ambiguity)
//	oi:{ownerAddress}&{collectionDescPinId}&{itemPinId}   → empty (holder index, re-keyed on transfer)
//	th:{itemPinId}&{height %020d}{txid}                   → ItemTransfer JSON (provenance)
//	pt:{itemPinId}                                        → PendingTransfer JSON (unconfirmed spend, advisory)
//	p:{collectionName}&{issuerAddress}&{pinId}            → PendingEntry JSON (out-of-order staging, per (name, issuer))
//
// The '&' separator inside composite keys assumes collection names never
// contain '&' — the same assumption man-p2p's schema makes for its oi:/p:
// families.
const (
	keyCollection   = "c:"
	keyCollByPinId  = "cp:"
	keyItem         = "i:"
	keyCollItem     = "ci:"
	keyOwnerItem    = "oi:"
	keyItemTransfer = "th:"
	keyPendingTr    = "pt:"
	keyPending      = "p:"
)

// Collection is the registered /nft/mrc721/{name}/collection_desc record.
// Field names and JSON tags mirror man-p2p's Mrc721Collection exactly (the
// odd lowercase "pinid"/"totalnum" spellings are part of the manapi wire
// contract). The record is uniquely identified by (CollectionName,
// CreateAddress); PinId (the collectionid) is the primary index key.
type Collection struct {
	CollectionName   string `json:"collectionName"`
	Name             string `json:"name"`
	TotalSupply      int64  `json:"totalSupply"` // -1 = unlimited
	RoyaltyRate      int    `json:"royaltyRate"`
	Desc             string `json:"desc"`
	Website          string `json:"website"`
	Cover            string `json:"cover"`
	Metadata         string `json:"metadata"`
	PinId            string `json:"pinid"` // collectionid = collection_desc pin id
	CreateAddress    string `json:"createAddress"`
	CreateMetaId     string `json:"createMetaId"`
	CreateTime       int64  `json:"createTime"`
	GenesisChainName string `json:"genesisChainName"`
	GenesisHeight    int64  `json:"genesisHeight"`
	GenesisTx        string `json:"genesisTx"`
	TotalNum         int64  `json:"totalnum"`
	// ModifyHistory is the issuer's collection_desc modify version chain:
	// modify pin ids in ingest order, the last one is the live version; a
	// repeated pin id is idempotent and never appended twice.
	ModifyHistory []string `json:"modifyHistory,omitempty"`
}

// Item is one minted /nft/mrc721/{collection}/{item} entry. JSON tags mirror
// man-p2p's Mrc721Item. Under the issuer-only revision every indexed item is
// minted by its album's issuer, so CreatorMinted is true by construction
// (field kept for wire compatibility).
type Item struct {
	ItemPinId        string `json:"itemPinId"`
	CollectionName   string `json:"collectionName"`
	CollectionPinId  string `json:"collectionPinId"`
	Number           int64  `json:"number"` // in-album mint number, 1-based
	Name             string `json:"name"`
	Desc             string `json:"desc"`
	Cover            string `json:"cover"`
	Metadata         string `json:"metadata"`
	OwnerAddress     string `json:"ownerAddress"`
	OwnerMetaId      string `json:"ownerMetaId"`
	CreatorMinted    bool   `json:"creatorMinted"`
	Burned           bool   `json:"burned"` // meltdown marker; excluded from holder views
	ContentType      string `json:"contentType"`
	ContentSummary   string `json:"contentSummary"`
	GenesisChainName string `json:"genesisChainName"`
	GenesisHeight    int64  `json:"genesisHeight"`
	GenesisTx        string `json:"genesisTx"`
	GenesisFee       int64  `json:"genesisFee"`
	Output           string `json:"output"`
	OutputValue      int64  `json:"outputValue"`
	CreateTime       int64  `json:"createTime"`
	TransferCount    int64  `json:"transferCount"`
}

// ItemTransfer is one confirmed ownership change of an item. JSON tags
// mirror man-p2p's Mrc721ItemTransfer.
type ItemTransfer struct {
	ItemPinId   string `json:"itemPinId"`
	FromAddress string `json:"fromAddress"`
	ToAddress   string `json:"toAddress"`
	ChainName   string `json:"chainName"`
	Height      int64  `json:"height"`
	TxId        string `json:"txId"`
	Time        int64  `json:"time"`
}

// ItemDescRef is one item reference inside an item_desc payload. The
// lowercase "pinid" tag mirrors man-p2p's Mrc721ItemDescRef (the bootstrap
// replay feeds these structs straight from upstream JSON). Metadata decodes
// tolerantly (R-F-11): the protocol defines it as arbitrary data, so the
// raw JSON is kept as-is and rendered through MetadataText — a typed string
// here made one object-typed metadata value fail the whole payload decode
// and drop the entire pin.
type ItemDescRef struct {
	PinId    string          `json:"pinid"`
	Name     string          `json:"name"`
	Desc     string          `json:"desc"`
	Cover    string          `json:"cover"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// MetadataText renders the ref's metadata in its stored string form: a JSON
// string verbatim, any other JSON value as its compact JSON text — the same
// rule stringField applies to collection metadata. Absent, null, or
// undecodable values yield "".
func (r ItemDescRef) MetadataText() string {
	if len(r.Metadata) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(r.Metadata, &s); err == nil {
		return s
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, r.Metadata); err != nil {
		return ""
	}
	if text := buf.String(); text != "null" {
		return text
	}
	return ""
}

// PendingEntry stages a mint / item_desc / collection_desc-modify pin that
// arrived before its (collectionName, issuer address) album was registered;
// drained on registration. Under issuer-only attribution every staged pin
// belongs to exactly one would-be album: the one named by the path whose
// issuer is the pin's own author.
type PendingEntry struct {
	Kind   string         `json:"kind"` // "mint" | "desc" | "colmod"
	PinId  string         `json:"pinId"`
	Mint   *PendingMint   `json:"mint,omitempty"`
	Desc   *PendingDesc   `json:"desc,omitempty"`
	ColMod *PendingColMod `json:"colmod,omitempty"`
}

// PendingMint carries everything needed to rebuild an item on drain.
type PendingMint struct {
	ItemPinId        string `json:"itemPinId"`
	OwnerAddress     string `json:"ownerAddress"`
	OwnerMetaId      string `json:"ownerMetaId"`
	ContentType      string `json:"contentType"`
	ContentSummary   string `json:"contentSummary"`
	GenesisChainName string `json:"genesisChainName"`
	GenesisHeight    int64  `json:"genesisHeight"`
	GenesisTx        string `json:"genesisTx"`
	GenesisFee       int64  `json:"genesisFee"`
	Output           string `json:"output"`
	OutputValue      int64  `json:"outputValue"`
	CreateTime       int64  `json:"createTime"`
}

// PendingDesc stages the not-yet-applied item references of an item_desc
// payload: refs whose items have not minted yet (drained as they mint). Refs
// failing the issuer gate are dropped at apply time, never staged.
type PendingDesc struct {
	DescPinId string        `json:"descPinId"`
	Items     []ItemDescRef `json:"items"`
}

// PendingColMod stages a collection_desc modify for chronological replay
// after the collection registers.
type PendingColMod struct {
	ModifyPinId   string `json:"modifyPinId"`
	ModifyAddress string `json:"modifyAddress"`
	Content       string `json:"content"` // raw modify JSON
	Timestamp     int64  `json:"timestamp"`
}

// PendingTransfer is an unconfirmed spend of an NFT outpoint observed by the
// mempool scan (advisory read-side channel only). Once the spend confirms,
// the transfer hook rewrites ownership, records the history row and deletes
// this record; dropped/replaced txs are cleaned by the mempool reconcile.
// TxId uses the ledger txid convention (same source as the confirmed
// history); latest scan wins (RBF replacement overwrites the whole record).
type PendingTransfer struct {
	ItemPinId     string `json:"itemPinId"`
	ChainName     string `json:"chainName"`
	SpentOutpoint string `json:"spentOutpoint"` // spent current outpoint (pre-confirmation == item.Output)
	FromAddress   string `json:"fromAddress"`   // holder at spend time (== item.OwnerAddress)
	ToAddress     string `json:"toAddress"`     // recipient derived from the spend tx (empty when derivation fails)
	TxId          string `json:"txId"`          // spending txid
	FirstSeenAt   int64  `json:"firstSeenAt"`   // first-observed epoch seconds
}

func collectionKey(collectionName, createAddress string) string {
	return keyCollection + collectionName + "&" + createAddress
}

// splitCollectionComposite splits a cp: reverse-lookup value back into the
// (collectionName, createAddress) pair. The issuer address is always the
// last '&'-separated segment; the name may itself contain '&' (the same
// tolerance the key schema documents).
func splitCollectionComposite(composite string) (string, string) {
	sep := strings.LastIndex(composite, "&")
	if sep < 0 {
		return composite, ""
	}
	return composite[:sep], composite[sep+1:]
}

func collByPinIdKey(collectionDescPinId string) string {
	return keyCollByPinId + collectionDescPinId
}

func itemKey(itemPinId string) string {
	return keyItem + itemPinId
}

func collItemKey(collectionDescPinId string, seq int64, itemPinId string) string {
	return fmt.Sprintf("%s%s%020d%s", keyCollItem, collectionDescPinId, seq, itemPinId)
}

func ownerItemKey(ownerAddress, collectionDescPinId, itemPinId string) string {
	return fmt.Sprintf("%s%s&%s&%s", keyOwnerItem, ownerAddress, collectionDescPinId, itemPinId)
}

func itemTransferKey(itemPinId string, height int64, txId string) string {
	return fmt.Sprintf("%s%s&%020d%s", keyItemTransfer, itemPinId, height, txId)
}

func pendingKey(collectionName, issuerAddress, pinId string) string {
	return fmt.Sprintf("%s%s&%s&%s", keyPending, collectionName, issuerAddress, pinId)
}

func pendingTransferKey(itemPinId string) string {
	return keyPendingTr + itemPinId
}

// errStopIteration breaks PebbleStore prefix scans early; the scan helper
// swallows it so callers of the Iterate* methods see plain nil.
var errStopIteration = errBreakScan{}

type errBreakScan struct{}

func (errBreakScan) Error() string { return "stop iteration" }

// nftStore is the storage facade over the mrc721nft namespace. Key layout
// follows the issuer-only revision; the record shapes stay manapi-wire
// compatible.
type nftStore struct {
	store *storage.PebbleStore
}

// scan walks one key family; returning false from fn stops the walk
// cleanly (the sentinel never leaks to the caller).
func (s *nftStore) scan(prefix string, fn func(key, value []byte) bool) error {
	err := s.store.ScanPrefix(Namespace, []byte(prefix), func(key, value []byte) error {
		if !fn(key, value) {
			return errStopIteration
		}
		return nil
	})
	if errors.Is(err, errStopIteration) {
		return nil
	}
	return err
}

func (s *nftStore) put(key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.store.Set(Namespace, []byte(key), raw)
}

// get unmarshals the value at key into v; reports whether it existed.
func (s *nftStore) get(key string, v any) (bool, error) {
	raw, err := s.store.Get(Namespace, []byte(key))
	if errors.Is(err, pebble.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return true, err
	}
	return true, nil
}

// ---------- collection ----------

func (s *nftStore) SaveCollection(col *Collection) error {
	if err := s.put(collectionKey(col.CollectionName, col.CreateAddress), col); err != nil {
		return err
	}
	return s.store.Set(Namespace, []byte(collByPinIdKey(col.PinId)), []byte(col.CollectionName+"&"+col.CreateAddress))
}

// GetCollection fetches the album registered under (collectionName,
// issuerAddress) — the mint-attribution and modify-resolution lookup.
func (s *nftStore) GetCollection(collectionName, issuerAddress string) (*Collection, error) {
	var col Collection
	found, err := s.get(collectionKey(collectionName, issuerAddress), &col)
	if err != nil || !found {
		return nil, err
	}
	return &col, nil
}

func (s *nftStore) GetCollectionByPinId(collectionDescPinId string) (*Collection, error) {
	raw, err := s.store.Get(Namespace, []byte(collByPinIdKey(collectionDescPinId)))
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	name, address := splitCollectionComposite(string(raw))
	return s.GetCollection(name, address)
}

// GetEarliestCollectionByName resolves an ambiguous collection name to the
// earliest-created album under it (createTime, then genesis height, then
// pinid as tie-breakers) — the deterministic fallback for name-based query
// parameters under non-unique names.
func (s *nftStore) GetEarliestCollectionByName(collectionName string) (*Collection, error) {
	prefix := keyCollection + collectionName + "&"
	var earliest *Collection
	err := s.scan(prefix, func(_, value []byte) bool {
		var col Collection
		if err := json.Unmarshal(value, &col); err != nil {
			return true // skip corrupt rows
		}
		if earliest == nil ||
			col.CreateTime < earliest.CreateTime ||
			(col.CreateTime == earliest.CreateTime && (col.GenesisHeight < earliest.GenesisHeight ||
				(col.GenesisHeight == earliest.GenesisHeight && col.PinId < earliest.PinId))) {
			earliest = &col
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return earliest, nil
}

// ListCollections scans the album main table in key order (lexicographic by
// collectionName, then issuer address) with offset pagination — same shape
// as man-p2p's ListMrc721Collections, whose registry scale stays
// scan-friendly.
func (s *nftStore) ListCollections(offset, limit int64) ([]*Collection, int64, error) {
	var all []*Collection
	err := s.store.ScanPrefix(Namespace, []byte(keyCollection), func(_, value []byte) error {
		var col Collection
		if err := json.Unmarshal(value, &col); err == nil {
			all = append(all, &col)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	total := int64(len(all))
	if offset >= total {
		return []*Collection{}, total, nil
	}
	end := offset + limit
	if end > total || end <= 0 {
		end = total
	}
	return all[offset:end], total, nil
}

// ---------- item ----------

func (s *nftStore) SaveItem(item *Item) error {
	return s.put(itemKey(item.ItemPinId), item)
}

func (s *nftStore) GetItem(itemPinId string) (*Item, error) {
	var item Item
	found, err := s.get(itemKey(itemPinId), &item)
	if err != nil || !found {
		return nil, err
	}
	return &item, nil
}

// IterateItems walks the item main table (offline repair/assert paths).
func (s *nftStore) IterateItems(fn func(item *Item) bool) error {
	return s.scan(keyItem, func(_, value []byte) bool {
		var item Item
		if err := json.Unmarshal(value, &item); err != nil {
			return true // skip corrupt rows, same as man-p2p
		}
		return fn(&item)
	})
}

// AppendCollectionItem records an item's mint order inside its album,
// keyed by the collectionid.
func (s *nftStore) AppendCollectionItem(collectionDescPinId string, seq int64, itemPinId string) error {
	return s.store.Set(Namespace, []byte(collItemKey(collectionDescPinId, seq, itemPinId)), []byte{})
}

// IterateCollectionItems walks an album's items in mint order, yielding
// (seq, itemPinId). Mirrors man-p2p's IterateMrc721CollectionItems including
// its tolerance of malformed keys (seq parses to 0).
func (s *nftStore) IterateCollectionItems(collectionDescPinId string, fn func(seq int64, itemPinId string) bool) error {
	prefix := keyCollItem + collectionDescPinId
	return s.scan(prefix, func(key, _ []byte) bool {
		rest := strings.TrimPrefix(string(key), prefix)
		if len(rest) < 20 {
			return true
		}
		seq, _ := strconv.ParseInt(rest[:20], 10, 64)
		return fn(seq, rest[20:])
	})
}

// ---------- owner ----------

func (s *nftStore) PutOwnerItem(ownerAddress, collectionDescPinId, itemPinId string) error {
	return s.store.Set(Namespace, []byte(ownerItemKey(ownerAddress, collectionDescPinId, itemPinId)), []byte{})
}

func (s *nftStore) DeleteOwnerItem(ownerAddress, collectionDescPinId, itemPinId string) error {
	return s.store.Delete(Namespace, []byte(ownerItemKey(ownerAddress, collectionDescPinId, itemPinId)))
}

// IterateOwnerItems walks the items an address holds; an empty collection
// pinid iterates across all albums. The callback yields the holding album's
// collectionid.
func (s *nftStore) IterateOwnerItems(ownerAddress, collectionDescPinId string, fn func(collectionDescPinId, itemPinId string) bool) error {
	prefix := keyOwnerItem + ownerAddress + "&"
	if collectionDescPinId != "" {
		prefix += collectionDescPinId + "&"
	}
	return s.scan(prefix, func(key, _ []byte) bool {
		rest := strings.TrimPrefix(string(key), prefix)
		if collectionDescPinId != "" {
			return fn(collectionDescPinId, rest)
		}
		sep := strings.Index(rest, "&")
		if sep < 0 {
			return true
		}
		return fn(rest[:sep], rest[sep+1:])
	})
}

// ---------- transfer ----------

func (s *nftStore) SaveItemTransfer(tr *ItemTransfer) error {
	return s.put(itemTransferKey(tr.ItemPinId, tr.Height, tr.TxId), tr)
}

// GetItemTransfer fetches one history row by (itemPinId, height, txId) —
// the transfer hook's idempotency key.
func (s *nftStore) GetItemTransfer(itemPinId string, height int64, txId string) (*ItemTransfer, error) {
	var tr ItemTransfer
	found, err := s.get(itemTransferKey(itemPinId, height, txId), &tr)
	if err != nil || !found {
		return nil, err
	}
	return &tr, nil
}

// IterateItemTransfers walks an item's provenance in height order.
func (s *nftStore) IterateItemTransfers(itemPinId string, fn func(tr *ItemTransfer) bool) error {
	prefix := keyItemTransfer + itemPinId + "&"
	return s.scan(prefix, func(_, value []byte) bool {
		var tr ItemTransfer
		if err := json.Unmarshal(value, &tr); err != nil {
			return true
		}
		return fn(&tr)
	})
}

// ---------- pending (out-of-order self-healing) ----------

// PutPending stages an out-of-order pin for the (collectionName,
// issuerAddress) album it can only ever belong to under issuer-only
// attribution.
func (s *nftStore) PutPending(collectionName, issuerAddress string, entry *PendingEntry) error {
	return s.put(pendingKey(collectionName, issuerAddress, entry.PinId), entry)
}

// PopPending removes and returns all staged entries of one
// (collectionName, issuerAddress) album.
func (s *nftStore) PopPending(collectionName, issuerAddress string) ([]*PendingEntry, error) {
	var entries []*PendingEntry
	var keys [][]byte
	prefix := keyPending + collectionName + "&" + issuerAddress + "&"
	err := s.store.ScanPrefix(Namespace, []byte(prefix), func(key, value []byte) error {
		var entry PendingEntry
		if err := json.Unmarshal(value, &entry); err == nil {
			entries = append(entries, &entry)
		}
		// Copy the key: pebble iterator buffers are invalid after Next.
		keys = append(keys, append([]byte(nil), key...))
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if err := s.store.Delete(Namespace, key); err != nil {
			return entries, err
		}
	}
	return entries, nil
}

// ---------- pending transfer (mempool unconfirmed spend, advisory) ----------

func (s *nftStore) SavePendingTransfer(pt *PendingTransfer) error {
	return s.put(pendingTransferKey(pt.ItemPinId), pt)
}

func (s *nftStore) GetPendingTransfer(itemPinId string) (*PendingTransfer, error) {
	var pt PendingTransfer
	found, err := s.get(pendingTransferKey(itemPinId), &pt)
	if err != nil || !found {
		return nil, err
	}
	return &pt, nil
}

func (s *nftStore) DeletePendingTransfer(itemPinId string) error {
	return s.store.Delete(Namespace, []byte(pendingTransferKey(itemPinId)))
}

// IteratePendingTransfers walks all unconfirmed spend records (reconcile
// input; scale = transfers in flight).
func (s *nftStore) IteratePendingTransfers(fn func(pt *PendingTransfer) bool) error {
	return s.scan(keyPendingTr, func(_, value []byte) bool {
		var pt PendingTransfer
		if err := json.Unmarshal(value, &pt); err != nil {
			return true
		}
		return fn(&pt)
	})
}
