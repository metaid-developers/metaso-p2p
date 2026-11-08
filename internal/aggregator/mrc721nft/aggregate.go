package mrc721nft

import (
	"encoding/json"
	"log"
	"sort"
	"strconv"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/pkg/idaddress"
)

// MRC-721 NFT aggregation, ported from man-p2p's man/mrc721.go and then
// revised per docs/mrc721-protocol-revision-issuer-only.md (the issuer-only
// protocol upgrade agreed among the protocol authors):
//
//   - An album's identity is its collectionid (the collection_desc pinid);
//     the human-level pair is (collection_name, issuer address). Names are
//     NOT unique: a collection_desc by a different address under the same
//     name defines a separate, independent album; per (name, issuer) only
//     the first collection_desc counts.
//   - Minting is issuer-only: a create pin under /nft/mrc721/{name}[/...]
//     (any non-desc path) is attributed to the same-name album whose
//     collection_desc author is the minter itself; pins by users without a
//     registered album under that name are staged and only ever commit to
//     an album registered by the same address. totalSupply thus counts the
//     issuer's own mints only.
//   - item_desc backfills item metadata by pinid reference (first per field
//     wins), gated: a reference applies only when the desc author equals the
//     referenced item's album issuer.
//   - collection_desc modify is issuer-only and forms a version chain;
//     transfer semantics are unchanged by the revision.

// TransferInfo mirrors man-p2p's pin.PinTransferInfo: the facts of one spent
// outpoint as derived by the chain adapter's transfer detection. Output and
// Location use the ledger txid convention for MVC.
type TransferInfo struct {
	Address     string // new owner address (empty on unresolvable burn)
	Output      string // "<txid>:<vout>" the NFT now sits at
	OutputValue int64  // sat value of that output
	Offset      uint64
	Location    string // spending txid
	FromAddress string
	IsMeltdown  bool
}

// ingestNftPin dispatches one confirmed pin into the MRC-721 aggregate.
// Operation semantics: only collection_desc accepts modify (issuer-only
// version chain); every other path only honours create; revoke/hide have no
// effect under /nft/mrc721. Bad pins are logged and skipped — they never
// fail the block pipeline.
func (a *Aggregator) ingestNftPin(pin *aggregator.PinInscription) {
	if pin == nil || a.db == nil || len(pin.Id) == 0 {
		return
	}
	// man-p2p's MVC adapter feeds OP_RETURN outputs only; witness-format MVC
	// pins are a separate MetaSo parse path and must stay out of the
	// aggregate so the pin universe (and every pinId) matches man-p2p's.
	if pin.ChainName == "mvc" && !pin.IsOpReturn {
		return
	}
	// man-p2p stores the ValidHostPath projection of the raw path: a
	// "<host>:/path" style path is indexed as the part after the colon.
	path := effectivePath(pin.Path)
	if !strings.HasPrefix(strings.ToLower(path), nftPathPrefix) {
		return
	}
	// collectionName comes from the path segment as stored (lowercase in
	// practice — the MVC adapter lowercases the raw path, same as man-p2p).
	segments := strings.Split(path, "/")
	if len(segments) < 4 || segments[3] == "" {
		return
	}
	collectionName := segments[3]
	op := ""
	if len(segments) > 4 {
		op = strings.ToLower(segments[4])
	}
	operation := strings.ToLower(strings.TrimSpace(pin.Operation))
	switch op {
	case "collection_desc":
		switch operation {
		case "create":
			a.collectionRegister(collectionName, pin)
		case "modify":
			a.collectionModify(collectionName, pin)
		}
	case "item_desc":
		if operation == "create" {
			a.itemDescHandle(collectionName, pin)
		}
	default:
		if operation == "create" {
			a.mintHandle(collectionName, pin)
		}
	}
}

