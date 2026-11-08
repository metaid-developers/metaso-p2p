package mrc721nft

import (
	"sort"
	"strings"
)

// Zero-conf read projections (R-F-2 issuance, R-F-3 trading): every read
// surface composes the confirmed Pebble aggregates with the in-memory
// overlay. Confirmed state is never mutated — projections clone the stored
// records and fold overlay effects on top, in the overlay's provisional
// order, so a dropped transaction's effects simply stop applying.
//
// Wire rule (R-F-5): the new fields (confirmed, firstSeenAt, numberFinal,
// ownershipConfirmed, confirmedOwnerAddress, descConfirmed, overSupply,
// mintedCount, confirmedMintedCount, contentConfirmed) are written only
// when the overlay is active for the request — a row fully backed by the
// confirmed aggregate keeps today's exact shape. confirmedOnly=1 skips the
// overlay entirely, byte-identical to the pre-zero-conf responses.

// viewOptions carries the per-request read options (R-F-6).
type viewOptions struct {
	confirmedOnly     bool
	includeOverSupply bool
}

// albumView is the merged view of one album: the confirmed record (when
// registered) or an overlay collection_desc create, with overlay modifies
// folded in.
type albumView struct {
	collection       *Collection
	confirmed        bool  // false when an overlay create or modify produced the view
	contentConfirmed bool  // false when any overlay modify applies
	firstSeenAt      int64 // overlay-affected views only
	name             string
	issuer           string
	confirmedMinted  int64 // confirmed TotalNum (0 while unregistered)
	stagedMintCount  int64 // confirmed staged mints (pre-registration)
	overlayMintCount int64 // non-oversupply overlay mints
}

// itemView is the merged view of one item.
type itemView struct {
	item                  *Item
	confirmed             bool
	numberFinal           bool
	descConfirmed         bool
	overSupply            bool
	ownershipConfirmed    bool
	confirmedOwnerAddress string
	firstSeenAt           int64
	albumIssuer           string // the item's album issuer (the desc gate reference)
	overlayTransfers      []*overlaySpendEntry
}

// ---------- album resolution ----------

// overlayAlbumsLocked derives the overlay album registrations from the
// snapshot: one per (collectionName, issuer) col_create pin — the pair's
// first create in provisional order wins (first-registration rule).
func overlayAlbumsLocked(snap *overlaySnapshot) map[string]*overlayPinEntry {
	albums := make(map[string]*overlayPinEntry)
	var creates []*overlayPinEntry
	for _, entry := range snap.pins {
		if entry.Kind == overlayEffectColCreate && entry.ColCreate != nil {
			creates = append(creates, entry)
		}
	}
	sort.Slice(creates, func(i, j int) bool { return overlayPinBefore(creates[i], creates[j]) })
	for _, entry := range creates {
		pair := entry.ColCreate.CollectionName + "&" + entry.ColCreate.CreateAddress
		if _, ok := albums[pair]; !ok {
			albums[pair] = entry
		}
	}
	return albums
}

// albumViewByPair resolves the merged album view for a (name, issuer) pair:
// the confirmed record wins over an overlay create; overlay modifies fold
// on top of whichever base won.
func (a *Aggregator) albumViewByPair(name, issuer string, opts viewOptions) *albumView {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.albumViewByPairLocked(a.overlaySnapshotLocked(), name, issuer, opts)
}

// albumViewByPairLocked is the locked core of albumViewByPair (caller holds
// a.mu and owns the snapshot).
func (a *Aggregator) albumViewByPairLocked(snap *overlaySnapshot, name, issuer string, opts viewOptions) *albumView {
	col, err := a.db.GetCollection(name, issuer)
	if err != nil {
		return nil
	}
	view := &albumView{name: name, issuer: issuer, contentConfirmed: true, confirmed: true}
	if col != nil {
		view.collection = cloneCollection(col)
		view.confirmedMinted = col.TotalNum
	} else if opts.confirmedOnly {
		return nil
	}
	if view.collection == nil {
		entry := overlayAlbumsLocked(snap)[name+"&"+issuer]
		if entry == nil || entry.ColCreate == nil {
			return nil
		}
		view.collection = cloneCollection(entry.ColCreate)
		view.confirmed = false
		view.contentConfirmed = true
		view.firstSeenAt = entry.FirstSeenAt
		view.stagedMintCount = a.stagedMintCountForLocked(name, issuer)
	}
	a.foldAlbumModifies(snap, view, opts)
	a.countOverlayMints(snap, view)
	return view
}

