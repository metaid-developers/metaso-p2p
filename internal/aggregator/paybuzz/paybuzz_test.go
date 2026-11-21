package paybuzz_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/paybuzz"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
	"github.com/metaid-developers/metaso-p2p/internal/api"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

func pinIdFor(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:]) + "i0"
}

func setupPaybuzzTestFixture(t *testing.T) (*paybuzz.Aggregator, *userinfo.Aggregator, *metaweb.Aggregator, *storage.PebbleStore, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })
	cacheProvider := cache.New(store)

	uiAgg := &userinfo.Aggregator{}
	if err := uiAgg.Init(store, cacheProvider); err != nil {
		t.Fatalf("init userinfo: %v", err)
	}

	pbAgg := &paybuzz.Aggregator{}
	pbAgg.SetProfileLookup(paybuzz.NewUserInfoLookupAdapter(uiAgg))
	if err := pbAgg.Init(store, cacheProvider); err != nil {
		t.Fatalf("init paybuzz: %v", err)
	}

	mwAgg := &metaweb.Aggregator{}
	mwAgg.SetProfileNamer(metaweb.NewUserInfoProfileNamer(uiAgg))
	mwAgg.SetDocumentSources(pbAgg)
	if err := mwAgg.Init(store, cacheProvider); err != nil {
		t.Fatalf("init metaweb: %v", err)
	}

	router := gin.New()
	router.Use(api.RequestTimingMiddleware())
	apiGroup := router.Group("/api")
	pbAgg.RegisterRoutes(apiGroup)
	mwAgg.RegisterRoutes(apiGroup)

	return pbAgg, uiAgg, mwAgg, store, router
}

func seedUserProfile(t *testing.T, store *storage.PebbleStore, gid, metaId, addr, name, avatar string) {
	t.Helper()
	prof := userinfo.UserProfile{
		GlobalMetaID: gid,
		MetaID:       metaId,
		Address:      addr,
		Name:         name,
		AvatarId:     avatar,
		ChainName:    "mvc",
	}
	raw, err := json.Marshal(prof)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	if err := store.Set("userinfo", []byte("profile:"+metaId), raw); err != nil {
		t.Fatalf("set profile: %v", err)
	}
	if err := store.Set("userinfo", []byte("globalmetaid:"+strings.ToLower(gid)), []byte(metaId)); err != nil {
		t.Fatalf("set globalmetaid index: %v", err)
	}
	if err := store.Set("userinfo", []byte("address:"+addr), []byte(metaId)); err != nil {
		t.Fatalf("set address index: %v", err)
	}
}

