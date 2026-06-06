package skillservice

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// Pebble key layout for rating data
//
//	rating_pin:<chainName>:<ratingPinId>            → RatingPin JSON
//	                                                 (the raw rating record;
//	                                                 used as the dedup source
//	                                                 of truth and for future
//	                                                 "latest ratings" reads)
//	rating_agg:<chainName>:<sourceServicePinId>     → RatingAggregate JSON
//	                                                 (sum / count, derived
//	                                                 incrementally — avg is
//	                                                 computed on read)
const (
	keyRatingPin = "rating_pin:"
	keyRatingAgg = "rating_agg:"
)

// ratingPinKey returns the dedup key for a single rating PIN. The aggregator
// stores ratings under the rating namespace; using chainName in the key
// keeps cross-chain rating ingestion isolated and lets future per-chain
// scans stay efficient.
func ratingPinKey(chainName, ratingPinId string) []byte {
	return []byte(keyRatingPin + chainName + ":" + ratingPinId)
}

// ratingAggKey returns the per-service aggregate key. Note we deliberately
// key the aggregate by sourceServicePinId (the stable root). Even though
// rating PINs sometimes target currentPinId or older version pin ids, the
// aggregator normalises everything to sourceServicePinId before bumping
// the aggregate, so this key always points at one logical service.
func ratingAggKey(chainName, sourceServicePinId string) []byte {
	return []byte(keyRatingAgg + chainName + ":" + sourceServicePinId)
}

// RatingPin is the persisted view of an individual /protocols/skill-service-rate
// PIN. We keep the original ServiceID alongside the normalised
// sourceServicePinId so the audit trail and any future detail endpoint can
// distinguish raw input from the resolved target.
type RatingPin struct {
	PinId             string `json:"pinId"`
	ChainName         string `json:"chainName"`
	RaterMetaId       string `json:"raterMetaId"`
	RaterGlobalMetaId string `json:"raterGlobalMetaId"`
	RaterAddress      string `json:"raterAddress"`

	// ServiceID is the rater-supplied target id. May be sourceServicePinId,
	// currentPinId or any version pinId.
	ServiceID string `json:"serviceID"`
	// SourceServicePinId is the normalised target after pin_to_source
	// lookup. Empty when the service is not (yet) indexed; the aggregate
	// is not bumped in that case.
	SourceServicePinId string `json:"sourceServicePinId"`

	ServicePaidTx string `json:"servicePaidTx"`
	Rate          int    `json:"rate"`
	Comment       string `json:"comment"`
	Timestamp     int64  `json:"timestamp"`
}

// RatingAggregate is a per-service rolling aggregate. Sum and Count are
// stored; Average is derived on read so we never have to worry about
// rounding drift across many updates.
type RatingAggregate struct {
	ChainName          string `json:"chainName"`
	SourceServicePinId string `json:"sourceServicePinId"`
	Sum                int64  `json:"sum"`
	Count              int64  `json:"count"`
}

// Average returns the floating-point average rating. Returns 0 for an
// empty aggregate so callers can blindly read it; the count field
// distinguishes "no ratings" from "average happens to be zero".
func (a *RatingAggregate) Average() float64 {
	if a == nil || a.Count == 0 {
		return 0
	}
	return float64(a.Sum) / float64(a.Count)
}

// --- ingestion ---------------------------------------------------------------

// processRatingPin handles a single /protocols/skill-service-rate PIN. It is
// called from the central dispatcher in process.go.
//
// Steps:
//  1. Parse contentSummary; drop the PIN on malformed JSON or out-of-range rate.
//  2. Dedup against the rating_pin namespace; if we have already counted this
//     pin id, do nothing.
//  3. Resolve the rater-supplied ServiceID to a sourceServicePinId via
//     loadServiceByAnyPinId (which understands the create/modify version
//     chain). When unresolved (service not yet indexed) we still persist the
//     rating pin so a later backfill can pick it up, but we do NOT bump the
//     aggregate yet — adding to an aggregate without knowing the target is
//     guaranteed to leave the data inconsistent.
//  4. Bump the aggregate (Sum += rate, Count += 1) and persist.
func (a *Aggregator) processRatingPin(pin *aggregator.PinInscription) error {
	if pin == nil || pin.Id == "" || pin.ChainName == "" {
		return nil
	}
	summary, ok := decodeRatingSummary(pin)
	if !ok {
		return nil
	}

	// Dedup: if we already stored this rating pin, ignore.
	if existing, _ := a.loadRatingPin(pin.ChainName, pin.Id); existing != nil {
		return nil
	}

	// Resolve the target ServiceID through the version-chain index. The
	// resolver consults pin_to_source first, then falls back to treating
	// the raw id as a sourceServicePinId. A missing service simply leaves
	// rec == nil; the rating is still recorded for later backfill.
	var sourcePinId string
	rec, err := a.loadServiceByAnyPinId(pin.ChainName, summary.ServiceID)
	if err != nil {
		return err
	}
	if rec != nil {
		sourcePinId = rec.SourceServicePinId
		if rec.ChainName != "" && rec.ChainName != pin.ChainName {
			// Cross-chain rating: per spec v1 we do not fold service
			// version chains across chains, so we also reject ratings
			// whose normalised target lives on another chain.
			log.Printf("[skillservice] rating pin %s on %s targets service %s/%s on different chain; skipped",
				pin.Id, pin.ChainName, rec.ChainName, rec.SourceServicePinId)
			sourcePinId = ""
		}
		if sourcePinId != "" && rec.SourceServicePinId != summary.ServiceID {
			log.Printf("[skillservice] rating pin %s normalised serviceID %s → %s",
				pin.Id, summary.ServiceID, rec.SourceServicePinId)
		}
	} else if summary.ServiceID != "" {
		log.Printf("[skillservice] rating pin %s targets unknown service %s/%s; persisted but not aggregated",
			pin.Id, pin.ChainName, summary.ServiceID)
	}

	rp := newRatingPin(pin, summary, sourcePinId)
	if err := a.saveRatingPin(rp); err != nil {
		return err
	}
	if sourcePinId == "" {
		return nil
	}
	return a.bumpRatingAggregate(pin.ChainName, sourcePinId, summary.Rate)
}