// effectivePath mirrors man-p2p's pin.ValidHostPath projection: a path with
// a "<host>:" prefix is indexed as the part after the colon.
func effectivePath(path string) string {
	if parts := strings.SplitN(path, ":", 2); len(parts) == 2 {
		return parts[1]
	}
	return path
}

// globalMetaIdByAddress converts a chain address to the ecosystem's global
// metaid (IDAddress format, pkg/idaddress — the same convention man-p2p's
// common.ConvertToGlobalMetaId applies to its GlobalMetaId fields).
// Unconvertible addresses (invalid/unknown format) yield "".
//
// NOTE: this deliberately diverges from man-p2p's MRC-721 DTOs, whose
// metaid/createMetaId/ownerMetaId values carry the legacy sha256(address)
// hex ("legacy metaid" — sunset usage). Downstream consumers of this
// surface use global metaids, so the aggregate derives them from the
// address locally, in both the live and the bootstrap paths.
func globalMetaIdByAddress(address string) string {
	if address == "" {
		return ""
	}
	idAddr, err := idaddress.ConvertFromBitcoin(address)
	if err != nil {
		log.Printf("[WARN] mrc721nft: convert address %s to global metaid: %v", address, err)
		return ""
	}
	return idAddr
}

func nftChainName(pin *aggregator.PinInscription) string {
	if pin.ChainName != "" {
		return pin.ChainName
	}
	return fallbackChainName
}

// maxSummaryBytes caps the stored content summary (same cap as man-p2p's
// pinListSummary).
const maxSummaryBytes = 4096

func pinListSummary(body []byte) string {
	if len(body) > maxSummaryBytes {
		return string(body[:maxSummaryBytes])
	}
	return string(body)
}

// ---------- collection ----------

// collectionRegister applies one collection_desc create: per
// (collection_name, issuer address) only the first counts; a desc by a
// different address under the same name is a separate, independent album.
func (a *Aggregator) collectionRegister(collectionName string, pin *aggregator.PinInscription) {
	existing, err := a.db.GetCollection(collectionName, pin.Address)
	if err != nil {
		log.Printf("[mrc721nft] get collection %s/%s: %v", collectionName, pin.Address, err)
		return
	}
	if existing != nil {
		return // first collection_desc per (name, issuer) wins
	}
	content, err := parseContent(pin.ContentBody)
	if err != nil {
		log.Printf("WARNING: mrc721nft collection_desc %s invalid content: %v", pin.Id, err)
		return
	}
	col := &Collection{
		CollectionName:   collectionName,
		PinId:            pin.Id,
		CreateAddress:    pin.Address,
		CreateMetaId:     globalMetaIdByAddress(pin.Address),
		CreateTime:       pin.Timestamp,
		GenesisChainName: nftChainName(pin),
		GenesisHeight:    pin.GenesisHeight,
		GenesisTx:        pin.GenesisTransaction,
	}
	col.Name = stringField(content, "name")
	col.Desc = stringField(content, "desc")
	col.Website = stringField(content, "website")
	col.Cover = stringField(content, "cover")
	col.Metadata = stringField(content, "metadata")
	// totalSupply defaults to -1, royaltyRate to 0 (per the official doc).
	col.TotalSupply = -1
	if raw, ok := lookupCI(content, "totalsupply"); ok {
		v, err := parseInt64(raw)
		if err != nil {
			log.Printf("WARNING: mrc721nft collection_desc %s invalid totalSupply: %v", pin.Id, err)
			return
		}
		col.TotalSupply = v
	}
	if raw, ok := lookupCI(content, "royaltyrate"); ok {
		v, err := parseInt64(raw)
		if err != nil {
			log.Printf("WARNING: mrc721nft collection_desc %s invalid royaltyRate: %v", pin.Id, err)
			return
		}
		col.RoyaltyRate = int(v)
	}
	if col.TotalSupply < -1 || col.TotalSupply > 1000000000000 {
		log.Printf("WARNING: mrc721nft collection_desc %s totalSupply out of range: %d", pin.Id, col.TotalSupply)
		return
	}
	if col.RoyaltyRate < 0 || col.RoyaltyRate > 20 {
		log.Printf("WARNING: mrc721nft collection_desc %s royaltyRate out of range: %d", pin.Id, col.RoyaltyRate)
		return
	}
	if err := a.db.SaveCollection(col); err != nil {
		log.Printf("WARNING: mrc721nft save collection %s/%s: %v", collectionName, pin.Address, err)
		return
	}
	// Registered: replay this issuer's staged pins (mints first, then descs,
	// then colmods in chronological order).
	a.drainPending(collectionName, pin.Address)
}

