package userinfo

import (
	"encoding/json"
	"strings"
)

// metaIDSearchDoc is the precomputed, lowercased per-user search document
// behind /api/metaid/list. Docs live only in memory: they are rebuilt from
// the stored profiles (plus revision and creation records) at startup and
// kept in sync on every profile write. A profile with no searchable /info
// content gets no doc and is excluded from both keyword search and the
// unfiltered feed. See docs/specs/2026-07-28-metaid-search-api.md.
type metaIDSearchDoc struct {
	globalMetaID  string
	metaID        string
	address       string
	chainName     string
	name          string
	avatarID      string
	bio           string
	chatSkills    []string
	hasChatPubkey bool
	hasHomepage   bool
	createdAt     int64 // unix seconds; 0 = unknown
	updatedAt     int64 // unix seconds; 0 = no timestamped /info revision

	nameExact   string // lowercased name with all whitespace removed (exact-name boost key)
	nameText    string // lowercased name corpus layer
	skillText   string // lowercased joined skill names corpus layer
	profileText string // lowercased bio/role/soul/goal/persona/llm corpus layer
}

// buildMetaIDSearchDoc derives the search document from a profile snapshot.
// Returns nil when the profile carries no searchable /info content, which is
// what keeps "registered but never filled in" identities out of the corpus.
func buildMetaIDSearchDoc(profile *UserProfile) *metaIDSearchDoc {
	if profile == nil {
		return nil
	}
	metaID := strings.TrimSpace(profile.MetaID)
	if metaID == "" {
		return nil
	}
	skills := parseMetaIDChatSkills(profile.ChatSkills)
	llm := parseMetaIDLLM(profile.LLM)
	llmText := strings.TrimSpace(strings.Join([]string{llm.Provider, llm.Model, llm.Name}, " "))

	name := strings.TrimSpace(profile.Name)
	bio := strings.TrimSpace(profile.Bio)
	role := strings.TrimSpace(profile.Role)
	soul := strings.TrimSpace(profile.Soul)
	goal := strings.TrimSpace(profile.Goal)
	persona := strings.TrimSpace(profile.Persona)
	if name == "" && bio == "" && role == "" && soul == "" && goal == "" &&
		persona == "" && llmText == "" && len(skills) == 0 {
		return nil
	}

	return &metaIDSearchDoc{
		globalMetaID:  strings.TrimSpace(profile.GlobalMetaID),
		metaID:        metaID,
		address:       strings.TrimSpace(profile.Address),
		chainName:     strings.TrimSpace(profile.ChainName),
		name:          name,
		avatarID:      strings.TrimSpace(profile.AvatarId),
		bio:           bio,
		chatSkills:    skills,
		hasChatPubkey: strings.TrimSpace(profile.ChatPublicKey) != "",
		hasHomepage:   strings.TrimSpace(profile.Homepage) != "",
		nameExact:     metaIDExactNameKey(name),
		nameText:      strings.ToLower(name),
		skillText:     strings.ToLower(strings.Join(skills, "\n")),
		profileText:   strings.ToLower(strings.Join([]string{bio, role, soul, goal, persona, llmText}, "\n")),
	}
}

func metaIDSearchDocKey(metaID string) string {
	return strings.ToLower(strings.TrimSpace(metaID))
}

// upsertSearchDoc syncs the registry with the given profile snapshot. It is
// called from the single persistence funnel (saveProfileAtKey), so every
// write path — block pins, mempool pins, backfill, remote completion — keeps
// the corpus current. createdAt/updatedAt are carried over from the existing
// doc; they are maintained separately via noteSearchDocRevision /
// noteSearchDocCreated and the startup scans.
func (a *Aggregator) upsertSearchDoc(profile *UserProfile) {
	if a == nil || profile == nil {
		return
	}
	key := metaIDSearchDocKey(profile.MetaID)
	if key == "" {
		return
	}
	doc := buildMetaIDSearchDoc(profile)

	a.searchDocsMu.Lock()
	defer a.searchDocsMu.Unlock()
	if a.searchDocs == nil {
		a.searchDocs = make(map[string]*metaIDSearchDoc)
	}
	if doc == nil {
		delete(a.searchDocs, key)
		return
	}
	if existing, ok := a.searchDocs[key]; ok {
		doc.createdAt = existing.createdAt
		doc.updatedAt = existing.updatedAt
	}
	a.searchDocs[key] = doc
}

