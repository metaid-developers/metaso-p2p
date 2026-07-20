package userinfo

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
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

func (a *Aggregator) shouldApplyInfoPin(identities []string, path string, pin *aggregator.PinInscription, profile *UserProfile) (bool, error) {
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

	var current *infoRevision
	for _, identity := range identities {
		raw, err := a.store.Get(namespace, infoRevisionKey(identity, path))
		if err != nil || len(raw) == 0 {
			continue
		}
		var revision infoRevision
		if err := json.Unmarshal(raw, &revision); err != nil {
			continue
		}
		if current == nil || infoRevisionAfter(revision, *current) {
			copy := revision
			current = &copy
		}
	}
	if current == nil {
		return true, nil
	}
	if current.PinID != "" && strings.EqualFold(current.PinID, candidate.PinID) {
		if candidate.Confirmed && !current.Confirmed {
			return true, nil
		}
		// A previous buggy writer could persist the revision while losing the
		// corresponding field in the shared profile snapshot. Replaying the
		// same pin is safe and repairs that split state.
		return !profileMatchesInfoPin(profile, path, pin), nil
	}
	return infoRevisionAfter(candidate, *current), nil
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

func (a *Aggregator) saveProfileWithInfoRevision(profile *UserProfile, metaid, path string, pin *aggregator.PinInscription) error {
	if a == nil || a.store == nil || profile == nil || pin == nil {
		return nil
	}
	revision := revisionForPin(pin)
	rawRevision, err := json.Marshal(revision)
	if err != nil {
		return err
	}
	canonicalMetaID := strings.TrimSpace(profile.MetaID)
	if canonicalMetaID == "" {
		canonicalMetaID = strings.TrimSpace(metaid)
		profile.MetaID = canonicalMetaID
	}
	entries, err := profileStorageEntries(canonicalMetaID, profile)
	if err != nil {
		return err
	}
	entries = append(entries, storageKeyValue(infoRevisionKey(canonicalMetaID, path), rawRevision))
	return a.store.SetBatch(namespace, entries)
}

func storageKeyValue(key, value []byte) storage.KeyValue {
	return storage.KeyValue{Key: key, Value: value}
}

func revisionForPin(pin *aggregator.PinInscription) infoRevision {
	return infoRevision{
		Timestamp:     normalisePinTimestampMillis(pin.Timestamp),
		GenesisHeight: pin.GenesisHeight,
		PinID:         strings.TrimSpace(pin.Id),
		Confirmed:     pin.GenesisHeight > 0,
	}
}

func infoRevisionAfter(candidate, current infoRevision) bool {
	if candidate.Timestamp != current.Timestamp {
		return candidate.Timestamp > current.Timestamp
	}
	if candidate.GenesisHeight != current.GenesisHeight {
		return candidate.GenesisHeight > current.GenesisHeight
	}
	if candidate.Confirmed != current.Confirmed {
		return candidate.Confirmed
	}
	return candidate.PinID > current.PinID
}

func profileMatchesInfoPin(profile *UserProfile, path string, pin *aggregator.PinInscription) bool {
	if profile == nil || pin == nil {
		return false
	}
	path = normaliseInfoPath(path)
	if len(pin.ContentBody) == 0 {
		switch path {
		case "/info/avatar":
			return profile.Avatar == "" && profile.AvatarId == ""
		case "/info/llm":
			return profile.LLM == "" && profile.LLMId == ""
		case "/info/persona":
			return profile.Persona == "" && profile.PersonaId == ""
		case "/info/homepage":
			return profile.Homepage == "" && profile.HomepageId == ""
		}
	}
	wantID := strings.TrimSpace(pin.Id)
	switch path {
	case "/info/name":
		return profile.NameId == wantID
	case "/info/avatar":
		return profile.AvatarId == wantID
	case "/info/nft-avatar":
		return profile.NftAvatar == "/content/"+wantID
	case "/info/bio":
		return profile.BioId == wantID
	case "/info/role":
		return profile.RoleId == wantID
	case "/info/soul":
		return profile.SoulId == wantID
	case "/info/goal":
		return profile.GoalId == wantID
	case "/info/chatskills":
		return profile.ChatSkillsId == wantID
	case "/info/llm":
		return profile.LLMId == wantID
	case "/info/persona":
		return profile.PersonaId == wantID
	case "/info/homepage":
		return profile.HomepageId == wantID
	case "/info/background":
		return profile.BackgroundId == wantID
	case "/info/chatpubkey":
		return profile.ChatPublicKeyId == wantID
	default:
		return false
	}
}