// ---------- collection_desc modify (issuer-only version chain) ----------

func (a *Aggregator) collectionModify(collectionName string, pin *aggregator.PinInscription) {
	col, err := a.db.GetCollection(collectionName, pin.Address)
	if err != nil {
		log.Printf("[mrc721nft] get collection %s/%s: %v", collectionName, pin.Address, err)
		return
	}
	if col == nil {
		// Album not registered yet: stage it, replayed in time order after
		// registration. Only ever replayed to the album of the same issuer.
		if err := a.db.PutPending(collectionName, pin.Address, &PendingEntry{
			Kind: "colmod", PinId: pin.Id,
			ColMod: &PendingColMod{
				ModifyPinId:   pin.Id,
				ModifyAddress: pin.Address,
				Content:       string(pin.ContentBody),
				Timestamp:     pin.Timestamp,
			},
		}); err != nil {
			log.Printf("WARNING: mrc721nft stage colmod %s: %v", pin.Id, err)
		}
		return
	}
	a.collectionModifyCommit(collectionName, col, pin.Id, pin.Address, pin.ContentBody)
}

// collectionModifyCommit applies one collection_desc modify: issuer-only;
// idempotent by modify pin id; display fields are overwritten wholesale and
// totalSupply/royaltyRate, when present, go through the same range
// validation (an invalid pin is ignored entirely). Lowering totalSupply does
// not revoke already-minted items — it only gates future mints.
func (a *Aggregator) collectionModifyCommit(collectionName string, col *Collection, modifyPinId, modifyAddress string, contentBody []byte) bool {
	if modifyAddress != col.CreateAddress {
		log.Printf("WARNING: mrc721nft collection modify %s rejected: %s is not issuer of %s (%s)", modifyPinId, modifyAddress, collectionName, col.CreateAddress)
		return false
	}
	for _, id := range col.ModifyHistory {
		if id == modifyPinId {
			return true // idempotent: dual-path delivery / replay never double-applies
		}
	}
	content, err := parseContent(contentBody)
	if err != nil {
		log.Printf("WARNING: mrc721nft collection modify %s invalid content: %v", modifyPinId, err)
		return false
	}
	newTotalSupply := col.TotalSupply
	if raw, ok := lookupCI(content, "totalsupply"); ok {
		v, err := parseInt64(raw)
		if err != nil {
			log.Printf("WARNING: mrc721nft collection modify %s invalid totalSupply: %v", modifyPinId, err)
			return false
		}
		newTotalSupply = v
	}
	newRoyaltyRate := col.RoyaltyRate
	if raw, ok := lookupCI(content, "royaltyrate"); ok {
		v, err := parseInt64(raw)
		if err != nil {
			log.Printf("WARNING: mrc721nft collection modify %s invalid royaltyRate: %v", modifyPinId, err)
			return false
		}
		newRoyaltyRate = int(v)
	}
	if newTotalSupply < -1 || newTotalSupply > 1000000000000 {
		log.Printf("WARNING: mrc721nft collection modify %s totalSupply out of range: %d", modifyPinId, newTotalSupply)
		return false
	}
	if newRoyaltyRate < 0 || newRoyaltyRate > 20 {
		log.Printf("WARNING: mrc721nft collection modify %s royaltyRate out of range: %d", modifyPinId, newRoyaltyRate)
		return false
	}
	if _, ok := lookupCI(content, "name"); ok {
		col.Name = stringField(content, "name")
	}
	if _, ok := lookupCI(content, "desc"); ok {
		col.Desc = stringField(content, "desc")
	}
	if _, ok := lookupCI(content, "website"); ok {
		col.Website = stringField(content, "website")
	}
	if _, ok := lookupCI(content, "cover"); ok {
		col.Cover = stringField(content, "cover")
	}
	if _, ok := lookupCI(content, "metadata"); ok {
		col.Metadata = stringField(content, "metadata")
	}
	col.TotalSupply = newTotalSupply
	col.RoyaltyRate = newRoyaltyRate
	col.ModifyHistory = append(col.ModifyHistory, modifyPinId)
	if err := a.db.SaveCollection(col); err != nil {
		log.Printf("WARNING: mrc721nft save collection %s modify: %v", collectionName, err)
		return false
	}
	return true
}

