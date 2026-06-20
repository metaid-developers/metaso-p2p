package userinfo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultProfileMode = "local-first"

	remoteLookupByMetaId       = "metaid"
	remoteLookupByAddress      = "address"
	remoteLookupByGlobalMetaId = "globalmetaid"
)

type remoteProfileLookup interface {
	LookupByMetaId(ctx context.Context, metaid string) (*UserProfile, error)
	LookupByAddress(ctx context.Context, address string) (*UserProfile, error)
	LookupByGlobalMetaId(ctx context.Context, globalMetaId string) (*UserProfile, error)
}

type remoteProfileQuery struct {
	kind  string
	value string
}

type remoteProfileQueries []remoteProfileQuery

func (a *Aggregator) configureRemoteProfileLookupFromEnv() {
	a.profileMode = normaliseProfileMode(os.Getenv("METASO_P2P_PROFILE_MODE"))
	a.allowRemoteFallback = parseBoolEnvDefault(os.Getenv("METASO_P2P_PROFILE_ALLOW_REMOTE_FALLBACK"), true)

	baseURL := strings.TrimSpace(os.Getenv("METASO_P2P_PROFILE_REMOTE_BASE_URL"))
	if baseURL == "" || a.profileMode == "local-only" {
		return
	}
	a.remoteLookup = newHTTPRemoteProfileLookup(baseURL)
}

func normaliseProfileMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "remote-only", "local-only", "local-first":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return defaultProfileMode
	}
}

