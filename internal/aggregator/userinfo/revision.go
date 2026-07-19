package userinfo

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

const infoRevisionPrefix = "info-revision:"

type infoRevision struct {
	Timestamp     int64  `json:"timestamp"`
	GenesisHeight int64  `json:"genesisHeight"`
	PinID         string `json:"pinId"`
	Confirmed     bool   `json:"confirmed"`
}

func isLatestProfileInfoPath(path string) bool {
	switch normaliseInfoPath(path) {
	case "/info/name", "/info/avatar", "/info/nft-avatar", "/info/bio",
		"/info/role", "/info/soul", "/info/goal", "/info/chatskills",
		"/info/llm", "/info/persona", "/info/homepage", "/info/background",
		"/info/chatpubkey":
		return true
	default:
		return false
	}
}

func infoRevisionKey(metaid, path string) []byte {
	encodedPath := base64.RawURLEncoding.EncodeToString([]byte(normaliseInfoPath(path)))
	return []byte(infoRevisionPrefix + strings.TrimSpace(metaid) + ":" + encodedPath)
}

func (a *Aggregator) shouldApplyInfoPin(metaid, path string, pin *aggregator.PinInscription) (bool, error) {
	if a == nil || a.store == nil || pin == nil {
		return false, nil
	}

	candidate := revisionForPin(pin)
	// Fixtures and legacy callers may not provide chain ordering metadata.
	// Preserve their arrival-order behavior; real block/backfill pins carry
	// either a timestamp or a genesis height.
	if candidate.Timestamp == 0 && candidate.GenesisHeight <= 0 {
		return true, nil
	}

	raw, err := a.store.Get(namespace, infoRevisionKey(metaid, path))
	if err != nil || len(raw) == 0 {
		return true, nil
	}
	var current infoRevision
	if err := json.Unmarshal(raw, &current); err != nil {
		return true, nil
	}
	if current.PinID != "" && strings.EqualFold(current.PinID, candidate.PinID) {
		return candidate.Confirmed && !current.Confirmed, nil
	}
	return infoRevisionAfter(candidate, current), nil
}

func (a *Aggregator) saveInfoRevision(metaid, path string, pin *aggregator.PinInscription) error {
	if a == nil || a.store == nil || pin == nil {
		return nil
	}
	revision := revisionForPin(pin)
	raw, err := json.Marshal(revision)
	if err != nil {
		return err
	}
	return a.store.Set(namespace, infoRevisionKey(metaid, path), raw)
}

func revisionForPin(pin *aggregator.PinInscription) infoRevision {
	return infoRevision{
		Timestamp:     normalisePinTimestampMillis(pin.Timestamp),
		GenesisHeight: pin.GenesisHeight,
		PinID:         strings.TrimSpace(pin.Id),
		Confirmed:     pin.GenesisHeight >= 0,
	}
}

func infoRevisionAfter(candidate, current infoRevision) bool {
	if candidate.Confirmed != current.Confirmed {
		return candidate.Confirmed
	}
	if candidate.Timestamp != current.Timestamp {
		return candidate.Timestamp > current.Timestamp
	}
	if candidate.GenesisHeight != current.GenesisHeight {
		return candidate.GenesisHeight > current.GenesisHeight
	}
	return candidate.PinID > current.PinID
}