func (a *Aggregator) getSearchDoc(metaID string) *metaIDSearchDoc {
	if a == nil {
		return nil
	}
	a.searchDocsMu.RLock()
	defer a.searchDocsMu.RUnlock()
	return a.searchDocs[metaIDSearchDocKey(metaID)]
}

// noteSearchDocRevision bumps a doc's updatedAt from an applied /info pin.
// revisionMillis is the normalised pin timestamp (millis); values <= 0 (pins
// carrying only a genesis height) leave updatedAt untouched.
func (a *Aggregator) noteSearchDocRevision(metaID string, revisionMillis int64) {
	if a == nil || revisionMillis <= 0 {
		return
	}
	key := metaIDSearchDocKey(metaID)
	if key == "" {
		return
	}
	seconds := revisionMillis / 1000
	a.searchDocsMu.Lock()
	defer a.searchDocsMu.Unlock()
	existing, ok := a.searchDocs[key]
	if !ok || seconds <= existing.updatedAt {
		return
	}
	updated := *existing
	updated.updatedAt = seconds
	a.searchDocs[key] = &updated
}

// noteSearchDocCreated records the MetaID registration time (unix seconds) on
// an existing doc, keeping the earliest value seen.
func (a *Aggregator) noteSearchDocCreated(metaID string, createdSeconds int64) {
	if a == nil || createdSeconds <= 0 {
		return
	}
	key := metaIDSearchDocKey(metaID)
	if key == "" {
		return
	}
	a.searchDocsMu.Lock()
	defer a.searchDocsMu.Unlock()
	existing, ok := a.searchDocs[key]
	if !ok || (existing.createdAt > 0 && existing.createdAt <= createdSeconds) {
		return
	}
	updated := *existing
	updated.createdAt = createdSeconds
	a.searchDocs[key] = &updated
}

// backfillSearchDocCreated lazily fills a doc's createdAt from the stored
// globalMetaId creation record. Runtime pin order is usually "init first,
// /info later": the init pin lands before any searchable content exists, so
// the doc is born with createdAt unset — the next /info write picks the
// record up here. Safe to call on every /info write: it is a no-op once
// createdAt is known.
func (a *Aggregator) backfillSearchDocCreated(metaID, globalMetaID string) {
	if a == nil || a.store == nil {
		return
	}
	doc := a.getSearchDoc(metaID)
	if doc == nil || doc.createdAt > 0 || strings.TrimSpace(globalMetaID) == "" {
		return
	}
	raw, err := a.store.Get(namespace, globalMetaIDCreationKey(globalMetaID))
	if err != nil || len(raw) == 0 {
		return
	}
	var record globalMetaIDCreationRecord
	if err := json.Unmarshal(raw, &record); err != nil || record.CreatedAt <= 0 {
		return
	}
	a.noteSearchDocCreated(metaID, record.CreatedAt/1000)
}