// ---------- item_desc ----------

// itemDescHandle applies one item_desc create. The desc's references are
// gated: a ref applies only when the desc author equals the referenced
// item's album issuer — nobody can describe someone else's items.
func (a *Aggregator) itemDescHandle(collectionName string, pin *aggregator.PinInscription) {
	refs, ok := parseItemDescRefs(pin.ContentBody)
	if !ok {
		return
	}
	col, err := a.db.GetCollection(collectionName, pin.Address)
	if err != nil {
		log.Printf("[mrc721nft] get collection %s/%s: %v", collectionName, pin.Address, err)
		return
	}
	if col == nil {
		// The author's album under this name is not registered: stage the
		// parsed refs, replayed after registration (and only ever applied to
		// items of that same author's albums).
		if err := a.db.PutPending(collectionName, pin.Address, &PendingEntry{
			Kind: "desc", PinId: pin.Id,
			Desc: &PendingDesc{DescPinId: pin.Id, Items: refs},
		}); err != nil {
			log.Printf("WARNING: mrc721nft stage desc %s: %v", pin.Id, err)
		}
		return
	}
	// Gate-passing refs whose items have not minted yet stay staged.
	a.applyDescRefs(collectionName, pin.Id, pin.Address, refs)
}

// applyDescRefs backfills item_desc references into indexed items (first
// per field wins), gated by the issuer rule: a ref applies only when the
// desc author equals the referenced item's album issuer. Refs whose items
// have not minted yet are staged for replay; refs failing the issuer gate
// are dropped with a log line.
func (a *Aggregator) applyDescRefs(collectionName, descPinId, issuerAddress string, refs []ItemDescRef) {
	var waiting []ItemDescRef
	for _, ref := range refs {
		if ref.PinId == "" {
			continue
		}
		item, err := a.db.GetItem(ref.PinId)
		if err != nil {
			log.Printf("[mrc721nft] get item %s: %v", ref.PinId, err)
			continue
		}
		if item == nil {
			waiting = append(waiting, ref)
			continue
		}
		// Issuer gate: the desc author must be the referenced item's album
		// issuer. The item's album is fixed by its collectionid.
		itemCol, err := a.db.GetCollectionByPinId(item.CollectionPinId)
		if err != nil {
			log.Printf("[mrc721nft] get item collection %s: %v", item.CollectionPinId, err)
			continue
		}
		if itemCol == nil {
			log.Printf("WARNING: mrc721nft item_desc %s: item %s references missing collection %s", descPinId, ref.PinId, item.CollectionPinId)
			continue
		}
		if itemCol.CreateAddress != issuerAddress {
			log.Printf("WARNING: mrc721nft item_desc %s rejected for item %s: %s is not issuer of %s (%s)", descPinId, ref.PinId, issuerAddress, itemCol.CollectionName, itemCol.CreateAddress)
			continue
		}
		changed := false
		if item.Name == "" && ref.Name != "" {
			item.Name, changed = ref.Name, true
		}
		if item.Desc == "" && ref.Desc != "" {
			item.Desc, changed = ref.Desc, true
		}
		if item.Cover == "" && ref.Cover != "" {
			item.Cover, changed = ref.Cover, true
		}
		refMetadata := ref.MetadataText()
		if item.Metadata == "" && refMetadata != "" {
			item.Metadata, changed = refMetadata, true
		}
		if changed {
			if err := a.db.SaveItem(item); err != nil {
				log.Printf("WARNING: mrc721nft update item %s desc: %v", ref.PinId, err)
			}
		}
	}
	if len(waiting) > 0 {
		if err := a.db.PutPending(collectionName, issuerAddress, &PendingEntry{
			Kind: "desc", PinId: descPinId,
			Desc: &PendingDesc{DescPinId: descPinId, Items: waiting},
		}); err != nil {
			log.Printf("WARNING: mrc721nft stage desc %s: %v", descPinId, err)
		}
	}
}

