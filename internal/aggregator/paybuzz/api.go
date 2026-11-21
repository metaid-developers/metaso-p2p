package paybuzz

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/api"
)

// AuthorDTO is the enriched author block returned on PayBuzz read endpoints.
type AuthorDTO struct {
	GlobalMetaId string `json:"globalMetaId"`
	MetaId       string `json:"metaid,omitempty"`
	Address      string `json:"address"`
	Name         string `json:"name"`
	Avatar       string `json:"avatar"`
}

// ItemDTO is the wire shape returned by GET /api/paybuzz/info and
// GET /api/paybuzz/list.
type ItemDTO struct {
	PaybuzzPinId     string         `json:"paybuzzPinId"`
	PinId            string         `json:"pinId"`
	CurrentPinId     string         `json:"currentPinId"`
	ChainName        string         `json:"chainName"`
	Title            string         `json:"title"`
	Preview          string         `json:"preview"`
	ContentType      string         `json:"contentType"`
	WordCount        int            `json:"wordCount"`
	Encryption       EncryptionMeta `json:"encryption"`
	AuthorMetaId     string         `json:"authorMetaId"`
	AuthorMvcAddress string         `json:"authorMvcAddress"`
	AuthorName       string         `json:"authorName,omitempty"`
	AuthorAvatar     string         `json:"authorAvatar,omitempty"`
	Author           AuthorDTO      `json:"author"`
	ShopMetaAppPinId string         `json:"shopMetaAppPinId,omitempty"`
	UnlockRules      UnlockRules    `json:"unlockRules"`
	CreatedAt        int64          `json:"createdAt"`
	UpdatedAt        int64          `json:"updatedAt"`
	Confirmed        bool           `json:"confirmed"`
}

func (a *Aggregator) itemDTOFromRecord(rec *Record) ItemDTO {
	author := a.resolveAuthorProfile(rec)
	authorMetaId := firstNonEmpty(rec.AuthorMetaId, author.GlobalMetaId, author.MetaId)
	authorAddr := firstNonEmpty(rec.AuthorMvcAddress, author.Address)
	rules := rec.UnlockRules
	if rules.TokenPrices == nil {
		rules.TokenPrices = []TokenPriceRule{}
	}
	if rules.VipMrc721Collections == nil {
		rules.VipMrc721Collections = []VipCollectionRule{}
	}
	return ItemDTO{
		PaybuzzPinId:     rec.SourcePinId,
		PinId:            rec.SourcePinId,
		CurrentPinId:     firstNonEmpty(rec.CurrentPinId, rec.SourcePinId),
		ChainName:        rec.ChainName,
		Title:            rec.Title,
		Preview:          rec.Preview,
		ContentType:      rec.ContentType,
		WordCount:        rec.WordCount,
		Encryption:       rec.Encryption,
		AuthorMetaId:     authorMetaId,
		AuthorMvcAddress: authorAddr,
		AuthorName:       author.Name,
		AuthorAvatar:     author.Avatar,
		Author:           author,
		ShopMetaAppPinId: rec.ShopMetaAppPinId,
		UnlockRules:      rules,
		CreatedAt:        rec.CreatedAt,
		UpdatedAt:        rec.UpdatedAt,
		Confirmed:        rec.Confirmed,
	}
}

// handleInfo serves GET /api/paybuzz/info?pinId=...
func (a *Aggregator) handleInfo(c *gin.Context) {
	if a == nil || a.store == nil {
		api.RespErr(c, codeUnavailable, "paybuzz index unavailable")
		return
	}
	pinId := strings.TrimSpace(c.Query("pinId"))
	if pinId == "" {
		api.RespErr(c, codeInvalidParam, "pinId is required")
		return
	}
	rec, err := a.LookupByPinId(pinId)
	if err != nil {
		api.RespErr(c, codeUnavailable, "failed to load paybuzz item")
		return
	}
	if rec == nil {
		api.RespErr(c, codeNotFound, "paybuzz item not found")
		return
	}
	api.RespSuccess(c, a.itemDTOFromRecord(rec))
}