// warmSearchDocs rebuilds the registry at startup: one pass over stored
// profiles for the corpus, one over per-path revision markers for updatedAt,
// and one over globalMetaId creation records for createdAt. The passes are
// sequential ScanPrefix calls; callbacks only touch the registry mutex and
// never re-enter the store.
func (a *Aggregator) warmSearchDocs() error {
	if a == nil || a.store == nil {
		return nil
	}
	a.searchDocsMu.Lock()
	if a.searchDocs == nil {
		a.searchDocs = make(map[string]*metaIDSearchDoc)
	}
	a.searchDocsMu.Unlock()

	if err := a.store.ScanPrefix(namespace, profileKey(""), func(_, value []byte) error {
		var profile UserProfile
		if err := json.Unmarshal(value, &profile); err != nil {
			return nil
		}
		a.upsertSearchDoc(&profile)
		return nil
	}); err != nil {
		return err
	}

	// updatedAt = max per-/info revision timestamp (revisions store millis).
	// Keys look like info-revision:<metaid>:<base64url(path)>; metaid itself
	// never contains ':'.
	if err := a.store.ScanPrefix(namespace, []byte(infoRevisionPrefix), func(key, value []byte) error {
		rest := string(key[len(infoRevisionPrefix):])
		idx := strings.LastIndex(rest, ":")
		if idx <= 0 {
			return nil
		}
		var revision infoRevision
		if err := json.Unmarshal(value, &revision); err != nil {
			return nil
		}
		a.noteSearchDocRevision(rest[:idx], revision.Timestamp)
		return nil
	}); err != nil {
		return err
	}

	// createdAt from the globalMetaId creation records (millis).
	if err := a.store.ScanPrefix(namespace, []byte(globalMetaIDCreationKeyPrefix), func(_, value []byte) error {
		var record globalMetaIDCreationRecord
		if err := json.Unmarshal(value, &record); err != nil {
			return nil
		}
		if record.CreatedAt > 0 {
			a.noteSearchDocCreated(record.MetaID, record.CreatedAt/1000)
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

// MetaIDLLMInfo is the parsed form of /info/llm, mirroring the bothomepage
// rules: a JSON object yields provider/model/name, a plain string is treated
// as the provider.
type MetaIDLLMInfo struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Name     string `json:"name,omitempty"`
}

// parseMetaIDChatSkills mirrors bothomepage's parseChatSkills: a JSON
// array/object yields its skill name list (allow/allowChatSkills/chatSkills/
// skills/tools keys, first non-empty wins), a plain string is a single skill,
// anything unparseable yields nil.
func parseMetaIDChatSkills(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[") {
		var decoded any
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			return nil
		}
		return metaIDStringSlice(decoded)
	}
	return []string{raw}
}

func parseMetaIDLLM(raw string) MetaIDLLMInfo {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return MetaIDLLMInfo{}
	}
	if strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[") {
		var decoded any
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			return MetaIDLLMInfo{}
		}
		return metaIDLLMValue(decoded)
	}
	return MetaIDLLMInfo{Provider: raw}
}

func metaIDLLMValue(value any) MetaIDLLMInfo {
	switch typed := value.(type) {
	case string:
		return MetaIDLLMInfo{Provider: strings.TrimSpace(typed)}
	case map[string]any:
		return MetaIDLLMInfo{
			Provider: metaIDFirstNonEmptyString(metaIDString(typed["provider"]), metaIDString(typed["primaryProvider"])),
			Model:    metaIDString(typed["model"]),
			Name:     metaIDFirstNonEmptyString(metaIDString(typed["name"]), metaIDString(typed["displayName"])),
		}
	default:
		return MetaIDLLMInfo{}
	}
}

func metaIDString(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func metaIDFirstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func metaIDStringSlice(value any) []string {
	switch typed := value.(type) {
	case string:
		if trimmed := strings.TrimSpace(typed); trimmed != "" {
			return []string{trimmed}
		}
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := metaIDString(item); text != "" {
				out = append(out, text)
			}
		}
		return out
	case map[string]any:
		for _, key := range []string{"allowChatSkills", "allow", "chatSkills", "skills", "tools"} {
			if out := metaIDStringSlice(typed[key]); len(out) > 0 {
				return out
			}
		}
	}
	return nil
}

// metaIDExactNameKey normalises a name (or keyword) for the exact-name boost
// comparison: lowercase with all whitespace removed, so "Alice Wang" and
// "alicewang" are considered the same exact name.
func metaIDExactNameKey(raw string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(raw))), "")
}