// LoadRatingAggregate returns the aggregate for one service, or (nil, nil) if
// no ratings have landed yet. Exposed for the list/detail handlers that
// land in M5/M6.
func (a *Aggregator) LoadRatingAggregate(chainName, sourceServicePinId string) (*RatingAggregate, error) {
	if chainName == "" || sourceServicePinId == "" {
		return nil, nil
	}
	raw, err := a.store.Get(NamespaceRate, ratingAggKey(chainName, sourceServicePinId))
	if err != nil || raw == nil {
		return nil, nil
	}
	var agg RatingAggregate
	if err := json.Unmarshal(raw, &agg); err != nil {
		return nil, fmt.Errorf("LoadRatingAggregate: corrupt entry %s/%s: %w",
			chainName, sourceServicePinId, err)
	}
	return &agg, nil
}

// --- helpers -----------------------------------------------------------------

func decodeRatingSummary(pin *aggregator.PinInscription) (RatingContentSummary, bool) {
	var s RatingContentSummary
	if len(pin.ContentBody) == 0 {
		return s, false
	}
	if err := json.Unmarshal(pin.ContentBody, &s); err != nil {
		return s, false
	}
	// Per spec the rate field is 1..5 inclusive. Anything outside that
	// range is discarded — out-of-range pins are typically authored by
	// faulty clients and adding them to the aggregate skews the average.
	if s.Rate < 1 || s.Rate > 5 {
		return s, false
	}
	if strings.TrimSpace(s.ServiceID) == "" {
		return s, false
	}
	return s, true
}

func newRatingPin(pin *aggregator.PinInscription, summary RatingContentSummary, normalisedSource string) *RatingPin {
	return &RatingPin{
		PinId:             pin.Id,
		ChainName:         pin.ChainName,
		RaterMetaId:       firstNonEmpty(pin.MetaId, pin.CreateMetaId),
		RaterGlobalMetaId: pin.GlobalMetaId,
		RaterAddress:      firstNonEmpty(pin.Address, pin.CreateAddress),

		ServiceID:          summary.ServiceID,
		SourceServicePinId: normalisedSource,

		ServicePaidTx: summary.ServicePaidTx,
		Rate:          summary.Rate,
		Comment:       summary.Comment,
		Timestamp:     pin.Timestamp,
	}
}

func (a *Aggregator) loadRatingPin(chainName, ratingPinId string) (*RatingPin, error) {
	if chainName == "" || ratingPinId == "" {
		return nil, errors.New("loadRatingPin: chainName and ratingPinId required")
	}
	raw, err := a.store.Get(NamespaceRate, ratingPinKey(chainName, ratingPinId))
	if err != nil || raw == nil {
		return nil, nil
	}
	var rp RatingPin
	if err := json.Unmarshal(raw, &rp); err != nil {
		return nil, fmt.Errorf("loadRatingPin: corrupt entry %s/%s: %w",
			chainName, ratingPinId, err)
	}
	return &rp, nil
}

func (a *Aggregator) saveRatingPin(rp *RatingPin) error {
	if rp == nil || rp.ChainName == "" || rp.PinId == "" {
		return errors.New("saveRatingPin: missing identity")
	}
	raw, err := json.Marshal(rp)
	if err != nil {
		return err
	}
	return a.store.Set(NamespaceRate, ratingPinKey(rp.ChainName, rp.PinId), raw)
}

// bumpRatingAggregate is the only mutator of the aggregate record. Callers
// must already have deduplicated the source rating PIN; calling this twice
// for the same rating PIN will double-count.
func (a *Aggregator) bumpRatingAggregate(chainName, sourcePinId string, rate int) error {
	agg, err := a.LoadRatingAggregate(chainName, sourcePinId)
	if err != nil {
		return err
	}
	if agg == nil {
		agg = &RatingAggregate{
			ChainName:          chainName,
			SourceServicePinId: sourcePinId,
		}
	}
	agg.Sum += int64(rate)
	agg.Count += 1
	raw, err := json.Marshal(agg)
	if err != nil {
		return err
	}
	return a.store.Set(NamespaceRate, ratingAggKey(chainName, sourcePinId), raw)
}
