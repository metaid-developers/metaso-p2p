package preference

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/pkg/idaddress"
)

// Storage keys, all under the open-evidence namespace prefix: the future
// validated (sealed-round) projection gets its own separate prefix and is
// never blended into these records.
const (
	keyOpenFeedback = "open:feedback:"
)

func openFeedbackKey(chainName, feedbackPinId string) []byte {
	return []byte(keyOpenFeedback + chainName + ":" + feedbackPinId)
}

func (a *Aggregator) loadFeedbackRecord(chainName, feedbackPinId string) (*FeedbackRecord, error) {
	raw, err := a.store.Get(Namespace, openFeedbackKey(chainName, feedbackPinId))
	if err != nil || raw == nil {
		return nil, nil
	}
	var rec FeedbackRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("loadFeedbackRecord: corrupt record %s/%s: %w", chainName, feedbackPinId, err)
	}
	return &rec, nil
}

func (a *Aggregator) saveFeedbackRecord(rec *FeedbackRecord) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return a.store.Set(Namespace, openFeedbackKey(rec.ChainName, rec.FeedbackPinId), raw)
}

// processPin is the entry point shared by the block and mempool handlers.
// Only create operations on /protocols/preference/feedback are consumed;
// modify and revoke are ignored on chain per the shared contract §4.1.
func (a *Aggregator) processPin(pin *aggregator.PinInscription, isMempool bool) error {
	if pin == nil || pin.Id == "" || pin.ChainName == "" {
		return nil
	}
	if protocolPathFromPinPath(pin.Path) != PathPreferenceFeedback {
		return nil
	}
	if normaliseOperation(pin.Operation) != "create" {
		return nil
	}

	chainName := strings.ToLower(strings.TrimSpace(pin.ChainName))
	existing, err := a.loadFeedbackRecord(chainName, pin.Id)
	if err != nil {
		return err
	}
	// Idempotent replay: an already-confirmed pin never changes state, and a
	// mempool echo after confirmation cannot demote the confirmed record.
	if existing != nil {
		if !existing.IsMempool || isMempool {
			return nil
		}
	}

	rec := a.buildFeedbackRecord(pin, chainName, isMempool)
	if err := a.saveFeedbackRecord(rec); err != nil {
		return err
	}
	// Only confirmed feedback changes open scoring state; the pending
	// record is stored for the includeMempool diagnostics view.
	if !rec.IsMempool {
		a.rebuildOpenScores()
	}
	return nil
}

// buildFeedbackRecord validates the batch and resolves its ArtifactRefs.
// Schema violations mark the whole pin malformed (atomic); per-item
// resolution problems (unresolvable app, version outside the chain,
// manifest mismatch, author conflict) exclude that item from scoring while
// unrelated valid pairs in the same batch stay eligible (§8 vector 9).
func (a *Aggregator) buildFeedbackRecord(pin *aggregator.PinInscription, chainName string, isMempool bool) *FeedbackRecord {
	rec := &FeedbackRecord{
		FeedbackPinId:      pin.Id,
		ChainName:          chainName,
		IsMempool:          isMempool,
		ConfirmedHeight:    pin.GenesisHeight,
		ConfirmedAt:        pin.Timestamp,
		ObservedAt:         pin.Timestamp,
		SignerGlobalMetaId: canonicalSignerGlobalMetaId(pin),
		SignerMetaId:       strings.TrimSpace(pin.MetaId),
		SignerAddress:      strings.TrimSpace(pin.Address),
	}

	body := feedbackPayloadBody(pin)
	categories, client, items, err := parseComparisonBatch(body)
	switch {
	case err != nil:
		rec.Status = StatusMalformed
		rec.InvalidReason = err.Error()
		return rec
	case !hasMotionVideoCategory(categories):
		// Schema-valid but outside the v1 Motion Video projection.
		rec.Status = StatusUnsupported
		rec.Categories = categories
		rec.Client = client
		return rec
	}

	rec.Status = StatusValid
	rec.Categories = categories
	rec.Client = client
	rec.IsTest = isTestBatch(categories)
	for i := range items {
		a.resolveComparisonExclusion(&items[i], chainName, rec.SignerGlobalMetaId, rec.SignerMetaId, rec.SignerAddress)
	}
	rec.Items = items
	return rec
}

// resolveComparisonExclusion marks one comparison excluded when its refs do
// not resolve against the indexed MetaApp manifests or the signer published
// one of the compared app versions.
func (a *Aggregator) resolveComparisonExclusion(item *FeedbackComparison, chainName, signerGlobalMetaId, signerMetaId, signerAddress string) {
	for _, side := range []struct {
		ref *ArtifactRef
	}{
		{&item.A}, {&item.B},
	} {
		if reason := a.resolveArtifactRef(side.ref, chainName, signerGlobalMetaId, signerMetaId, signerAddress); reason != "" {
			item.Excluded = true
			item.ExclusionReason = reason
			return
		}
	}
}