// parseItemDescRefs decodes one item_desc payload ref by ref (R-F-11): the
// protocol defines metadata as arbitrary data, so one non-string metadata
// value must not erase the whole pin. The payload is rejected (ok=false)
// only when it is unreadable as a whole (invalid JSON or a non-array items
// field); a malformed individual ref is logged and skipped, the surviving
// refs are kept.
func parseItemDescRefs(contentBody []byte) ([]ItemDescRef, bool) {
	content, err := parseContent(contentBody)
	if err != nil {
		log.Printf("WARNING: mrc721nft item_desc invalid content: %v", err)
		return nil, false
	}
	raw, ok := lookupCI(content, "items")
	if !ok {
		return nil, true
	}
	var rawRefs []json.RawMessage
	if err := json.Unmarshal(raw, &rawRefs); err != nil {
		log.Printf("WARNING: mrc721nft item_desc invalid items array: %v", err)
		return nil, false
	}
	refs := make([]ItemDescRef, 0, len(rawRefs))
	for i, rawRef := range rawRefs {
		var ref ItemDescRef
		if err := json.Unmarshal(rawRef, &ref); err != nil {
			log.Printf("WARNING: mrc721nft item_desc ref %d skipped (malformed): %v", i, err)
			continue
		}
		refs = append(refs, ref)
	}
	return refs, true
}

// ---------- mint ----------

// mintHandle applies one mint create under issuer-only attribution: the pin
// belongs to the same-name album whose issuer is the minter; a user without
// a registered album under that name never contributes to anyone's album.
func (a *Aggregator) mintHandle(collectionName string, pin *aggregator.PinInscription) {
	col, err := a.db.GetCollection(collectionName, pin.Address)
	if err != nil {
		log.Printf("[mrc721nft] get collection %s/%s: %v", collectionName, pin.Address, err)
		return
	}
	mint := &PendingMint{
		ItemPinId:        pin.Id,
		OwnerAddress:     pin.Address,
		OwnerMetaId:      globalMetaIdByAddress(pin.Address),
		ContentType:      pin.ContentType,
		ContentSummary:   pinListSummary(pin.ContentBody),
		GenesisChainName: nftChainName(pin),
		GenesisHeight:    pin.GenesisHeight,
		TxIndex:          pin.TxIndex,
		OutputIndex:      pinOutputIndex(pin.Id),
		GenesisTx:        pin.GenesisTransaction,
		// MetaSo's PinInscription carries no genesis fee; the field exists in
		// the record shape for manapi parity and is never served.
		GenesisFee:  0,
		Output:      pin.Output,
		OutputValue: pin.OutputValue,
		CreateTime:  pin.Timestamp,
	}
	if col == nil {
		// Out of order: the minter's album under this name is not registered
		// yet, stage for replay (only ever replayed to that same album).
		if err := a.db.PutPending(collectionName, pin.Address, &PendingEntry{
			Kind: "mint", PinId: pin.Id, Mint: mint,
		}); err != nil {
			log.Printf("WARNING: mrc721nft stage mint %s: %v", pin.Id, err)
		}
		return
	}
	a.mintCommit(collectionName, col, mint)
}