func parseBoolEnvDefault(raw string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return fallback
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func (a *Aggregator) lookupByMetaId(ctx context.Context, metaid string) (*UserProfile, error) {
	profile, err := a.getProfile(metaid)
	if err != nil {
		return nil, err
	}
	return a.completeProfile(ctx, profile, remoteProfileQueries{
		{kind: remoteLookupByMetaId, value: metaid},
		// Some MVC local views use the address as the MetaID key. If the
		// remote metaid route misses, the address route can still recover
		// the canonical profile and chat key.
		{kind: remoteLookupByAddress, value: metaid},
	}, metaid)
}

func (a *Aggregator) lookupByAddress(ctx context.Context, address string) (*UserProfile, error) {
	profile, err := a.findProfileByAddress(address)
	if err != nil {
		return nil, err
	}
	return a.completeProfile(ctx, profile, remoteProfileQueries{
		{kind: remoteLookupByAddress, value: address},
	}, "")
}

func (a *Aggregator) lookupByGlobalMetaId(ctx context.Context, globalMetaId string) (*UserProfile, error) {
	profile, err := a.findProfileByGlobalMetaId(globalMetaId)
	if err != nil {
		return nil, err
	}
	return a.completeProfile(ctx, profile, remoteProfileQueries{
		{kind: remoteLookupByGlobalMetaId, value: globalMetaId},
		// Legacy Bot Hub records sometimes carried the chain address in the
		// providerGlobalMetaId field. Delivery fallback flows may then call
		// /info/globalmetaid/<address>, so recover via address/metaid routes.
		{kind: remoteLookupByAddress, value: globalMetaId},
		{kind: remoteLookupByMetaId, value: globalMetaId},
	}, globalMetaId)
}

func (a *Aggregator) completeProfile(ctx context.Context, local *UserProfile, queries remoteProfileQueries, aliasKey string) (*UserProfile, error) {
	if !a.shouldFetchRemote(local) {
		return local, nil
	}

	remote, err := a.fetchRemoteProfile(ctx, queries)
	if err != nil {
		log.Printf("[userinfo] remote profile fallback failed: %v", err)
		if local == nil {
			return nil, err
		}
		return local, nil
	}
	if remote == nil {
		return local, nil
	}

	merged := mergeUserProfiles(local, remote)
	persisted, err := a.persistMergedProfile(aliasKey, merged)
	if err != nil {
		return nil, err
	}
	return persisted, nil
}

func (a *Aggregator) shouldFetchRemote(local *UserProfile) bool {
	if a.remoteLookup == nil || a.profileMode == "local-only" {
		return false
	}
	if a.profileMode == "remote-only" {
		return true
	}
	if !a.allowRemoteFallback {
		return false
	}
	return profileNeedsRemoteCompletion(local)
}

func (a *Aggregator) fetchRemoteProfile(ctx context.Context, queries remoteProfileQueries) (*UserProfile, error) {
	seen := make(map[string]bool)
	var merged *UserProfile
	for _, q := range queries {
		q.value = strings.TrimSpace(q.value)
		if q.value == "" {
			continue
		}
		key := q.kind + ":" + q.value
		if seen[key] {
			continue
		}
		seen[key] = true

		var (
			profile *UserProfile
			err     error
		)
		switch q.kind {
		case remoteLookupByMetaId:
			profile, err = a.remoteLookup.LookupByMetaId(ctx, q.value)
		case remoteLookupByAddress:
			profile, err = a.remoteLookup.LookupByAddress(ctx, q.value)
		case remoteLookupByGlobalMetaId:
			profile, err = a.remoteLookup.LookupByGlobalMetaId(ctx, q.value)
		default:
			continue
		}
		if err != nil {
			return nil, err
		}
		if profile != nil {
			merged = mergeUserProfiles(merged, profile)
			if !profileNeedsRemoteCompletion(merged) {
				return merged, nil
			}
		}
	}
	return merged, nil
}

func profileNeedsRemoteCompletion(profile *UserProfile) bool {
	if profile == nil {
		return true
	}
	return strings.TrimSpace(profile.ChatPublicKey) == "" ||
		strings.TrimSpace(profile.GlobalMetaID) == "" ||
		strings.TrimSpace(profile.MetaID) == "" ||
		strings.TrimSpace(profile.Address) == "" ||
		avatarNeedsRemoteCompletion(profile)
}

func (a *Aggregator) persistMergedProfile(aliasKey string, profile *UserProfile) (*UserProfile, error) {
	if profile == nil {
		return nil, nil
	}
	profile, err := a.mergeWithStoredProfiles(profile)
	if err != nil {
		return nil, err
	}
	if err := a.saveProfile(profile); err != nil {
		return nil, err
	}
	if aliasKey = strings.TrimSpace(aliasKey); aliasKey != "" && aliasKey != profile.MetaID {
		if err := a.saveProfileAtKey(aliasKey, profile); err != nil {
			return nil, err
		}
		a.cache.InvalidateByPrefix("profile:" + aliasKey)
	}
	if profile.MetaID != "" {
		a.cache.InvalidateByPrefix("profile:" + profile.MetaID)
	}
	return profile, nil
}

func (a *Aggregator) mergeWithStoredProfiles(profile *UserProfile) (*UserProfile, error) {
	if a == nil || profile == nil {
		return profile, nil
	}

	merged := profile
	seen := make(map[string]struct{}, 3)
	loaders := []func() (*UserProfile, error){
		func() (*UserProfile, error) {
			metaid := strings.TrimSpace(profile.MetaID)
			if metaid == "" {
				return nil, nil
			}
			return a.getProfile(metaid)
		},
		func() (*UserProfile, error) {
			address := strings.TrimSpace(profile.Address)
			if address == "" {
				return nil, nil
			}
			return a.findProfileByAddress(address)
		},
		func() (*UserProfile, error) {
			globalMetaID := strings.TrimSpace(profile.GlobalMetaID)
			if globalMetaID == "" {
				return nil, nil
			}
			return a.findProfileByGlobalMetaId(globalMetaID)
		},
	}

	for _, load := range loaders {
		existing, err := load()
		if err != nil {
			return nil, err
		}
		if existing == nil {
			continue
		}
		key := storedProfileIdentityKey(existing)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		merged = mergeUserProfiles(existing, merged)
	}

	return merged, nil
}

func storedProfileIdentityKey(profile *UserProfile) string {
	if profile == nil {
		return ""
	}
	for _, value := range []string{profile.MetaID, profile.GlobalMetaID, profile.Address} {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
			return value
		}
	}
	return ""
}