// handleList serves GET /api/paybuzz/list?author=...&tokenAssetKey=...&vipCollectionPinId=...&cursor=...&size=...
func (a *Aggregator) handleList(c *gin.Context) {
	if a == nil || a.store == nil {
		api.RespErr(c, codeUnavailable, "paybuzz index unavailable")
		return
	}

	size := defaultListSize
	if sizeRaw := strings.TrimSpace(c.Query("size")); sizeRaw != "" {
		parsed, err := strconv.Atoi(sizeRaw)
		if err != nil || parsed <= 0 {
			api.RespErr(c, codeInvalidParam, "invalid parameter: size")
			return
		}
		if parsed > maxListSize {
			parsed = maxListSize
		}
		size = parsed
	}
	offset, ok := parseListCursor(c.Query("cursor"))
	if !ok {
		api.RespErr(c, codeInvalidParam, "invalid parameter: cursor")
		return
	}

	authorFilter := strings.TrimSpace(c.Query("author"))
	tokenFilter := normalizeAssetKey(c.Query("tokenAssetKey"))
	vipFilter := strings.ToLower(strings.TrimSpace(c.Query("vipCollectionPinId")))
	keyword := strings.ToLower(strings.TrimSpace(firstNonEmpty(c.Query("keyword"), c.Query("q"))))

	var tokenAllowed map[string]struct{}
	if tokenFilter != "" {
		tokenAllowed, _ = a.scanSourceSetByPrefix(tokenIndexPrefix(tokenFilter))
	}
	var vipAllowed map[string]struct{}
	if vipFilter != "" {
		vipAllowed, _ = a.scanSourceSetByPrefix(vipIndexPrefix(vipFilter))
	}
	var authorIdentities map[string]struct{}
	if authorFilter != "" {
		authorIdentities = a.expandAuthorIdentities(authorFilter)
	}

	allRecords, err := a.listAllRecordsNewestFirst()
	if err != nil {
		api.RespErr(c, codeUnavailable, "failed to list paybuzz items")
		return
	}

	filtered := make([]ItemDTO, 0, len(allRecords))
	for _, rec := range allRecords {
		if tokenFilter != "" {
			if _, inIdx := tokenAllowed[rec.SourcePinId]; !inIdx || !recordHasToken(rec, tokenFilter) {
				continue
			}
		}
		if vipFilter != "" {
			if _, inIdx := vipAllowed[rec.SourcePinId]; !inIdx || !recordHasVipCollection(rec, vipFilter) {
				continue
			}
		}
		dto := a.itemDTOFromRecord(rec)
		if authorFilter != "" && !dtoMatchesAuthor(rec, &dto, authorIdentities) {
			continue
		}
		if keyword != "" &&
			!strings.Contains(strings.ToLower(dto.Title), keyword) &&
			!strings.Contains(strings.ToLower(dto.Preview), keyword) {
			continue
		}
		filtered = append(filtered, dto)
	}

	total := len(filtered)
	if offset > total {
		offset = total
	}
	end := offset + size
	if end > total {
		end = total
	}
	nextCursor := ""
	if end < total {
		nextCursor = strconv.Itoa(end)
	}

	api.RespSuccess(c, gin.H{
		"list":    filtered[offset:end],
		"total":   total,
		"cursor":  nextCursor,
		"hasMore": end < total,
	})
}

func (a *Aggregator) expandAuthorIdentities(raw string) map[string]struct{} {
	lower := strings.ToLower(strings.TrimSpace(raw))
	out := map[string]struct{}{lower: {}}

	a.mu.RLock()
	lookup := a.profileLookup
	a.mu.RUnlock()
	if lookup == nil {
		return out
	}
	addSnap := func(s *AuthorProfileSnapshot) {
		if s == nil {
			return
		}
		for _, v := range []string{s.GlobalMetaId, s.MetaId, s.Address} {
			if trimmed := strings.ToLower(strings.TrimSpace(v)); trimmed != "" {
				out[trimmed] = struct{}{}
			}
		}
	}
	if s, _ := lookup.LookupByGlobalMetaId(raw); s != nil {
		addSnap(s)
	}
	if s, _ := lookup.LookupByMetaId(raw); s != nil {
		addSnap(s)
	}
	if s, _ := lookup.LookupByAddress(raw); s != nil {
		addSnap(s)
	}
	return out
}

func dtoMatchesAuthor(rec *Record, dto *ItemDTO, allowed map[string]struct{}) bool {
	candidates := []string{
		rec.AuthorMetaId,
		rec.AuthorMvcAddress,
		rec.CreatorGlobalMetaId,
		rec.CreatorMetaId,
		rec.CreatorAddress,
		dto.Author.GlobalMetaId,
		dto.Author.MetaId,
		dto.Author.Address,
	}
	for _, cand := range candidates {
		if lower := strings.ToLower(strings.TrimSpace(cand)); lower != "" {
			if _, ok := allowed[lower]; ok {
				return true
			}
		}
	}
	return false
}

func recordHasToken(rec *Record, assetKey string) bool {
	for _, tok := range rec.UnlockRules.TokenPrices {
		if normalizeAssetKey(tok.AssetKey) == assetKey {
			return true
		}
	}
	return false
}

func recordHasVipCollection(rec *Record, collectionPinId string) bool {
	for _, col := range rec.UnlockRules.VipMrc721Collections {
		if strings.ToLower(strings.TrimSpace(col.CollectionPinId)) == collectionPinId {
			return true
		}
	}
	return false
}

func parseListCursor(raw string) (int, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, true
	}
	if n, err := strconv.Atoi(trimmed); err == nil {
		return n, n >= 0
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(trimmed); err == nil {
		var obj struct {
			Offset int `json:"o"`
		}
		if json.Unmarshal(decoded, &obj) == nil && obj.Offset >= 0 {
			return obj.Offset, true
		}
	}
	return 0, false
}