// mintCommit indexes an item: supply gate, number allocation, main table +
// album order + holder index. Idempotent by itemPinId: the same pin
// arriving through dual paths or a backfill replay never double-counts.
// Under issuer-only attribution the minter is always the album issuer (the
// album is resolved by the minter's own address), so CreatorMinted is true
// by construction.
func (a *Aggregator) mintCommit(collectionName string, col *Collection, mint *PendingMint) bool {
	if existing, _ := a.db.GetItem(mint.ItemPinId); existing != nil {
		return true
	}
	if mint.OwnerAddress != col.CreateAddress {
		// Invariant guard: collection resolution is by (name, minter
		// address), so this cannot trigger — kept as defence in depth.
		log.Printf("WARNING: mrc721nft mint %s rejected: %s is not issuer of %s (%s)", mint.ItemPinId, mint.OwnerAddress, collectionName, col.CreateAddress)
		return false
	}
	if col.TotalSupply != -1 && col.TotalNum >= col.TotalSupply {
		log.Printf("WARNING: mrc721nft mint %s rejected: exceeds totalSupply %d", collectionName, col.TotalSupply)
		return false
	}
	col.TotalNum++
	item := &Item{
		ItemPinId:        mint.ItemPinId,
		CollectionName:   collectionName,
		CollectionPinId:  col.PinId,
		Number:           col.TotalNum,
		OwnerAddress:     mint.OwnerAddress,
		OwnerMetaId:      mint.OwnerMetaId,
		CreatorMinted:    mint.OwnerAddress == col.CreateAddress,
		ContentType:      mint.ContentType,
		ContentSummary:   mint.ContentSummary,
		GenesisChainName: mint.GenesisChainName,
		GenesisHeight:    mint.GenesisHeight,
		TxIndex:          mint.TxIndex,
		GenesisTx:        mint.GenesisTx,
		GenesisFee:       mint.GenesisFee,
		Output:           mint.Output,
		OutputValue:      mint.OutputValue,
		CreateTime:       mint.CreateTime,
	}
	if err := a.db.SaveCollection(col); err != nil {
		log.Printf("WARNING: mrc721nft update collection %s totalnum: %v", collectionName, err)
		return false
	}
	if err := a.db.SaveItem(item); err != nil {
		log.Printf("WARNING: mrc721nft save item: %v", err)
		return false
	}
	if err := a.db.AppendCollectionItem(col.PinId, item.Number, item.ItemPinId); err != nil {
		log.Printf("WARNING: mrc721nft append coll item: %v", err)
	}
	if err := a.db.PutOwnerItem(mint.OwnerAddress, col.PinId, item.ItemPinId); err != nil {
		log.Printf("WARNING: mrc721nft put owner item: %v", err)
	}
	a.markTrackedDirty()
	// The new item may be exactly what a staged desc is waiting for.
	a.drainPendingDescs(collectionName, col.CreateAddress)
	return true
}

// ---------- transfer ----------