// albumViewByPinId resolves by collectionid: the confirmed album under that
// pinid, else the overlay create with that pinid.
func (a *Aggregator) albumViewByPinId(pinId string, opts viewOptions) *albumView {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.albumViewByPinIdLocked(a.overlaySnapshotLocked(), pinId, opts)
}

func (a *Aggregator) albumViewByPinIdLocked(snap *overlaySnapshot, pinId string, opts viewOptions) *albumView {
	col, err := a.db.GetCollectionByPinId(pinId)
	if err != nil {
		return nil
	}
	if col != nil {
		return a.albumViewByPairLocked(snap, col.CollectionName, col.CreateAddress, opts)
	}
	if opts.confirmedOnly {
		return nil
	}
	var entry *overlayPinEntry
	for _, candidate := range overlayAlbumsLocked(snap) {
		if candidate.PinId == pinId {
			entry = candidate
			break
		}
	}
	if entry == nil || entry.ColCreate == nil {
		return nil
	}
	return a.albumViewFromOverlayCreateLocked(snap, entry, opts)
}

// albumViewByName resolves an ambiguous name to the earliest album under
// it: confirmed albums first (exactly today's rule), overlay albums only
// when no confirmed album exists under the name.
func (a *Aggregator) albumViewByName(name string, opts viewOptions) *albumView {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.albumViewByNameLocked(a.overlaySnapshotLocked(), name, opts)
}

func (a *Aggregator) albumViewByNameLocked(snap *overlaySnapshot, name string, opts viewOptions) *albumView {
	if col, err := a.db.GetEarliestCollectionByName(name); err == nil && col != nil {
		return a.albumViewByPairLocked(snap, col.CollectionName, col.CreateAddress, opts)
	}
	if opts.confirmedOnly {
		return nil
	}
	var candidates []*overlayPinEntry
	for _, entry := range overlayAlbumsLocked(snap) {
		if entry.ColCreate != nil && entry.ColCreate.CollectionName == name {
			candidates = append(candidates, entry)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		x, y := candidates[i].ColCreate, candidates[j].ColCreate
		if x.CreateTime != y.CreateTime {
			return x.CreateTime < y.CreateTime
		}
		return x.PinId < y.PinId
	})
	return a.albumViewFromOverlayCreateLocked(snap, candidates[0], opts)
}

// albumViewFromOverlayCreateLocked assembles the view of an overlay-created
// album (confirmed albums for the pair always win over it).
func (a *Aggregator) albumViewFromOverlayCreateLocked(snap *overlaySnapshot, entry *overlayPinEntry, opts viewOptions) *albumView {
	view := &albumView{
		name:             entry.ColCreate.CollectionName,
		issuer:           entry.ColCreate.CreateAddress,
		collection:       cloneCollection(entry.ColCreate),
		confirmed:        false,
		contentConfirmed: true,
		firstSeenAt:      entry.FirstSeenAt,
	}
	view.stagedMintCount = a.stagedMintCountForLocked(view.name, view.issuer)
	a.foldAlbumModifies(snap, view, opts)
	a.countOverlayMints(snap, view)
	return view
}