func mergeUserProfiles(local, remote *UserProfile) *UserProfile {
	if local == nil {
		cp := *remote
		return &cp
	}
	out := *local

	// Prefer canonical remote identity fields because local partial MVC
	// windows can key profiles by address when historical init data is absent.
	overrideString(&out.MetaID, remote.MetaID)
	overrideString(&out.GlobalMetaID, remote.GlobalMetaID)
	overrideString(&out.Address, remote.Address)

	fillString(&out.Name, remote.Name)
	fillString(&out.NameId, remote.NameId)
	fillAvatarFields(&out, remote)
	fillString(&out.NftAvatar, remote.NftAvatar)
	fillString(&out.Bio, remote.Bio)
	fillString(&out.BioId, remote.BioId)
	fillString(&out.Background, remote.Background)
	fillString(&out.ChatPublicKey, remote.ChatPublicKey)
	fillString(&out.ChatPublicKeyId, remote.ChatPublicKeyId)
	fillString(&out.ChainName, remote.ChainName)

	return &out
}

func fillString(dst *string, src string) {
	if shouldFillString(*dst, src) {
		*dst = src
	}
}

func shouldFillString(dst, src string) bool {
	dst = strings.TrimSpace(dst)
	src = strings.TrimSpace(src)
	if src == "" {
		return false
	}
	return dst == "" || dst == "/content/"
}

func avatarNeedsRemoteCompletion(profile *UserProfile) bool {
	if profile == nil {
		return true
	}
	avatar := strings.TrimSpace(profile.Avatar)
	if avatar == "" || avatar == "/content/" || isLegacyManAPIContentURL(avatar) {
		return true
	}
	return false
}

func fillAvatarFields(out *UserProfile, remote *UserProfile) {
	if out == nil || remote == nil {
		return
	}
	srcAvatar, srcID := normaliseAvatarReference(remote.Avatar, remote.AvatarId)
	if srcAvatar == "" && srcID == "" {
		return
	}

	dstAvatar := strings.TrimSpace(out.Avatar)
	dstID := firstNonEmptyString(out.AvatarId, avatarIDFromReference(dstAvatar))
	if shouldUseRemoteAvatar(dstAvatar, dstID, srcAvatar, srcID) {
		if srcAvatar != "" {
			out.Avatar = srcAvatar
		}
		if srcID != "" {
			out.AvatarId = srcID
		}
		return
	}
	if strings.TrimSpace(out.AvatarId) == "" && dstID != "" {
		out.AvatarId = dstID
	}
}

func shouldUseRemoteAvatar(dstAvatar, dstID, srcAvatar, srcID string) bool {
	dstAvatar = strings.TrimSpace(dstAvatar)
	dstID = strings.TrimSpace(dstID)
	srcAvatar = strings.TrimSpace(srcAvatar)
	srcID = strings.TrimSpace(srcID)
	if srcAvatar == "" && srcID == "" {
		return false
	}
	if dstAvatar == "" || dstAvatar == "/content/" || isLegacyManAPIContentURL(dstAvatar) {
		return true
	}
	return srcID != "" && dstID != "" && !strings.EqualFold(srcID, dstID)
}

func normaliseAvatarReference(avatar, avatarID string) (string, string) {
	avatar = strings.TrimSpace(avatar)
	avatarID = strings.TrimSpace(avatarID)
	if avatarID == "" {
		avatarID = avatarIDFromReference(avatar)
	}
	if avatarID == "" {
		return avatar, ""
	}
	if avatar == "" || avatar == "/content/" || isContentBackedAvatarReference(avatar) {
		return "/content/" + avatarID, avatarID
	}
	return avatar, avatarID
}

func isContentBackedAvatarReference(avatar string) bool {
	avatar = strings.TrimSpace(avatar)
	lower := strings.ToLower(avatar)
	return strings.HasPrefix(avatar, "/content/") ||
		strings.HasPrefix(lower, "metafile:") ||
		isLegacyManAPIContentURL(avatar) ||
		isFileIndexerContentURL(avatar)
}

func avatarIDFromReference(avatar string) string {
	avatar = strings.TrimSpace(avatar)
	if avatar == "" || avatar == "/content/" {
		return ""
	}
	lower := strings.ToLower(avatar)
	switch {
	case strings.HasPrefix(avatar, "/content/"):
		return strings.Trim(strings.TrimPrefix(avatar, "/content/"), "/")
	case strings.HasPrefix(lower, "metafile://"):
		return strings.Trim(strings.TrimPrefix(avatar, "metafile://"), "/")
	case strings.HasPrefix(lower, "metafile:"):
		return strings.Trim(strings.TrimPrefix(avatar, "metafile:"), "/")
	case isLegacyManAPIContentURL(avatar), isFileIndexerContentURL(avatar):
		if parsed, err := url.Parse(avatar); err == nil {
			parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
			if len(parts) > 0 {
				return strings.TrimSpace(parts[len(parts)-1])
			}
		}
	}
	return ""
}