// applyTransferHook consumes transfer-detection results (engine wiring),
// updating item ownership, re-keying the holder index and appending provenance
// history. transferMap is keyed by spent outpoint "txid:index";
// pinIdByOutput maps outpoint → item pin id. Idempotent by
// (itemPinId, height, txid): the same transfer event replayed through chain
// re-index, dual-path delivery or backfill re-runs never double-counts.
func (a *Aggregator) applyTransferHook(chainName string, height int64, transferMap map[string]*TransferInfo, pinIdByOutput map[string]string, transferTime int64) {
	for output, info := range transferMap {
		pinId := pinIdByOutput[output]
		if pinId == "" {
			continue
		}
		if existing, _ := a.db.GetItemTransfer(pinId, height, info.Location); existing != nil {
			continue
		}
		item, err := a.db.GetItem(pinId)
		if err != nil {
			log.Printf("[mrc721nft] get item %s: %v", pinId, err)
			continue
		}
		if item == nil {
			continue // not an MRC-721 item
		}
		if info.IsMeltdown || info.Address == "" {
			// Meltdown: mark burned, drop from holder views, keep the
			// history row for provenance.
			item.Burned = true
			if item.OwnerAddress != "" {
				a.db.DeleteOwnerItem(item.OwnerAddress, item.CollectionPinId, item.ItemPinId)
			}
		} else if item.OwnerAddress != info.Address {
			if item.OwnerAddress != "" {
				a.db.DeleteOwnerItem(item.OwnerAddress, item.CollectionPinId, item.ItemPinId)
			}
			a.db.PutOwnerItem(info.Address, item.CollectionPinId, item.ItemPinId)
			item.OwnerAddress = info.Address
			item.OwnerMetaId = globalMetaIdByAddress(info.Address)
		}
		if !info.IsMeltdown {
			item.Output = info.Output
			item.OutputValue = info.OutputValue
		}
		item.TransferCount++
		if err := a.db.SaveItem(item); err != nil {
			log.Printf("WARNING: mrc721nft save item %s on transfer: %v", pinId, err)
			continue
		}
		a.markTrackedDirty()
		if err := a.db.SaveItemTransfer(&ItemTransfer{
			ItemPinId:   pinId,
			FromAddress: info.FromAddress,
			ToAddress:   info.Address,
			ChainName:   chainName,
			Height:      height,
			TxId:        info.Location,
			Time:        transferTime,
		}); err != nil {
			log.Printf("WARNING: mrc721nft save item transfer %s: %v", pinId, err)
		} else if pt, _ := a.db.GetPendingTransfer(pinId); pt != nil && pt.SpentOutpoint == output {
			// Pending-channel settlement: the pending spend of this outpoint
			// has been decided — same tx confirmed (now in history) or the
			// conflicting one was evicted. The mempool reconcile is the
			// fallback sweeper.
			if err := a.db.DeletePendingTransfer(pinId); err != nil {
				log.Printf("WARNING: mrc721nft delete pending transfer %s: %v", pinId, err)
			}
		}
	}
}

// ---------- pending replay ----------

// drainPending replays an issuer's staged pins after their album registers:
// mints commit first, descs backfill next, colmods apply last in time order.
// The staged mints sort into true chain order (height, block tx index, pin
// output index — R-F-10) before committing, so both the numbering and the
// supply cut follow creation order instead of Pebble's lexicographic pinId
// key order; the zero-conf overlay numbering (R-F-2.3) reuses the same rule.
func (a *Aggregator) drainPending(collectionName, issuerAddress string) {
	col, err := a.db.GetCollection(collectionName, issuerAddress)
	if err != nil || col == nil {
		return
	}
	entries, err := a.db.PopPending(collectionName, issuerAddress)
	if err != nil {
		log.Printf("WARNING: mrc721nft pop pending %s/%s: %v", collectionName, issuerAddress, err)
		return
	}
	if len(entries) == 0 {
		return
	}
	var colMods []*PendingEntry
	var mints []*PendingEntry
	for _, entry := range entries {
		switch {
		case entry.Kind == "mint" && entry.Mint != nil:
			mints = append(mints, entry)
		case entry.Kind == "desc":
			a.drainOneDesc(collectionName, issuerAddress, entry)
		case entry.Kind == "colmod" && entry.ColMod != nil:
			colMods = append(colMods, entry)
		}
	}
	sort.Slice(mints, func(i, j int) bool {
		return stagedMintChainBefore(mints[i].Mint, mints[j].Mint)
	})
	for _, entry := range mints {
		a.mintCommit(collectionName, col, entry.Mint)
	}
	// colmods replay in (timestamp, pinId) order; the last one wins.
	sort.Slice(colMods, func(i, j int) bool {
		x, y := colMods[i].ColMod, colMods[j].ColMod
		if x.Timestamp != y.Timestamp {
			return x.Timestamp < y.Timestamp
		}
		return x.ModifyPinId < y.ModifyPinId
	})
	for _, entry := range colMods {
		m := entry.ColMod
		col, err := a.db.GetCollection(collectionName, issuerAddress)
		if err != nil || col == nil {
			return
		}
		a.collectionModifyCommit(collectionName, col, m.ModifyPinId, m.ModifyAddress, []byte(m.Content))
	}
}