// foldAlbumModifies applies the album's overlay collection_desc modifies in
// provisional order (issuer-gated, same field rules as the confirmed path).
func (a *Aggregator) foldAlbumModifies(snap *overlaySnapshot, view *albumView, opts viewOptions) {
	var modifies []*overlayPinEntry
	for _, entry := range snap.pins {
		if entry.Kind == overlayEffectColModify && entry.ColModify != nil &&
			entry.Collection == view.name && entry.ColModify.ModifyAddress == view.issuer {
			modifies = append(modifies, entry)
		}
	}
	if len(modifies) == 0 || opts.confirmedOnly {
		return
	}
	sort.Slice(modifies, func(i, j int) bool { return overlayPinBefore(modifies[i], modifies[j]) })
	col := view.collection
	for _, entry := range modifies {
		content, err := parseContent([]byte(entry.ColModify.Content))
		if err != nil {
			continue // invalid modify content: skipped, exactly like the confirmed path
		}
		if raw, ok := lookupCI(content, "totalsupply"); ok {
			if v, err := parseInt64(raw); err == nil && v >= -1 && v <= 1000000000000 {
				col.TotalSupply = v
			}
		}
		if raw, ok := lookupCI(content, "royaltyrate"); ok {
			if v, err := parseInt64(raw); err == nil && v >= 0 && v <= 20 {
				col.RoyaltyRate = int(v)
			}
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
		col.ModifyHistory = append(col.ModifyHistory, entry.ColModify.ModifyPinId)
		if view.confirmed {
			view.confirmed = false // an overlay modify marks the row overlay-affected
			view.firstSeenAt = entry.FirstSeenAt
		}
		view.contentConfirmed = false
	}
}

// countOverlayMints fills the album's non-oversupply overlay mint count
// (the served mintedCount's overlay part, R-F-2.3): overlay mints past the
// remaining supply stay out of the counts — the same outcome the chain's
// final order produces.
func (a *Aggregator) countOverlayMints(snap *overlaySnapshot, view *albumView) {
	mints := a.overlayMintsForAlbumLocked(snap, view.name, view.issuer)
	if len(mints) == 0 {
		return
	}
	supply := int64(-1)
	if view.collection != nil {
		supply = view.collection.TotalSupply
	}
	remaining := supply
	if remaining != -1 {
		remaining -= view.confirmedMinted + view.stagedMintCount
	}
	count := int64(0)
	for range mints {
		if remaining != -1 && remaining <= 0 {
			break
		}
		count++
		if remaining != -1 {
			remaining--
		}
	}
	view.overlayMintCount = count
}

// overlayMintsForAlbumLocked returns the album's overlay mint pins in
// provisional order (topological: parent mint tx before child, then
// firstSeenAt, then txid).
func (a *Aggregator) overlayMintsForAlbumLocked(snap *overlaySnapshot, name, issuer string) []*overlayPinEntry {
	var mints []*overlayPinEntry
	for _, entry := range snap.pins {
		if entry.Kind == overlayEffectMint && entry.Mint != nil &&
			entry.Collection == name && entry.Mint.OwnerAddress == issuer {
			mints = append(mints, entry)
		}
	}
	return orderOverlayMints(mints)
}

// stagedMintCountForLocked counts the confirmed staged mints of a pair
// (album not yet registered).
func (a *Aggregator) stagedMintCountForLocked(name, issuer string) int64 {
	staged, err := a.db.PeekPendingAll()
	if err != nil {
		return 0
	}
	count := int64(0)
	for _, sp := range staged {
		if sp.CollectionName == name && sp.IssuerAddress == issuer && sp.Entry.Kind == "mint" && sp.Entry.Mint != nil {
			count++
		}
	}
	return count
}

// stagedMintsForLocked returns the pair's confirmed staged mints in the
// order drainPending will commit them (R-F-2.3 numbering source).
func (a *Aggregator) stagedMintsForLocked(name, issuer string) []*PendingMint {
	staged, err := a.db.PeekPendingAll()
	if err != nil {
		return nil
	}
	var mints []*PendingMint
	for _, sp := range staged {
		if sp.CollectionName == name && sp.IssuerAddress == issuer && sp.Entry.Kind == "mint" && sp.Entry.Mint != nil {
			mints = append(mints, sp.Entry.Mint)
		}
	}
	sort.Slice(mints, func(i, j int) bool { return stagedMintChainBefore(mints[i], mints[j]) })
	return mints
}

// cloneCollection deep-copies a stored record for projection folding.
func cloneCollection(col *Collection) *Collection {
	if col == nil {
		return nil
	}
	out := *col
	if len(col.ModifyHistory) > 0 {
		out.ModifyHistory = append([]string(nil), col.ModifyHistory...)
	} else {
		out.ModifyHistory = nil
	}
	return &out
}

// overlayPinBefore is the provisional order of overlay pins (R-F-3.4
// design principle: firstSeenAt, then txid, then pin id for pathological
// same-tx ties).
func overlayPinBefore(x, y *overlayPinEntry) bool {
	if x.FirstSeenAt != y.FirstSeenAt {
		return x.FirstSeenAt < y.FirstSeenAt
	}
	if x.TxId != y.TxId {
		return x.TxId < y.TxId
	}
	return x.PinId < y.PinId
}

// orderOverlayMints sorts overlay mints topologically — a parent mint tx
// ranks before any mint tx spending its outputs — then by firstSeenAt,
// then txid. Ancestry runs over the pins' wire-hash input pointers against
// the overlay set's WireHash values (the ledger-hash convention differs
// from the wire hash on MVC version>=10 txs); funding chains through
// non-pin mempool txs are invisible here, which keeps such mints
// numberFinal:false (conservative and safe).
func orderOverlayMints(mints []*overlayPinEntry) []*overlayPinEntry {
	if len(mints) <= 1 {
		return mints
	}
	parent := ancestorMap(mints)
	depth := make(map[string]int, len(mints))
	visiting := make(map[string]bool)
	var depthOf func(pinId string) int
	depthOf = func(pinId string) int {
		if d, ok := depth[pinId]; ok {
			return d
		}
		if visiting[pinId] {
			return 0 // cycle guard (cannot happen in a tx DAG)
		}
		visiting[pinId] = true
		d := 0
		if p, ok := parent[pinId]; ok {
			d = depthOf(p) + 1
		}
		delete(visiting, pinId)
		depth[pinId] = d
		return d
	}
	ordered := make([]*overlayPinEntry, len(mints))
	copy(ordered, mints)
	sort.SliceStable(ordered, func(i, j int) bool {
		di, dj := depthOf(ordered[i].PinId), depthOf(ordered[j].PinId)
		if di != dj {
			return di < dj
		}
		return overlayPinBefore(ordered[i], ordered[j])
	})
	return ordered
}

// ancestorMap builds the pinId → direct-parent-pinId map of an overlay mint
// set: a mint is a child when one of its tx inputs spends an output of
// another mint tx of the set.
func ancestorMap(mints []*overlayPinEntry) map[string]string {
	byWireHash := make(map[string]string, len(mints))
	for _, m := range mints {
		if m.WireHash != "" {
			byWireHash[m.WireHash] = m.PinId
		}
	}
	parent := make(map[string]string, len(mints))
	for _, m := range mints {
		for _, input := range m.Mint.Inputs {
			wire := input
			if idx := strings.Index(wire, ":"); idx >= 0 {
				wire = wire[:idx]
			}
			if p, ok := byWireHash[wire]; ok && p != m.PinId {
				parent[m.PinId] = p
				break
			}
		}
	}
	return parent
}

// isAncestor reports whether ancestor is a transitive parent of pinId
// within the overlay mint set.
func isAncestor(parent map[string]string, ancestor, pinId string) bool {
	seen := make(map[string]struct{})
	for {
		p, ok := parent[pinId]
		if !ok {
			return false
		}
		if p == ancestor {
			return true
		}
		if _, cycle := seen[p]; cycle {
			return false
		}
		seen[p] = struct{}{}
		pinId = p
	}
}

// ---------- item projection ----------

// itemViewsForAlbum builds the merged item list of one album in serve
// order: confirmed items (mint order), then staged confirmed mints (chain
// order — the order drainPending will apply), then overlay mints
// (topological provisional order), with provisional numbers and the
// supply gate applied.
func (a *Aggregator) itemViewsForAlbum(view *albumView, opts viewOptions) []*itemView {
	a.mu.Lock()
	defer a.mu.Unlock()
	snap := a.overlaySnapshotLocked()

	views := make([]*itemView, 0, view.confirmedMinted+view.stagedMintCount+view.overlayMintCount)
	if view.collection != nil {
		_ = a.db.IterateCollectionItems(view.collection.PinId, func(seq int64, itemPinId string) bool {
			if item, err := a.db.GetItem(itemPinId); err == nil && item != nil {
				iv := a.itemViewForConfirmedLocked(snap, item, view.issuer, opts)
				views = append(views, iv)
			}
			return true
		})
	}
	supply := int64(-1)
	if view.collection != nil {
		supply = view.collection.TotalSupply
	}

	// Staged confirmed mints (album not registered yet): numbered first, in
	// exactly the order drainPending will commit them (R-F-2.3).
	pos := view.confirmedMinted
	for _, mint := range a.stagedMintsForLocked(view.name, view.issuer) {
		pos++
		iv := a.itemViewForStagedMintLocked(snap, view, mint, pos, supply, opts)
		if iv.overSupply && !opts.includeOverSupply {
			continue
		}
		views = append(views, iv)
	}

	// Overlay mints: provisional numbers continue after the confirmed and
	// staged mints; past-supply mints get overSupply and stay hidden unless
	// includeOverSupply=1.
	for _, entry := range a.overlayMintsForAlbumLocked(snap, view.name, view.issuer) {
		pos++
		iv := a.itemViewForOverlayMintLocked(snap, view, entry, pos, supply)
		if iv.overSupply && !opts.includeOverSupply {
			continue
		}
		views = append(views, iv)
	}
	return views
}

// itemViewForConfirmedLocked folds the overlay onto a confirmed item.
func (a *Aggregator) itemViewForConfirmedLocked(snap *overlaySnapshot, item *Item, issuer string, opts viewOptions) *itemView {
	merged := *item
	view := &itemView{
		item:               &merged,
		confirmed:          true,
		numberFinal:        true,
		ownershipConfirmed: true,
		albumIssuer:        issuer,
	}
	if issuer == "" {
		view.albumIssuer = a.albumIssuerOfItemLocked(item)
	}
	if opts.confirmedOnly {
		return view
	}
	view.confirmedOwnerAddress = item.OwnerAddress
	view.applyOwnershipFold(snap)
	view.applyDescFold(snap, item.CollectionName)
	return view
}

// albumIssuerOfItemLocked resolves a confirmed item's album issuer.
func (a *Aggregator) albumIssuerOfItemLocked(item *Item) string {
	if col, err := a.db.GetCollectionByPinId(item.CollectionPinId); err == nil && col != nil {
		return col.CreateAddress
	}
	return ""
}

// itemViewForStagedMintLocked projects a confirmed staged mint (album
// unregistered): the pin is confirmed, the number is chain-ordered final,
// the row itself is not in the confirmed aggregate yet.
func (a *Aggregator) itemViewForStagedMintLocked(snap *overlaySnapshot, view *albumView, mint *PendingMint, number, supply int64, opts viewOptions) *itemView {
	item := &Item{
		ItemPinId:        mint.ItemPinId,
		CollectionName:   view.name,
		CollectionPinId:  view.collection.PinId,
		Number:           number,
		OwnerAddress:     mint.OwnerAddress,
		OwnerMetaId:      mint.OwnerMetaId,
		CreatorMinted:    mint.OwnerAddress == view.issuer,
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
	iv := &itemView{
		item:                  item,
		confirmed:             false, // not in the confirmed aggregate yet
		numberFinal:           true,  // chain-ordered confirmed mints cannot reorder
		ownershipConfirmed:    false,
		confirmedOwnerAddress: mint.OwnerAddress,
		albumIssuer:           view.issuer,
	}
	if supply != -1 && number > supply {
		iv.overSupply = true
	}
	if opts.confirmedOnly {
		return iv
	}
	a.applyStagedDescFold(iv, view.name, view.issuer)
	iv.applyOwnershipFold(snap)
	return iv
}

// itemViewForOverlayMintLocked projects one overlay mint.
func (a *Aggregator) itemViewForOverlayMintLocked(snap *overlaySnapshot, view *albumView, entry *overlayPinEntry, number, supply int64) *itemView {
	mint := entry.Mint
	item := &Item{
		ItemPinId:        mint.ItemPinId,
		CollectionName:   view.name,
		CollectionPinId:  view.collection.PinId,
		Number:           number,
		OwnerAddress:     mint.OwnerAddress,
		OwnerMetaId:      mint.OwnerMetaId,
		CreatorMinted:    mint.OwnerAddress == view.issuer,
		ContentType:      mint.ContentType,
		ContentSummary:   mint.ContentSummary,
		GenesisChainName: mint.GenesisChainName,
		GenesisHeight:    -1, // MAN convention for overlay rows (R-F-5)
		TxIndex:          mint.TxIndex,
		GenesisTx:        mint.GenesisTx,
		GenesisFee:       mint.GenesisFee,
		Output:           mint.Output,
		OutputValue:      mint.OutputValue,
		CreateTime:       mint.CreateTime,
	}
	iv := &itemView{
		item:                  item,
		confirmed:             false,
		ownershipConfirmed:    false,
		confirmedOwnerAddress: mint.OwnerAddress,
		firstSeenAt:           entry.FirstSeenAt,
		albumIssuer:           view.issuer,
	}
	if supply != -1 && number > supply {
		iv.overSupply = true
	}
	// numberFinal: every overlay mint ranked ahead must be a tx ancestor
	// (a parent→child chain no miner can reorder); staged/confirmed mints
	// ranked ahead are chain-final by definition.
	iv.numberFinal = true
	mints := a.overlayMintsForAlbumLocked(snap, view.name, view.issuer)
	if len(mints) > 1 {
		parent := ancestorMap(mints)
		for _, other := range mints {
			if other.PinId == entry.PinId {
				break // everything after is ranked behind
			}
			if !isAncestor(parent, other.PinId, entry.PinId) {
				iv.numberFinal = false
				break
			}
		}
	}
	iv.applyDescFold(snap, view.name)
	iv.applyOwnershipFold(snap)
	return iv
}

// applyOwnershipFold walks the item's overlay spends in provisional order,
// chaining from the item's current output (R-F-3.1). Chained unconfirmed
// spends apply in one fold; a spend whose outpoint no longer matches
// (its tx lost a race against a confirmed spend) is skipped.
func (v *itemView) applyOwnershipFold(snap *overlaySnapshot) {
	var spends []*overlaySpendEntry
	for _, sp := range snap.spends {
		if sp.ItemPinId == v.item.ItemPinId {
			spends = append(spends, sp)
		}
	}
	if len(spends) == 0 {
		return
	}
	sort.Slice(spends, func(i, j int) bool {
		if spends[i].FirstSeenAt != spends[j].FirstSeenAt {
			return spends[i].FirstSeenAt < spends[j].FirstSeenAt
		}
		return spends[i].TxId < spends[j].TxId
	})
	current := v.item.Output
	applied := false
	for _, sp := range spends {
		if sp.SpentOutpoint != current {
			continue // dangling: the outpoint moved via a confirmed spend
		}
		if sp.Burned {
			v.item.Burned = true
			v.item.OwnerAddress = ""
			v.item.OwnerMetaId = ""
		} else {
			v.item.OwnerAddress = sp.ToAddress
			v.item.OwnerMetaId = globalMetaIdByAddress(sp.ToAddress)
			v.item.Burned = false
		}
		if !sp.Burned {
			v.item.Output = sp.Output
			v.item.OutputValue = sp.OutputValue
		}
		current = sp.Output
		applied = true
		v.overlayTransfers = append(v.overlayTransfers, sp)
	}
	if applied {
		v.ownershipConfirmed = false
	}
}

// applyDescFold folds overlay item_desc refs onto the item (R-F-2.4):
// confirmed descs are already in the stored fields, overlay refs fill only
// still-empty fields, gated on the desc author being the item's album
// issuer, provisional order, first-write-wins. A field filled here makes
// descConfirmed:false.
func (v *itemView) applyDescFold(snap *overlaySnapshot, collectionName string) {
	var descs []*overlayPinEntry
	for _, entry := range snap.pins {
		if entry.Kind == overlayEffectItemDesc && entry.Desc != nil && entry.Collection == collectionName {
			descs = append(descs, entry)
		}
	}
	if len(descs) == 0 || v.albumIssuer == "" {
		return
	}
	sort.Slice(descs, func(i, j int) bool { return overlayPinBefore(descs[i], descs[j]) })
	for _, entry := range descs {
		if entry.Author != v.albumIssuer {
			continue // issuer gate: only the album issuer describes its items
		}
		for _, ref := range entry.Desc.Items {
			if ref.PinId == v.item.ItemPinId {
				v.fillDescFields(ref)
			}
		}
	}
}

// applyStagedDescFold applies the pair's staged (confirmed, pre-registration)
// descs onto a staged item — exactly what drainOneDesc will do at
// registration. Staged descs are keyed per (name, issuer) and staged only
// for the author's own album, so the issuer gate holds by construction.
// Callers hold a.mu (the staging scan is a Pebble read).
func (a *Aggregator) applyStagedDescFold(v *itemView, collectionName, issuer string) {
	descs, err := a.peekStagedDescs(collectionName, issuer)
	if err != nil {
		return
	}
	for _, desc := range descs {
		for _, ref := range desc.Items {
			if ref.PinId == v.item.ItemPinId {
				v.fillDescFields(ref)
			}
		}
	}
}

// peekStagedDescs returns the pair's staged item_desc payloads read-only.
// Caller holds a.mu.
func (a *Aggregator) peekStagedDescs(collectionName, issuer string) ([]*PendingDesc, error) {
	staged, err := a.db.PeekPendingAll()
	if err != nil {
		return nil, err
	}
	var descs []*PendingDesc
	for _, sp := range staged {
		if sp.CollectionName == collectionName && sp.IssuerAddress == issuer && sp.Entry.Kind == "desc" && sp.Entry.Desc != nil {
			descs = append(descs, sp.Entry.Desc)
		}
	}
	return descs, nil
}

// fillDescFields applies one ref's still-empty fields (first-write-wins).
func (v *itemView) fillDescFields(ref ItemDescRef) {
	changed := false
	if v.item.Name == "" && ref.Name != "" {
		v.item.Name, changed = ref.Name, true
	}
	if v.item.Desc == "" && ref.Desc != "" {
		v.item.Desc, changed = ref.Desc, true
	}
	if v.item.Cover == "" && ref.Cover != "" {
		v.item.Cover, changed = ref.Cover, true
	}
	refMeta := ref.MetadataText()
	if v.item.Metadata == "" && refMeta != "" {
		v.item.Metadata, changed = refMeta, true
	}
	if changed {
		v.descConfirmed = false
	}
}

// itemViewByPinId resolves one item's merged view: confirmed item, staged
// mint, or overlay mint.
func (a *Aggregator) itemViewByPinId(pinId string, opts viewOptions) *itemView {
	a.mu.Lock()
	defer a.mu.Unlock()
	snap := a.overlaySnapshotLocked()

	if item, err := a.db.GetItem(pinId); err == nil && item != nil {
		return a.itemViewForConfirmedLocked(snap, item, "", opts)
	}
	if opts.confirmedOnly {
		return nil
	}
	// Overlay mint (direct pinId key hit).
	if entry := snap.pins[pinId]; entry != nil && entry.Kind == overlayEffectMint && entry.Mint != nil {
		view := a.albumViewByPairLocked(snap, entry.Collection, entry.Mint.OwnerAddress, opts)
		if view == nil {
			return nil
		}
		supply := int64(-1)
		if view.collection != nil {
			supply = view.collection.TotalSupply
		}
		pos := view.confirmedMinted + view.stagedMintCount
		for _, m := range a.overlayMintsForAlbumLocked(snap, view.name, view.issuer) {
			pos++
			if m.PinId == pinId {
				return a.itemViewForOverlayMintLocked(snap, view, entry, pos, supply)
			}
		}
		return nil
	}
	// Staged confirmed mint (album unregistered): rank within the drain
	// order (confirmed staged first, then overlay mints).
	view := a.albumViewByStagedItemLocked(snap, pinId, opts)
	if view == nil {
		return nil
	}
	supply := int64(-1)
	if view.collection != nil {
		supply = view.collection.TotalSupply
	}
	pos := view.confirmedMinted
	for _, mint := range a.stagedMintsForLocked(view.name, view.issuer) {
		pos++
		if mint.ItemPinId == pinId {
			return a.itemViewForStagedMintLocked(snap, view, mint, pos, supply, opts)
		}
	}
	return nil
}

// albumViewByStagedItemLocked resolves the album view a staged item belongs
// to (found via the staging scan).
func (a *Aggregator) albumViewByStagedItemLocked(snap *overlaySnapshot, itemPinId string, opts viewOptions) *albumView {
	staged, _ := a.db.PeekPendingAll()
	for _, sp := range staged {
		if sp.Entry.Kind == "mint" && sp.Entry.Mint != nil && sp.Entry.Mint.ItemPinId == itemPinId {
			return a.albumViewByPairLocked(snap, sp.CollectionName, sp.IssuerAddress, opts)
		}
	}
	return nil
}