func isLegacyManAPIContentURL(asset string) bool {
	parsed, err := url.Parse(strings.TrimSpace(asset))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Host)
	return host == "manapi.metaid.io" && strings.HasPrefix(parsed.Path, "/content/")
}

func isFileIndexerContentURL(asset string) bool {
	parsed, err := url.Parse(strings.TrimSpace(asset))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Host)
	return host == "file.metaid.io" && strings.HasPrefix(parsed.Path, "/metafile-indexer/content/")
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func overrideString(dst *string, src string) {
	if strings.TrimSpace(src) != "" {
		*dst = src
	}
}

type httpRemoteProfileLookup struct {
	baseURL string
	client  *http.Client
}

func newHTTPRemoteProfileLookup(baseURL string) *httpRemoteProfileLookup {
	return &httpRemoteProfileLookup{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		client:  &http.Client{Timeout: 3 * time.Second},
	}
}

func (l *httpRemoteProfileLookup) LookupByMetaId(ctx context.Context, metaid string) (*UserProfile, error) {
	return l.lookup(ctx, remoteLookupByMetaId, metaid)
}

func (l *httpRemoteProfileLookup) LookupByAddress(ctx context.Context, address string) (*UserProfile, error) {
	return l.lookup(ctx, remoteLookupByAddress, address)
}

func (l *httpRemoteProfileLookup) LookupByGlobalMetaId(ctx context.Context, globalMetaId string) (*UserProfile, error) {
	return l.lookup(ctx, remoteLookupByGlobalMetaId, globalMetaId)
}

func (l *httpRemoteProfileLookup) lookup(ctx context.Context, kind, value string) (*UserProfile, error) {
	if l == nil || l.baseURL == "" || strings.TrimSpace(value) == "" {
		return nil, nil
	}

	endpoint := fmt.Sprintf("%s/info/%s/%s", l.baseURL, kind, url.PathEscape(strings.TrimSpace(value)))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("remote profile %s returned HTTP %d", kind, resp.StatusCode)
	}

	var envelope remoteProfileEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	if envelope.Code != apiSuccessCode {
		return nil, nil
	}
	return envelope.Data.toUserProfile(), nil
}

const apiSuccessCode = 1

type remoteProfileEnvelope struct {
	Code int                  `json:"code"`
	Data remoteProfilePayload `json:"data"`
}

type remoteProfilePayload struct {
	GlobalMetaID    string          `json:"globalMetaId"`
	MetaID          string          `json:"metaid"`
	Address         string          `json:"address"`
	Name            string          `json:"name"`
	NameId          string          `json:"nameId"`
	Avatar          string          `json:"avatar"`
	AvatarId        string          `json:"avatarId"`
	NftAvatar       string          `json:"nftAvatar"`
	Bio             json.RawMessage `json:"bio"`
	BioId           string          `json:"bioId"`
	Background      string          `json:"background"`
	ChatPublicKey   string          `json:"chatpubkey"`
	ChatPublicKeyId string          `json:"chatpubkeyId"`
	ChainName       string          `json:"chainName"`
}

func (p remoteProfilePayload) toUserProfile() *UserProfile {
	if p.MetaID == "" && p.GlobalMetaID == "" && p.Address == "" {
		return nil
	}
	avatar, avatarID := normaliseAvatarReference(p.Avatar, p.AvatarId)
	return &UserProfile{
		GlobalMetaID:    p.GlobalMetaID,
		MetaID:          p.MetaID,
		Address:         p.Address,
		Name:            p.Name,
		NameId:          p.NameId,
		Avatar:          avatar,
		AvatarId:        avatarID,
		NftAvatar:       p.NftAvatar,
		Bio:             rawJSONToString(p.Bio),
		BioId:           p.BioId,
		Background:      p.Background,
		ChatPublicKey:   p.ChatPublicKey,
		ChatPublicKeyId: p.ChatPublicKeyId,
		ChainName:       p.ChainName,
	}
}

func rawJSONToString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err == nil {
		return compact.String()
	}
	return string(raw)
}