func TestPaybuzzCreateModifyFoldingOwnerGuardAndListFilters(t *testing.T) {
	pbAgg, _, _, store, router := setupPaybuzzTestFixture(t)

	author1GID := "idq1author100000000000000000000000000000000"
	author1MetaId := strings.Repeat("a", 64)
	author1Addr := "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"
	seedUserProfile(t, store, author1GID, author1MetaId, author1Addr, "Dr. Alice", "metafile://avatar-alice")

	attackerGID := "idq1attacker0000000000000000000000000000000"
	attackerAddr := "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2"

	assetMetaid := "mrc20:4f92aaaa00000000000000000000000000000000000000000000000000000000i0"
	assetShow := "mvc-ft:a24201:005d49"
	vipCol1 := strings.Repeat("c", 64) + "i0"
	vipCol2 := strings.Repeat("d", 64) + "i0"

	rootPin1 := pinIdFor("paybuzz-root-1")
	createBody1, _ := json.Marshal(map[string]any{
		"title":       "2027 AI Agent On-Chain Microeconomy Deep Dive v1",
		"preview":     "Public 300-character executive summary covering autonomous agent settlement rails and dual-track monetization.",
		"contentType": "text/markdown",
		"wordCount":   6800,
		"encryption": map[string]any{
			"algo":                  "AES-256-GCM",
			"ivHex":                 strings.Repeat("12", 12),
			"authTagHex":            strings.Repeat("34", 16),
			"cipherSha256":          strings.Repeat("56", 32),
			"keySaltHex":            strings.Repeat("78", 32),
			"ciphertextPinOrBase64": "metafile://" + pinIdFor("enc-blob-1") + ".enc",
		},
		"authorMetaId":     author1GID,
		"authorMvcAddress": author1Addr,
		"unlockRules": map[string]any{
			"mePriceMilli": 2000,
			"tokenPrices": []map[string]any{
				{
					"assetKey":       assetMetaid,
					"chain":          "btc",
					"standard":       "mrc20",
					"symbol":         "METAID",
					"decimals":       8,
					"amountBase":     "1000000000",
					"amountDisplay":  "10",
					"receiveAddress": "bc1qxy2kgdygjrsqtzq2n0yrf2493p83kkfjhx0wlh",
				},
			},
			"vipMrc721Collections": []map[string]any{
				{
					"collectionPinId": vipCol1,
					"collectionName":  "Annual Research Pass",
				},
			},
		},
		"createdAt": 1795300000,
	})

	if _, err := pbAgg.HandleBlockPin(&aggregator.PinInscription{
		Id:            rootPin1,
		Path:          paybuzz.PathPayBuzz,
		Operation:     "create",
		GlobalMetaId:  author1GID,
		MetaId:        author1MetaId,
		Address:       author1Addr,
		ChainName:     "mvc",
		GenesisHeight: 100,
		Timestamp:     1795300000,
		ContentBody:   createBody1,
	}); err != nil {
		t.Fatalf("create1 err: %v", err)
	}

	// 1. Forged modify by attacker MUST be rejected; original v1 stays intact.
	forgedModifyPin := pinIdFor("forged-modify-1")
	forgedBody, _ := json.Marshal(map[string]any{
		"title":       "HACKED TITLE",
		"preview":     "Hijacked preview",
		"contentType": "text/markdown",
		"wordCount":   100,
		"encryption": map[string]any{
			"algo":                  "AES-256-GCM",
			"ivHex":                 strings.Repeat("aa", 12),
			"authTagHex":            strings.Repeat("bb", 16),
			"cipherSha256":          strings.Repeat("cc", 32),
			"ciphertextPinOrBase64": "base64:AAAA",
		},
		"unlockRules": map[string]any{
			"mePriceMilli": 1,
		},
	})
	if _, err := pbAgg.HandleBlockPin(&aggregator.PinInscription{
		Id:            forgedModifyPin,
		Path:          paybuzz.PathPayBuzz + "@" + rootPin1,
		Operation:     "modify",
		GlobalMetaId:  attackerGID,
		Address:       attackerAddr,
		ChainName:     "mvc",
		GenesisHeight: 101,
		Timestamp:     1795300100,
		ContentBody:   forgedBody,
	}); err != nil {
		t.Fatalf("forged modify err: %v", err)
	}

	// 2. Malformed modify by true author MUST be skipped; v1 remains active.
	badModifyPin := pinIdFor("bad-author-modify")
	if _, err := pbAgg.HandleBlockPin(&aggregator.PinInscription{
		Id:            badModifyPin,
		Path:          "@" + rootPin1,
		Operation:     "modify",
		GlobalMetaId:  author1GID,
		MetaId:        author1MetaId,
		Address:       author1Addr,
		ChainName:     "mvc",
		GenesisHeight: 102,
		Timestamp:     1795300200,
		ContentBody:   []byte(`{"title":""}`),
	}); err != nil {
		t.Fatalf("bad modify err: %v", err)
	}

	// 3. Valid modify by true author updates title, preview, wordCount, and switches token price to SHOW + vipCol2!
	validModifyPin := pinIdFor("valid-author-modify-v2")
	modifyBodyV2, _ := json.Marshal(map[string]any{
		"title":       "2027 AI Agent On-Chain Microeconomy Deep Dive v2 (Updated)",
		"preview":     "Updated 300-character preview introducing zero-cost MRC-721 VIP pass verification and MVC FT settlement.",
		"contentType": "text/markdown",
		"wordCount":   7400,
		"encryption": map[string]any{
			"algo":                  "AES-256-GCM",
			"ivHex":                 strings.Repeat("ab", 12),
			"authTagHex":            strings.Repeat("cd", 16),
			"cipherSha256":          strings.Repeat("ef", 32),
			"keySaltHex":            strings.Repeat("99", 32),
			"ciphertextPinOrBase64": "metafile://" + pinIdFor("enc-blob-2") + ".enc",
		},
		"authorMetaId":     author1GID,
		"authorMvcAddress": author1Addr,
		"unlockRules": map[string]any{
			"mePriceMilli": 3000,
			"tokenPrices": []map[string]any{
				{
					"assetKey":       assetShow,
					"chain":          "mvc",
					"standard":       "metacontract-ft",
					"symbol":         "SHOW",
					"decimals":       8,
					"amountBase":     "5000000000",
					"amountDisplay":  "50",
					"receiveAddress": author1Addr,
				},
			},
			"vipMrc721Collections": []map[string]any{
				{
					"collectionPinId": vipCol2,
					"collectionName":  "Platinum Column Pass",
				},
			},
		},
	})
	if _, err := pbAgg.HandleBlockPin(&aggregator.PinInscription{
		Id:            validModifyPin,
		Path:          "@" + rootPin1,
		Operation:     "modify",
		GlobalMetaId:  author1GID,
		MetaId:        author1MetaId,
		Address:       author1Addr,
		ChainName:     "mvc",
		GenesisHeight: 103,
		Timestamp:     1795300300,
		ContentBody:   modifyBodyV2,
	}); err != nil {
		t.Fatalf("valid modify err: %v", err)
	}

	// Verify GET /api/paybuzz/info works by BOTH sourcePinId and currentPinId, with profile enrichment!
	for _, queryPin := range []string{rootPin1, validModifyPin} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/paybuzz/info?pinId="+queryPin, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("info status %d", rec.Code)
		}
		var env struct {
			Code           int             `json:"code"`
			Message        string          `json:"message"`
			ProcessingTime *int64          `json:"processingTime"`
			Data           paybuzz.ItemDTO `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode info: %v", err)
		}
		if env.Code != 0 || env.Message != "" || env.ProcessingTime == nil {
			t.Fatalf("unexpected envelope: %+v", env)
		}
		if env.Data.PaybuzzPinId != rootPin1 || env.Data.CurrentPinId != validModifyPin {
			t.Fatalf("unexpected version fold ids: %+v", env.Data)
		}
		if env.Data.Title != "2027 AI Agent On-Chain Microeconomy Deep Dive v2 (Updated)" || env.Data.WordCount != 7400 {
			t.Fatalf("expected v2 folded content, got %+v", env.Data)
		}
		if env.Data.AuthorName != "Dr. Alice" || env.Data.Author.Avatar != "metafile://avatar-alice" {
			t.Fatalf("expected author profile completion, got %+v", env.Data.Author)
		}
		if env.Data.Encryption.KeySaltHex != strings.Repeat("99", 32) {
			t.Fatalf("expected keySaltHex preserved, got %q", env.Data.Encryption.KeySaltHex)
		}
	}

	// Forged modify pin must NOT resolve to rootPin1!
	rec404 := httptest.NewRecorder()
	router.ServeHTTP(rec404, httptest.NewRequest(http.MethodGet, "/api/paybuzz/info?pinId="+forgedModifyPin, nil))
	var env404 struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(rec404.Body.Bytes(), &env404)
	if env404.Code != 40400 {
		t.Fatalf("expected 40400 for unmapped forged modify pin, got %d", env404.Code)
	}

	// Seed a second article by author2 supporting METAID + vipCol1 to test list filters & pagination.
	rootPin2 := pinIdFor("paybuzz-root-2")
	author2GID := "idq1author200000000000000000000000000000000"
	author2Addr := "1AuthorTwoMvcAddress11111111111111"
	createBody2, _ := json.Marshal(map[string]any{
		"title":       "Zero-Knowledge Rollup Engineering Notes",
		"preview":     "Concise guide to recursive proof aggregation and verifier circuit compression.",
		"contentType": "text/markdown",
		"wordCount":   3200,
		"encryption": map[string]any{
			"algo":                  "AES-256-GCM",
			"ivHex":                 strings.Repeat("21", 12),
			"authTagHex":            strings.Repeat("43", 16),
			"cipherSha256":          strings.Repeat("65", 32),
			"ciphertextPinOrBase64": "base64:VEVTVF9DSVBIRVJURVhU",
		},
		"authorMetaId":     author2GID,
		"authorMvcAddress": author2Addr,
		"unlockRules": map[string]any{
			"tokenPrices": []map[string]any{
				{
					"assetKey":       assetMetaid,
					"chain":          "btc",
					"standard":       "mrc20",
					"symbol":         "METAID",
					"decimals":       8,
					"amountBase":     "800000000",
					"amountDisplay":  "8",
					"receiveAddress": "bc1qxy2kgdygjrsqtzq2n0yrf2493p83kkfjhx0wlh",
				},
			},
			"vipMrc721Collections": []map[string]any{
				{
					"collectionPinId": vipCol1,
					"collectionName":  "Annual Research Pass",
				},
			},
		},
		"createdAt": 1795300500,
	})
	_, _ = pbAgg.HandleBlockPin(&aggregator.PinInscription{
		Id:            rootPin2,
		Path:          paybuzz.PathPayBuzz,
		Operation:     "create",
		GlobalMetaId:  author2GID,
		Address:       author2Addr,
		ChainName:     "mvc",
		GenesisHeight: 105,
		Timestamp:     1795300500,
		ContentBody:   createBody2,
	})

	queryList := func(query string) ([]paybuzz.ItemDTO, int, string) {
		r := httptest.NewRecorder()
		router.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/paybuzz/list?"+query, nil))
		var resp struct {
			Code int `json:"code"`
			Data struct {
				List   []paybuzz.ItemDTO `json:"list"`
				Total  int               `json:"total"`
				Cursor string            `json:"cursor"`
			} `json:"data"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &resp); err != nil || resp.Code != 0 {
			t.Fatalf("list failed (%s): err=%v code=%d raw=%s", query, err, resp.Code, r.Body.String())
		}
		return resp.Data.List, resp.Data.Total, resp.Data.Cursor
	}

	// Old token (assetMetaid) was replaced on rootPin1 by v2 modify -> must ONLY return rootPin2!
	listMetaid, totalMetaid, _ := queryList("tokenAssetKey=" + url.QueryEscape(assetMetaid))
	if totalMetaid != 1 || listMetaid[0].PaybuzzPinId != rootPin2 {
		t.Fatalf("expected stale token index cleaned on modify (only rootPin2 left), got total=%d list=%+v", totalMetaid, listMetaid)
	}

	// New token (assetShow) -> returns rootPin1!
	listShow, totalShow, _ := queryList("tokenAssetKey=" + url.QueryEscape(assetShow))
	if totalShow != 1 || listShow[0].PaybuzzPinId != rootPin1 {
		t.Fatalf("expected rootPin1 for assetShow, got total=%d list=%+v", totalShow, listShow)
	}

	// Filter by vipCollectionPinId (vipCol2 -> rootPin1, vipCol1 -> rootPin2)
	listVip2, totalVip2, _ := queryList("vipCollectionPinId=" + url.QueryEscape(vipCol2))
	if totalVip2 != 1 || listVip2[0].PaybuzzPinId != rootPin1 {
		t.Fatalf("expected rootPin1 for vipCol2, got %+v", listVip2)
	}
	listVip1, totalVip1, _ := queryList("vipCollectionPinId=" + url.QueryEscape(vipCol1))
	if totalVip1 != 1 || listVip1[0].PaybuzzPinId != rootPin2 {
		t.Fatalf("expected rootPin2 for vipCol1, got %+v", listVip1)
	}

	// Filter by author (both MVC address and globalMetaId resolve via userinfo)
	listAuthAddr, totalAuthAddr, _ := queryList("author=" + url.QueryEscape(author1Addr))
	if totalAuthAddr != 1 || listAuthAddr[0].PaybuzzPinId != rootPin1 {
		t.Fatalf("expected rootPin1 by author address, got %+v", listAuthAddr)
	}

	// Pagination: size=1 -> page 1 returns newest (rootPin2) + cursor="1", page 2 returns rootPin1 + cursor=""
	p1, totalAll, cur1 := queryList("size=1")
	if totalAll != 2 || len(p1) != 1 || p1[0].PaybuzzPinId != rootPin2 || cur1 != "1" {
		t.Fatalf("unexpected page 1: total=%d p1=%+v cur=%q", totalAll, p1, cur1)
	}
	p2, _, cur2 := queryList("size=1&cursor=" + cur1)
	if len(p2) != 1 || p2[0].PaybuzzPinId != rootPin1 || cur2 != "" {
		t.Fatalf("unexpected page 2: p2=%+v cur=%q", p2, cur2)
	}
}