// stagedMintChainBefore orders two staged mints the way the chain committed
// them: block height first, then the transaction index within the block,
// then the pin output index (R-F-10). Entries staged before the chain
// position was carried carry 0 for both new fields; the output index then
// falls back to the pinId suffix, and a still-undecidable pair falls back to
// the pin id so the order stays deterministic.
func stagedMintChainBefore(x, y *PendingMint) bool {
	if x.GenesisHeight != y.GenesisHeight {
		return x.GenesisHeight < y.GenesisHeight
	}
	if x.TxIndex != y.TxIndex {
		return x.TxIndex < y.TxIndex
	}
	xOut, yOut := x.OutputIndex, y.OutputIndex
	if xOut == 0 {
		xOut = pinOutputIndex(x.ItemPinId)
	}
	if yOut == 0 {
		yOut = pinOutputIndex(y.ItemPinId)
	}
	if xOut != yOut {
		return xOut < yOut
	}
	return x.ItemPinId < y.ItemPinId
}

// pinOutputIndex extracts the output index from a "<ledgerTxid>i<vout>" pin
// id (the trailing digit run); unknown shapes yield 0.
func pinOutputIndex(pinId string) int64 {
	idx := strings.LastIndex(pinId, "i")
	if idx < 0 || idx == len(pinId)-1 {
		return 0
	}
	v, err := strconv.ParseInt(pinId[idx+1:], 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// drainPendingDescs replays an issuer's staged descs once new items of their
// album minted; non-desc staging goes back as-is.
func (a *Aggregator) drainPendingDescs(collectionName, issuerAddress string) {
	entries, err := a.db.PopPending(collectionName, issuerAddress)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.Kind == "desc" {
			a.drainOneDesc(collectionName, issuerAddress, entry)
		} else {
			a.db.PutPending(collectionName, issuerAddress, entry)
		}
	}
}

// drainOneDesc applies one staged desc under the issuer gate; refs whose
// items are still unindexed are re-staged by applyDescRefs.
func (a *Aggregator) drainOneDesc(collectionName, issuerAddress string, entry *PendingEntry) {
	if entry.Desc == nil {
		return
	}
	a.applyDescRefs(collectionName, entry.Desc.DescPinId, issuerAddress, entry.Desc.Items)
}

// ---------- content parsing (case-insensitive JSON keys, values verbatim) ----------

type nftContent map[string]json.RawMessage

func parseContent(body []byte) (nftContent, error) {
	var content nftContent
	if err := json.Unmarshal(body, &content); err != nil {
		return nil, err
	}
	return content, nil
}

func lookupCI(content nftContent, key string) (json.RawMessage, bool) {
	if v, ok := content[key]; ok {
		return v, true
	}
	for k, v := range content {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return nil, false
}

func stringField(content nftContent, key string) string {
	raw, ok := lookupCI(content, key)
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// metadata accepts arbitrary JSON (object/array), stored verbatim.
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return trimmed
	}
	return ""
}

func parseInt64(raw json.RawMessage) (int64, error) {
	var i int64
	if err := json.Unmarshal(raw, &i); err == nil {
		return i, nil
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return int64(f), nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strconv.ParseInt(s, 10, 64)
	}
	return 0, strconv.ErrSyntax
}