// resolveArtifactRef validates one ArtifactRef side and returns "" when it
// resolves cleanly, otherwise the exclusion reason.
func (a *Aggregator) resolveArtifactRef(ref *ArtifactRef, pinChainName, signerGlobalMetaId, signerMetaId, signerAddress string) string {
	if !strings.EqualFold(ref.ChainName, pinChainName) {
		return ExclusionChainMismatch
	}
	resolver := a.metaAppResolver
	if resolver == nil {
		return ""
	}

	rootPinId := metaAppPinId(ref.App)
	root, ok := resolver.ResolveMetaAppSnapshot(ref.ChainName, rootPinId)
	if !ok {
		return ExclusionAppNotFound
	}
	// The version must belong to the same app's modify chain on the same
	// chain: it either is the root, or resolves through the same chain to
	// the same root.
	versionPinId := metaAppPinId(ref.Version)
	if versionPinId != root.RootPinId {
		versionSnapshot, ok := resolver.ResolveMetaAppSnapshot(ref.ChainName, versionPinId)
		if !ok || versionSnapshot.RootPinId != root.RootPinId {
			return ExclusionVersionOutsideApp
		}
	}

	// Current-version refs must agree with the folded manifest claim.
	// Historical versions' manifests are not retained, so their fields are
	// recorded as stated (contract §3 rule 7: historical evidence stays
	// queryable when a new video starts a fresh aggregate).
	if versionPinId == root.CurrentPinId && len(root.ManifestPayload) > 0 {
		manifestVideo, manifestSha := manifestVideoClaim(root.ManifestPayload)
		if manifestSha != "" {
			if normalizeMetaFileURI(ref.Video) != normalizeMetaFileURI(manifestVideo) ||
				ref.VideoSha256 != manifestSha {
				return ExclusionArtifactMismatch
			}
		}
	}

	if publisherIsSigner(root, signerGlobalMetaId, signerMetaId, signerAddress) {
		return ExclusionAuthorConflict
	}
	return ""
}

func publisherIsSigner(snapshot *publishedcontent.MetaAppSnapshot, signerGlobalMetaId, signerMetaId, signerAddress string) bool {
	signerIds := []string{signerGlobalMetaId, signerMetaId, signerAddress}
	publisherIds := []string{snapshot.PublisherGlobalMetaId, snapshot.PublisherMetaId, snapshot.PublisherAddress}
	for _, signer := range signerIds {
		if signer == "" {
			continue
		}
		for _, publisher := range publisherIds {
			if publisher != "" && strings.EqualFold(signer, publisher) {
				return true
			}
		}
	}
	return false
}

// manifestVideoClaim reads the current folded manifest's video declarations.
func manifestVideoClaim(payload map[string]any) (videoFile, videoSha256 string) {
	meta, _ := payload["metadata"].(map[string]any)
	if meta == nil {
		return "", ""
	}
	if v, ok := meta["videoFile"].(string); ok {
		videoFile = strings.TrimSpace(v)
	}
	if v, ok := meta["videoSha256"].(string); ok {
		videoSha256 = strings.TrimSpace(v)
	}
	return videoFile, videoSha256
}

func metaAppPinId(uri string) string {
	return strings.TrimPrefix(strings.TrimSpace(uri), "metaapp://")
}

// normalizeMetaFileURI compares metafile references ignoring an optional
// filename extension, which carries no validity meaning (contract §2.2).
func normalizeMetaFileURI(ref string) string {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "metafile://") {
		return ref
	}
	body := strings.TrimPrefix(ref, "metafile://")
	pinPart := body
	if dot := strings.Index(body, "."); dot >= 0 {
		pinPart = body[:dot]
	}
	return "metafile://" + pinPart
}

func feedbackPayloadBody(pin *aggregator.PinInscription) []byte {
	raw := bytes.TrimSpace(pin.ContentBody)
	if len(raw) == 0 {
		raw = []byte(strings.TrimSpace(pin.ContentSummary))
	}
	return raw
}

func canonicalSignerGlobalMetaId(pin *aggregator.PinInscription) string {
	globalMetaId := strings.TrimSpace(pin.GlobalMetaId)
	address := firstNonEmptyTrimmed(pin.Address, pin.CreateAddress)
	if address == "" {
		return globalMetaId
	}
	if globalMetaId != "" && !strings.EqualFold(globalMetaId, address) {
		return globalMetaId
	}
	if encoded := idaddress.EncodeGlobalMetaId(address, pin.ChainName); encoded != "" {
		return encoded
	}
	return globalMetaId
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normaliseOperation(operation string) string {
	return strings.ToLower(strings.TrimSpace(operation))
}

// protocolPathFromPinPath mirrors the publishedcontent path normalisation:
// strip trailing slashes, drop an @target suffix, and accept both plain and
// scheme-prefixed path spellings.
func protocolPathFromPinPath(path string) string {
	base := strings.TrimRight(strings.TrimSpace(path), "/")
	if at := strings.Index(base, "@"); at > 0 {
		base = base[:at]
	}
	if idx := strings.LastIndex(base, ":/"); idx >= 0 {
		if candidate := base[idx+1:]; strings.HasPrefix(candidate, "/protocols/") {
			base = candidate
		}
	}
	return strings.ToLower(base)
}

// ErrStoreNotInitialised guards read paths used before Init.
var ErrStoreNotInitialised = errors.New("preference aggregator is not initialised")