func TestPaybuzzMetawebUnifiedSearchHitsTitleAnd300CharPreviewOnly(t *testing.T) {
	pbAgg, _, _, store, router := setupPaybuzzTestFixture(t)

	authorGID := "idq1researcher0000000000000000000000000000"
	authorMetaId := strings.Repeat("b", 64)
	authorAddr := "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"
	seedUserProfile(t, store, authorGID, authorMetaId, authorAddr, "ResearchBot", "metafile://research-avatar")

	// Construct a 300-rune preview where a unique keyword sits near rune 285 (within the 300-rune window).
	padding := strings.Repeat("链上商业经济研报导读", 26) // 260 runes
	preview300 := padding + "量子抗性跨链结算终局分析"           // < 300 runes

	pinId := pinIdFor("search-paybuzz-1")
	secretCipherKeyword := "TOP_SECRET_CIPHERTEXT_TOKEN_XYZ987"
	body, _ := json.Marshal(map[string]any{
		"title":       "Phase 1B Sovereign Content Flywheel Blueprint",
		"preview":     preview300,
		"contentType": "text/markdown",
		"wordCount":   5200,
		"encryption": map[string]any{
			"algo":                  "AES-256-GCM",
			"ivHex":                 strings.Repeat("11", 12),
			"authTagHex":            strings.Repeat("22", 16),
			"cipherSha256":          strings.Repeat("33", 32),
			"ciphertextPinOrBase64": "base64:" + secretCipherKeyword,
		},
		"authorMetaId":     authorGID,
		"authorMvcAddress": authorAddr,
		"unlockRules": map[string]any{
			"mePriceMilli": 1500,
		},
		"createdAt": 1795301000,
	})
	if _, err := pbAgg.HandleMempoolPin(&aggregator.PinInscription{
		Id:           pinId,
		Path:         paybuzz.PathPayBuzz,
		Operation:    "create",
		GlobalMetaId: authorGID,
		MetaId:       authorMetaId,
		Address:      authorAddr,
		ChainName:    "mvc",
		Timestamp:    1795301000,
		ContentBody:  body,
	}); err != nil {
		t.Fatalf("mempool ingest: %v", err)
	}

	searchMetaweb := func(q string) []struct {
		Protocol     string `json:"protocol"`
		PinId        string `json:"pinId"`
		CurrentPinId string `json:"currentPinId"`
		Title        string `json:"title"`
		Summary      string `json:"summary"`
		Publisher    struct {
			Name string `json:"name"`
		} `json:"publisher"`
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/metaweb/search?protocols=paybuzz&q="+url.QueryEscape(q), nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("search status %d", rec.Code)
		}
		var resp struct {
			Code int `json:"code"`
			Data struct {
				Items []struct {
					Protocol     string `json:"protocol"`
					PinId        string `json:"pinId"`
					CurrentPinId string `json:"currentPinId"`
					Title        string `json:"title"`
					Summary      string `json:"summary"`
					Publisher    struct {
						Name string `json:"name"`
					} `json:"publisher"`
				} `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Code != 0 {
			t.Fatalf("decode metaweb search (%s): err=%v code=%d raw=%s", q, err, resp.Code, rec.Body.String())
		}
		return resp.Data.Items
	}

	// 1. Hit by plaintext title
	byTitle := searchMetaweb("Sovereign Content Flywheel")
	if len(byTitle) != 1 || byTitle[0].Protocol != "paybuzz" || byTitle[0].PinId != pinId || byTitle[0].Publisher.Name != "ResearchBot" {
		t.Fatalf("expected metaweb search hit on title, got %+v", byTitle)
	}

	// 2. Hit by tail of 300-rune plaintext preview (>200 chars, <=300 chars!)
	byPreview := searchMetaweb("量子抗性跨链结算终局")
	if len(byPreview) != 1 || byPreview[0].PinId != pinId || !strings.Contains(byPreview[0].Summary, "量子抗性跨链结算终局分析") {
		t.Fatalf("expected metaweb search hit on 300-rune preview tail, got %+v", byPreview)
	}

	// 3. Ciphertext field must NEVER be indexed or searchable
	byCipher := searchMetaweb(secretCipherKeyword)
	if len(byCipher) != 0 {
		t.Fatalf("ciphertext must never be indexed in search_metaweb, got %+v", byCipher)
	}
}
