package ftmarket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultMvcOpenAPIBaseURL is the public MicroVisionChain OpenAPI indexer used
	// when a private Higun instance is not configured.
	DefaultMvcOpenAPIBaseURL = "https://api.microvisionchain.com/open-api-mvc"

	// DefaultMetaidMarketBaseURL is the public metaid.market API host used for
	// BTC MRC-20 floor snapshots and metadata/holder fallback.
	DefaultMetaidMarketBaseURL = "https://api.metaid.market"
)

// UTXOLivenessResult captures the outcome of an upstream UTXO liveness check.
// Checked=false indicates transient upstream unavailability (do not falsely invalidate).
type UTXOLivenessResult struct {
	Checked bool
	Alive   bool
	Reason  string
}

// UTXOVerifier verifies whether a listing's lot.utxoOutpoint is still unspent/available.
type UTXOVerifier interface {
	CheckListingUTXO(ctx context.Context, rec *SellListingRecord) UTXOLivenessResult
}

// UpstreamUTXOVerifier verifies MVC UTXOs against higun (with public MVC OpenAPI fallback)
// and BTC MRC-20 UTXOs against man-p2p.
type UpstreamUTXOVerifier struct {
	HigunBaseURL      string
	MvcOpenAPIBaseURL string
	ManBaseURL        string
	HTTPClient        *http.Client
}

// NewUpstreamUTXOVerifier builds an UpstreamUTXOVerifier for higun and man-p2p.
func NewUpstreamUTXOVerifier(higunBaseURL, manBaseURL string, httpClient *http.Client) *UpstreamUTXOVerifier {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &UpstreamUTXOVerifier{
		HigunBaseURL: strings.TrimRight(strings.TrimSpace(higunBaseURL), "/"),
		ManBaseURL:   strings.TrimRight(strings.TrimSpace(manBaseURL), "/"),
		HTTPClient:   httpClient,
	}
}

// WithMvcOpenAPIBaseURL configures the public MVC OpenAPI fallback endpoint used
// when HigunBaseURL is empty or temporarily unreachable.
func (v *UpstreamUTXOVerifier) WithMvcOpenAPIBaseURL(mvcOpenAPIBaseURL string) *UpstreamUTXOVerifier {
	if v == nil {
		return nil
	}
	v.MvcOpenAPIBaseURL = strings.TrimRight(strings.TrimSpace(mvcOpenAPIBaseURL), "/")
	return v
}

// CheckListingUTXO implements UTXOVerifier.
func (v *UpstreamUTXOVerifier) CheckListingUTXO(ctx context.Context, rec *SellListingRecord) UTXOLivenessResult {
	if v == nil || rec == nil || rec.Asset == nil || rec.Lot == nil || rec.Lot.UtxoOutpoint == "" {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	switch rec.Asset.Chain {
	case "mvc":
		return v.checkMvcUTXO(ctx, rec)
	case "btc":
		return v.checkBtcMrc20UTXO(ctx, rec)
	default:
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
}

func (v *UpstreamUTXOVerifier) checkMvcUTXO(ctx context.Context, rec *SellListingRecord) UTXOLivenessResult {
	outpoint := strings.ToLower(strings.TrimSpace(rec.Lot.UtxoOutpoint))
	if outpoint == "" {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}

	if v.HigunBaseURL != "" {
		res := v.checkMvcUTXOHigun(ctx, rec, outpoint)
		if res.Checked {
			return res
		}
	}

	if v.MvcOpenAPIBaseURL != "" {
		return v.checkMvcUTXOOpenAPI(ctx, rec, outpoint)
	}

	return UTXOLivenessResult{Checked: false, Alive: true}
}

func (v *UpstreamUTXOVerifier) checkMvcUTXOHigun(ctx context.Context, rec *SellListingRecord, outpoint string) UTXOLivenessResult {
	// Contract rail (TokenSell): check contract output spend status via higun /utxo/check.
	if rec.Rails != nil && rec.Rails.MvcTokenSell != nil {
		return v.checkHigunRawUtxo(ctx, outpoint, rec.Overlay)
	}

	// Wallet direct-transfer rail (ME / fiat): verify against higun /ft/utxos and /ft/mempool/utxos.
	codehash, genesis := extractMvcCodehashGenesis(rec.Asset)
	if codehash == "" || genesis == "" || rec.SellerAddress == "" {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}

	confirmedSet, confirmedOK := v.fetchHigunFtUtxos(ctx, rec.SellerAddress, codehash, genesis)
	memIncome, memSpend, mempoolOK := v.fetchHigunFtMempoolUtxos(ctx, rec.SellerAddress, codehash, genesis)

	if mempoolOK {
		if _, spent := memSpend[outpoint]; spent {
			return UTXOLivenessResult{Checked: true, Alive: false, Reason: "utxo_spent"}
		}
		if _, inMem := memIncome[outpoint]; inMem {
			return UTXOLivenessResult{Checked: true, Alive: true}
		}
	}
	if confirmedOK {
		if _, alive := confirmedSet[outpoint]; alive {
			return UTXOLivenessResult{Checked: true, Alive: true}
		}
		// Not in confirmed FT UTXOs for sellerAddress and not in mempool income.
		if mempoolOK || !rec.Overlay {
			return UTXOLivenessResult{Checked: true, Alive: false, Reason: "utxo_spent"}
		}
	}
	return UTXOLivenessResult{Checked: false, Alive: true}
}

func (v *UpstreamUTXOVerifier) checkMvcUTXOOpenAPI(ctx context.Context, rec *SellListingRecord, outpoint string) UTXOLivenessResult {
	codehash, genesis := extractMvcCodehashGenesis(rec.Asset)
	if !hex40Pattern.MatchString(codehash) || !hex40Pattern.MatchString(genesis) {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}

	ownerAddr := strings.TrimSpace(rec.SellerAddress)
	if rec.Rails != nil && rec.Rails.MvcTokenSell != nil {
		// Contract rail outputs live on the contract locking script rather than seller wallet;
		// only query by address when contractAddress is explicitly supplied, otherwise degrade safely.
		ownerAddr = strings.TrimSpace(rec.Rails.MvcTokenSell.ContractAddress)
		if ownerAddr == "" {
			return UTXOLivenessResult{Checked: false, Alive: true}
		}
	}
	if ownerAddr == "" {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}

	alive, exhaustive, ok := v.checkMvcOpenAPIFtOutpoint(ctx, ownerAddr, codehash, genesis, outpoint)
	if !ok {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	if alive {
		return UTXOLivenessResult{Checked: true, Alive: true}
	}
	if exhaustive && !rec.Overlay {
		return UTXOLivenessResult{Checked: true, Alive: false, Reason: "utxo_spent"}
	}
	return UTXOLivenessResult{Checked: false, Alive: true}
}

func (v *UpstreamUTXOVerifier) checkMvcOpenAPIFtOutpoint(
	ctx context.Context,
	address, codehash, genesis, targetOutpoint string,
) (alive bool, exhaustive bool, ok bool) {
	base := strings.TrimRight(strings.TrimSpace(v.MvcOpenAPIBaseURL), "/")
	if base == "" {
		return false, false, false
	}

	const maxPages = 5
	flag := ""
	for page := 0; page < maxPages; page++ {
		u, err := url.Parse(base + "/contract/ft/address/" + url.PathEscape(address) + "/utxo")
		if err != nil {
			return false, false, false
		}
		q := u.Query()
		q.Set("codeHash", codehash)
		q.Set("genesis", genesis)
		if flag != "" {
			q.Set("flag", flag)
		}
		u.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return false, false, false
		}
		resp, err := v.HTTPClient.Do(req)
		if err != nil {
			return false, false, false
		}
		raw, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return false, false, false
		}

		var items []struct {
			Txid    string `json:"txid"`
			TxIndex int64  `json:"txIndex"`
			Flag    string `json:"flag"`
		}
		if err := json.Unmarshal(raw, &items); err != nil {
			return false, false, false
		}
		for _, item := range items {
			if item.Txid == "" {
				continue
			}
			op := fmt.Sprintf("%s:%d", strings.ToLower(strings.TrimSpace(item.Txid)), item.TxIndex)
			if op == targetOutpoint {
				return true, true, true
			}
		}
		if len(items) < 100 {
			return false, true, true
		}
		nextFlag := strings.TrimSpace(items[len(items)-1].Flag)
		if nextFlag == "" || nextFlag == flag {
			return false, true, true
		}
		flag = nextFlag
	}
	// Address had >= maxPages*100 FT UTXOs without matching; avoid false eviction.
	return false, false, true
}

func extractMvcCodehashGenesis(asset *AssetInfo) (string, string) {
	if asset == nil || asset.Codehash == nil || asset.Genesis == nil {
		return "", ""
	}
	ch := strings.ToLower(strings.TrimSpace(*asset.Codehash))
	gen, _ := ResolveMvcGenesisAndSensibleId(ch, *asset.Genesis)
	return ch, gen
}

func (v *UpstreamUTXOVerifier) checkHigunRawUtxo(ctx context.Context, outpoint string, isOverlay bool) UTXOLivenessResult {
	body, _ := json.Marshal(map[string]any{"outPoints": []string{outpoint}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.HigunBaseURL+"/utxo/check", bytes.NewReader(body))
	if err != nil {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := v.HTTPClient.Do(req)
	if err != nil {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	var env struct {
		Code int `json:"code"`
		Data map[string]struct {
			IsExist     bool   `json:"isExist"`
			SpendStatus string `json:"spendStatus"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || (env.Code != 2000 && env.Code != 0) {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	info, ok := env.Data[outpoint]
	if !ok {
		for k, val := range env.Data {
			if strings.EqualFold(k, outpoint) {
				info = val
				ok = true
				break
			}
		}
	}
	if !ok {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	if strings.EqualFold(info.SpendStatus, "spend") {
		return UTXOLivenessResult{Checked: true, Alive: false, Reason: "utxo_spent"}
	}
	if info.IsExist && strings.EqualFold(info.SpendStatus, "unspent") {
		return UTXOLivenessResult{Checked: true, Alive: true}
	}
	if !info.IsExist && !isOverlay {
		return UTXOLivenessResult{Checked: true, Alive: false, Reason: "utxo_spent"}
	}
	return UTXOLivenessResult{Checked: false, Alive: true}
}

func (v *UpstreamUTXOVerifier) fetchHigunFtUtxos(ctx context.Context, address, codehash, genesis string) (map[string]struct{}, bool) {
	u, err := url.Parse(v.HigunBaseURL + "/ft/utxos")
	if err != nil {
		return nil, false
	}
	q := u.Query()
	q.Set("address", address)
	q.Set("codeHash", codehash)
	q.Set("genesis", genesis)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, false
	}
	resp, err := v.HTTPClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}
	var env struct {
		Code int `json:"code"`
		Data struct {
			UTXOs []struct {
				Txid    string `json:"txid"`
				TxIndex int64  `json:"txIndex"`
			} `json:"utxos"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || (env.Code != 0 && env.Code != 2000 && env.Code != 1) {
		return nil, false
	}
	out := make(map[string]struct{}, len(env.Data.UTXOs))
	for _, u := range env.Data.UTXOs {
		if u.Txid != "" {
			out[fmt.Sprintf("%s:%d", strings.ToLower(strings.TrimSpace(u.Txid)), u.TxIndex)] = struct{}{}
		}
	}
	return out, true
}

func (v *UpstreamUTXOVerifier) fetchHigunFtMempoolUtxos(ctx context.Context, address, codehash, genesis string) (map[string]struct{}, map[string]struct{}, bool) {
	u, err := url.Parse(v.HigunBaseURL + "/ft/mempool/utxos")
	if err != nil {
		return nil, nil, false
	}
	q := u.Query()
	q.Set("address", address)
	q.Set("codeHash", codehash)
	q.Set("genesis", genesis)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil, false
	}
	resp, err := v.HTTPClient.Do(req)
	if err != nil {
		return nil, nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, false
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, false
	}
	var env struct {
		Code int `json:"code"`
		Data struct {
			Income []struct {
				Txid    string `json:"txid"`
				TxIndex int64  `json:"txIndex"`
			} `json:"income"`
			Spend []struct {
				Txid    string `json:"txid"`
				TxIndex int64  `json:"txIndex"`
			} `json:"spend"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || (env.Code != 0 && env.Code != 2000 && env.Code != 1) {
		return nil, nil, false
	}
	income := make(map[string]struct{}, len(env.Data.Income))
	for _, u := range env.Data.Income {
		if u.Txid != "" {
			income[fmt.Sprintf("%s:%d", strings.ToLower(strings.TrimSpace(u.Txid)), u.TxIndex)] = struct{}{}
		}
	}
	spend := make(map[string]struct{}, len(env.Data.Spend))
	for _, u := range env.Data.Spend {
		if u.Txid != "" {
			spend[fmt.Sprintf("%s:%d", strings.ToLower(strings.TrimSpace(u.Txid)), u.TxIndex)] = struct{}{}
		}
	}
	return income, spend, true
}

func (v *UpstreamUTXOVerifier) checkBtcMrc20UTXO(ctx context.Context, rec *SellListingRecord) UTXOLivenessResult {
	if v.ManBaseURL == "" {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	outpoint := strings.ToLower(strings.TrimSpace(rec.Lot.UtxoOutpoint))
	parts := strings.SplitN(outpoint, ":", 2)
	if len(parts) != 2 {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	txid, idxStr := parts[0], parts[1]

	u, err := url.Parse(v.ManBaseURL + "/api/mrc20/tx/history")
	if err != nil {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	q := u.Query()
	q.Set("txId", txid)
	q.Set("index", idxStr)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	resp, err := v.HTTPClient.Do(req)
	if err != nil {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	var env struct {
		Code int `json:"code"`
		Data *struct {
			List []struct {
				TxPoint   string `json:"txPoint"`
				Mrc20Id   string `json:"mrc20Id"`
				ToAddress string `json:"toAddress"`
				Status    int    `json:"status"` // 0 = Available, -1 = Spent, 1/2/3 = Pending
				Verify    bool   `json:"verify"`
			} `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	if env.Code == 100 {
		// man-p2p ErrNoDataFound
		if !rec.Overlay {
			return UTXOLivenessResult{Checked: true, Alive: false, Reason: "utxo_unavailable"}
		}
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	if env.Code != 1 && env.Code != 0 {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	if env.Data == nil || len(env.Data.List) == 0 {
		if !rec.Overlay {
			return UTXOLivenessResult{Checked: true, Alive: false, Reason: "utxo_unavailable"}
		}
		return UTXOLivenessResult{Checked: false, Alive: true}
	}

	var matched *struct {
		TxPoint   string `json:"txPoint"`
		Mrc20Id   string `json:"mrc20Id"`
		ToAddress string `json:"toAddress"`
		Status    int    `json:"status"`
		Verify    bool   `json:"verify"`
	}
	for i := range env.Data.List {
		item := &env.Data.List[i]
		if strings.EqualFold(strings.TrimSpace(item.TxPoint), outpoint) {
			matched = item
			break
		}
	}
	if matched == nil {
		matched = &env.Data.List[0]
	}

	if rec.Asset.Mrc20Id != nil && *rec.Asset.Mrc20Id != "" && matched.Mrc20Id != "" {
		if !strings.EqualFold(matched.Mrc20Id, *rec.Asset.Mrc20Id) {
			return UTXOLivenessResult{Checked: true, Alive: false, Reason: "asset_mismatch"}
		}
	}
	if !matched.Verify || matched.Status != 0 {
		return UTXOLivenessResult{Checked: true, Alive: false, Reason: "utxo_spent"}
	}
	if rec.Rails != nil && (rec.Rails.Me != nil || rec.Rails.Fiat != nil) {
		expectedOwner := firstNonEmpty(rec.SellerBtcAddress, rec.SellerAddress)
		if expectedOwner != "" && matched.ToAddress != "" && !sameSellerAddress(matched.ToAddress, expectedOwner) {
			return UTXOLivenessResult{Checked: true, Alive: false, Reason: "owner_mismatch"}
		}
	}
	return UTXOLivenessResult{Checked: true, Alive: true}
}

func (a *Aggregator) checkUTXOLivenessCached(ctx context.Context, rec *SellListingRecord) UTXOLivenessResult {
	if a == nil || rec == nil || rec.Lot == nil || rec.Lot.UtxoOutpoint == "" {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	a.mu.Lock()
	verifier := a.utxoVerifier
	ttl := a.verifyCacheTTL
	a.mu.Unlock()
	return a.checkUTXOLivenessWithVerifier(ctx, rec, verifier, ttl)
}

func (a *Aggregator) checkUTXOLivenessCachedLocked(ctx context.Context, rec *SellListingRecord) UTXOLivenessResult {
	if a == nil || rec == nil || rec.Lot == nil || rec.Lot.UtxoOutpoint == "" {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	return a.checkUTXOLivenessWithVerifier(ctx, rec, a.utxoVerifier, a.verifyCacheTTL)
}

func (a *Aggregator) checkUTXOLivenessWithVerifier(ctx context.Context, rec *SellListingRecord, verifier UTXOVerifier, ttl time.Duration) UTXOLivenessResult {
	if verifier == nil {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	if ttl <= 0 {
		ttl = defaultVerifyCacheTTL
	}

	cacheKey := rec.ListingKey + "|" + rec.Lot.UtxoOutpoint
	now := time.Now()

	a.verifyCacheMu.Lock()
	if entry, ok := a.verifyCache[cacheKey]; ok && now.Sub(entry.checkedAt) < ttl {
		a.verifyCacheMu.Unlock()
		return entry.result
	}
	a.verifyCacheMu.Unlock()

	res := verifier.CheckListingUTXO(ctx, rec)
	if res.Checked {
		a.verifyCacheMu.Lock()
		a.verifyCache[cacheKey] = cachedUTXOCheck{checkedAt: now, result: res}
		a.verifyCacheMu.Unlock()
	}
	return res
}

// VerifyActiveListings verifies UTXO liveness for all currently active listings,
// marking spent/unavailable listings effectiveActive=false and invalidating market cache.
func (a *Aggregator) VerifyActiveListings(ctx context.Context) int {
	if a == nil || a.store == nil {
		return 0
	}
	a.mu.Lock()
	verifier := a.utxoVerifier
	zeroConf := a.zeroConfTrade
	a.mu.Unlock()
	if verifier == nil {
		return 0
	}
	records, err := a.listAllEffectiveListings(zeroConf)
	if err != nil {
		return 0
	}
	return a.refreshListingsLiveness(ctx, records, true)
}

func (a *Aggregator) refreshListingsLiveness(ctx context.Context, records []*SellListingRecord, forceBypassCache bool) int {
	a.mu.Lock()
	verifier := a.utxoVerifier
	a.mu.Unlock()
	if verifier == nil || len(records) == 0 {
		return 0
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var active []*SellListingRecord
	for _, r := range records {
		if r != nil && r.EffectiveActive && r.Lot != nil && r.Lot.UtxoOutpoint != "" {
			active = append(active, r)
		}
	}
	if len(active) == 0 {
		return 0
	}

	const maxWorkers = 8
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	invalidated := 0

	for _, rec := range active {
		rec := rec
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			var res UTXOLivenessResult
			if forceBypassCache {
				res = verifier.CheckListingUTXO(ctx, rec)
				if res.Checked {
					a.verifyCacheMu.Lock()
					a.verifyCache[rec.ListingKey+"|"+rec.Lot.UtxoOutpoint] = cachedUTXOCheck{checkedAt: time.Now(), result: res}
					a.verifyCacheMu.Unlock()
				}
			} else {
				res = a.checkUTXOLivenessCached(ctx, rec)
			}
			if res.Checked && !res.Alive {
				reason := firstNonEmpty(res.Reason, "utxo_spent")
				if a.markListingUTXOSpent(rec.ListingKey, rec.Overlay, reason) {
					mu.Lock()
					invalidated++
					mu.Unlock()
				}
				rec.EffectiveActive = false
				rec.InactiveReason = reason
			}
		}()
	}
	wg.Wait()

	if invalidated > 0 {
		a.invalidateMarketCache()
	}
	return invalidated
}

// UpstreamTokenMetadataProvider queries man-p2p, api.metaid.market, higun, and
// MVC OpenAPI for token metadata, holder counts, and dynamic MVC sensibleId mappings.
type UpstreamTokenMetadataProvider struct {
	HigunBaseURL        string
	MvcOpenAPIBaseURL   string
	ManBaseURL          string
	MetaidMarketBaseURL string
	HTTPClient          *http.Client

	summaryMu        sync.Mutex
	lastSummaryFetch time.Time
}

// NewUpstreamTokenMetadataProvider creates a TokenMetadataProvider backed by higun & man-p2p.
func NewUpstreamTokenMetadataProvider(higunBaseURL, manBaseURL string, httpClient *http.Client) *UpstreamTokenMetadataProvider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 4 * time.Second}
	}
	return &UpstreamTokenMetadataProvider{
		HigunBaseURL: strings.TrimRight(strings.TrimSpace(higunBaseURL), "/"),
		ManBaseURL:   strings.TrimRight(strings.TrimSpace(manBaseURL), "/"),
		HTTPClient:   httpClient,
	}
}

// WithFallbackURLs configures the public MVC OpenAPI and metaid.market fallback
// endpoints used when HigunBaseURL is empty or ManBaseURL lacks MRC-20 tick info.
func (p *UpstreamTokenMetadataProvider) WithFallbackURLs(mvcOpenAPIBaseURL, metaidMarketBaseURL string) *UpstreamTokenMetadataProvider {
	if p == nil {
		return nil
	}
	p.MvcOpenAPIBaseURL = strings.TrimRight(strings.TrimSpace(mvcOpenAPIBaseURL), "/")
	p.MetaidMarketBaseURL = strings.TrimRight(strings.TrimSpace(metaidMarketBaseURL), "/")
	return p
}

// FetchTokenMetadata implements TokenMetadataProvider.
func (p *UpstreamTokenMetadataProvider) FetchTokenMetadata(ctx context.Context, asset AssetInfo) (*TokenChainMetadata, bool) {
	if p == nil {
		return nil, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	switch asset.Chain {
	case "btc":
		if asset.Mrc20Id == nil || strings.TrimSpace(*asset.Mrc20Id) == "" {
			return nil, false
		}
		return p.fetchBtcTokenMetadata(ctx, *asset.Mrc20Id)

	case "mvc":
		if asset.Codehash == nil || asset.Genesis == nil {
			return nil, false
		}
		return p.fetchMvcTokenMetadata(ctx, *asset.Codehash, *asset.Genesis)
	}
	return nil, false
}

func (p *UpstreamTokenMetadataProvider) fetchBtcTokenMetadata(ctx context.Context, mrc20Id string) (*TokenChainMetadata, bool) {
	var merged TokenChainMetadata
	gotAny := false

	if strings.TrimSpace(p.ManBaseURL) != "" {
		if manMeta, ok := p.fetchBtcMetaFromMan(ctx, mrc20Id); ok && manMeta != nil {
			mergeChainMetadata(&merged, manMeta)
			gotAny = true
		}
	}

	needFallback := !gotAny || merged.HolderCount == 0 || merged.Decimals == 0 || merged.Symbol == "" || merged.Icon == ""
	if needFallback && strings.TrimSpace(p.MetaidMarketBaseURL) != "" {
		if mmMeta, _, ok := fetchMrc20TickInfoFromMetaidMarket(ctx, p.HTTPClient, p.MetaidMarketBaseURL, mrc20Id); ok && mmMeta != nil {
			mergeChainMetadata(&merged, mmMeta)
			gotAny = true
		}
	}

	if !gotAny {
		return nil, false
	}
	return &merged, true
}

func (p *UpstreamTokenMetadataProvider) fetchBtcMetaFromMan(ctx context.Context, mrc20Id string) (*TokenChainMetadata, bool) {
	base := strings.TrimRight(strings.TrimSpace(p.ManBaseURL), "/")
	if base == "" {
		return nil, false
	}
	u := base + "/api/mrc20/tick/info/" + url.PathEscape(strings.TrimSpace(mrc20Id))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, false
	}
	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}
	var env struct {
		Code int `json:"code"`
		Data *struct {
			Tick      string `json:"tick"`
			TokenName string `json:"tokenName"`
			Decimals  string `json:"decimals"`
			Holders   uint64 `json:"holders"`
			Metadata  string `json:"metadata"`
			MetaData  string `json:"metaData"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || (env.Code != 1 && env.Code != 0) || env.Data == nil {
		return nil, false
	}
	dec, _ := strconv.Atoi(strings.TrimSpace(env.Data.Decimals))
	icon := extractMrc20Icon(firstNonEmpty(env.Data.Metadata, env.Data.MetaData), "")
	return &TokenChainMetadata{
		HolderCount: int(env.Data.Holders),
		Symbol:      strings.TrimSpace(env.Data.Tick),
		TokenName:   strings.TrimSpace(env.Data.TokenName),
		Decimals:    dec,
		Icon:        icon,
	}, true
}

func (p *UpstreamTokenMetadataProvider) fetchMvcTokenMetadata(ctx context.Context, rawCodehash, rawGenesis string) (*TokenChainMetadata, bool) {
	codehash := strings.ToLower(strings.TrimSpace(rawCodehash))
	genesis, sensibleId := ResolveMvcGenesisAndSensibleId(codehash, rawGenesis)
	if !hex40Pattern.MatchString(genesis) && (hex72Pattern.MatchString(genesis) || hex64Pattern.MatchString(genesis)) {
		if gen40, ok := p.ResolveMvcSensibleId(ctx, codehash, genesis); ok && hex40Pattern.MatchString(gen40) {
			sensibleId = genesis
			genesis = gen40
		}
	}

	var merged TokenChainMetadata
	if sensibleId != "" {
		merged.SensibleId = sensibleId
	}
	gotAny := false

	if strings.TrimSpace(p.HigunBaseURL) != "" && hex40Pattern.MatchString(codehash) && hex40Pattern.MatchString(genesis) {
		if higunMeta, ok := p.fetchMvcMetaFromHigun(ctx, codehash, genesis); ok && higunMeta != nil {
			mergeChainMetadata(&merged, higunMeta)
			gotAny = true
		}
	}

	needOpenAPI := strings.TrimSpace(p.MvcOpenAPIBaseURL) != "" &&
		hex40Pattern.MatchString(codehash) &&
		hex40Pattern.MatchString(genesis) &&
		(!gotAny || merged.HolderCount == 0 || merged.SensibleId == "" || merged.Symbol == "")
	if needOpenAPI {
		if openMeta, ok := p.fetchMvcMetaFromOpenAPI(ctx, codehash, genesis); ok && openMeta != nil {
			mergeChainMetadata(&merged, openMeta)
			gotAny = true
		}
	}

	if !gotAny {
		return nil, false
	}
	return &merged, true
}

func (p *UpstreamTokenMetadataProvider) fetchMvcMetaFromHigun(ctx context.Context, codehash, genesis string) (*TokenChainMetadata, bool) {
	base := strings.TrimRight(strings.TrimSpace(p.HigunBaseURL), "/")
	u, err := url.Parse(base + "/ft/owners")
	if err != nil {
		return nil, false
	}
	q := u.Query()
	q.Set("codeHash", codehash)
	q.Set("genesis", genesis)
	q.Set("cursor", "0")
	q.Set("size", "1")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, false
	}
	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}
	var env struct {
		Code int `json:"code"`
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || (env.Code != 0 && env.Code != 2000 && env.Code != 1) {
		return nil, false
	}
	return &TokenChainMetadata{HolderCount: env.Data.Total}, true
}

func (p *UpstreamTokenMetadataProvider) fetchMvcMetaFromOpenAPI(ctx context.Context, codehash, genesis string) (*TokenChainMetadata, bool) {
	base := strings.TrimRight(strings.TrimSpace(p.MvcOpenAPIBaseURL), "/")
	if base == "" {
		return nil, false
	}

	var out TokenChainMetadata
	gotAny := false

	// 1. /contract/ft/{codeHash}/{genesis}/owners?size=1 returns total holder count and basic FT metadata.
	ownersURL := base + "/contract/ft/" + url.PathEscape(codehash) + "/" + url.PathEscape(genesis) + "/owners?size=1"
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, ownersURL, nil); err == nil {
		if resp, err := p.HTTPClient.Do(req); err == nil {
			raw, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				var owners struct {
					Count   int `json:"count"`
					Records []struct {
						Name       string `json:"name"`
						Symbol     string `json:"symbol"`
						Decimal    int    `json:"decimal"`
						SensibleId string `json:"sensibleId"`
					} `json:"records"`
				}
				if json.Unmarshal(raw, &owners) == nil {
					gotAny = true
					out.HolderCount = owners.Count
					if len(owners.Records) > 0 {
						r0 := owners.Records[0]
						out.Symbol = strings.TrimSpace(r0.Symbol)
						out.TokenName = strings.TrimSpace(r0.Name)
						out.Decimals = r0.Decimal
						if sid := strings.ToLower(strings.TrimSpace(r0.SensibleId)); sid != "" {
							out.SensibleId = sid
							RegisterMvcSensibleMapping(codehash, genesis, sid)
						}
					}
				}
			}
		}
	}

	// 2. /contract/ft/{codeHash}/{genesis}/genesis populates sensibleId and canonical genesis name/symbol.
	if out.SensibleId == "" || out.TokenName == "" || out.Symbol == "" {
		genURL := base + "/contract/ft/" + url.PathEscape(codehash) + "/" + url.PathEscape(genesis) + "/genesis"
		if req, err := http.NewRequestWithContext(ctx, http.MethodGet, genURL, nil); err == nil {
			if resp, err := p.HTTPClient.Do(req); err == nil {
				raw, readErr := io.ReadAll(resp.Body)
				resp.Body.Close()
				if readErr == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
					var genInfo struct {
						Name       string `json:"name"`
						Symbol     string `json:"symbol"`
						Decimal    int    `json:"decimal"`
						SensibleId string `json:"sensibleId"`
					}
					if json.Unmarshal(raw, &genInfo) == nil {
						if genInfo.Symbol != "" || genInfo.Name != "" || genInfo.SensibleId != "" {
							gotAny = true
						}
						if out.Symbol == "" {
							out.Symbol = strings.TrimSpace(genInfo.Symbol)
						}
						if out.TokenName == "" {
							out.TokenName = strings.TrimSpace(genInfo.Name)
						}
						if out.Decimals <= 0 && genInfo.Decimal > 0 {
							out.Decimals = genInfo.Decimal
						}
						if sid := strings.ToLower(strings.TrimSpace(genInfo.SensibleId)); sid != "" {
							out.SensibleId = sid
							RegisterMvcSensibleMapping(codehash, genesis, sid)
						}
					}
				}
			}
		}
	}

	if !gotAny {
		return nil, false
	}
	return &out, true
}

// ResolveMvcSensibleId implements MvcSensibleResolver by consulting the in-memory
// registry first and refreshing from MVC OpenAPI /contract/ft/summary on misses.
func (p *UpstreamTokenMetadataProvider) ResolveMvcSensibleId(ctx context.Context, codehash, sensibleId string) (string, bool) {
	if p == nil {
		return "", false
	}
	ch := strings.ToLower(strings.TrimSpace(codehash))
	sid := strings.ToLower(strings.TrimSpace(sensibleId))
	if !hex40Pattern.MatchString(ch) || (!hex72Pattern.MatchString(sid) && !hex64Pattern.MatchString(sid)) {
		return "", false
	}
	if gen, _ := ResolveMvcGenesisAndSensibleId(ch, sid); hex40Pattern.MatchString(gen) {
		return gen, true
	}

	base := strings.TrimRight(strings.TrimSpace(p.MvcOpenAPIBaseURL), "/")
	if base == "" {
		return "", false
	}

	p.summaryMu.Lock()
	if time.Since(p.lastSummaryFetch) < 5*time.Minute {
		p.summaryMu.Unlock()
		gen, _ := ResolveMvcGenesisAndSensibleId(ch, sid)
		return gen, hex40Pattern.MatchString(gen)
	}
	p.lastSummaryFetch = time.Now()
	p.summaryMu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/contract/ft/summary?cursor=0&size=500", nil)
	if err != nil {
		return "", false
	}
	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return "", false
	}
	raw, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false
	}
	var sum struct {
		Records []struct {
			CodeHash   string `json:"codeHash"`
			Genesis    string `json:"genesis"`
			SensibleId string `json:"sensibleId"`
		} `json:"records"`
	}
	if err := json.Unmarshal(raw, &sum); err != nil {
		return "", false
	}
	for _, rec := range sum.Records {
		RegisterMvcSensibleMapping(rec.CodeHash, rec.Genesis, rec.SensibleId)
	}
	gen, _ := ResolveMvcGenesisAndSensibleId(ch, sid)
	return gen, hex40Pattern.MatchString(gen)
}

func mergeChainMetadata(dst *TokenChainMetadata, src *TokenChainMetadata) {
	if dst == nil || src == nil {
		return
	}
	if src.HolderCount > dst.HolderCount {
		dst.HolderCount = src.HolderCount
	}
	if dst.Symbol == "" && src.Symbol != "" {
		dst.Symbol = src.Symbol
	}
	if dst.TokenName == "" && src.TokenName != "" {
		dst.TokenName = src.TokenName
	}
	if dst.Decimals <= 0 && src.Decimals > 0 {
		dst.Decimals = src.Decimals
	}
	if dst.Icon == "" && src.Icon != "" {
		dst.Icon = src.Icon
	}
	if dst.SensibleId == "" && src.SensibleId != "" {
		dst.SensibleId = src.SensibleId
	}
}

// MetaidMarketFloorProvider optionally fetches BTC MRC-20 floor snapshots from api.metaid.market.
type MetaidMarketFloorProvider struct {
	BaseURL    string
	HTTPClient *http.Client
	mu         sync.Mutex
	cache      map[string]cachedBtcFloor
	ttl        time.Duration
}

type cachedBtcFloor struct {
	fetchedAt time.Time
	floor     *string
	ok        bool
}

// NewMetaidMarketFloorProvider creates a cached BTC floor fetcher for api.metaid.market.
func NewMetaidMarketFloorProvider(baseURL string, httpClient *http.Client) *MetaidMarketFloorProvider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 3 * time.Second}
	}
	return &MetaidMarketFloorProvider{
		BaseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		HTTPClient: httpClient,
		cache:      make(map[string]cachedBtcFloor),
		ttl:        15 * time.Second,
	}
}

// FetchBtcUnitFloorSats implements BtcFloorProvider.
func (p *MetaidMarketFloorProvider) FetchBtcUnitFloorSats(ctx context.Context, mrc20Id string) (*string, bool) {
	if p == nil || p.BaseURL == "" || mrc20Id == "" {
		return nil, false
	}
	key := strings.ToLower(strings.TrimSpace(mrc20Id))
	now := time.Now()
	p.mu.Lock()
	if c, ok := p.cache[key]; ok && now.Sub(c.fetchedAt) < p.ttl {
		p.mu.Unlock()
		return c.floor, c.ok
	}
	p.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	_, floorStr, ok := fetchMrc20TickInfoFromMetaidMarket(ctx, p.HTTPClient, p.BaseURL, key)
	if !ok {
		return nil, false
	}
	if floorStr == "" {
		p.mu.Lock()
		p.cache[key] = cachedBtcFloor{fetchedAt: now, floor: nil, ok: false}
		p.mu.Unlock()
		return nil, false
	}
	p.mu.Lock()
	p.cache[key] = cachedBtcFloor{fetchedAt: now, floor: &floorStr, ok: true}
	p.mu.Unlock()
	return &floorStr, true
}

func metaidMarketTickInfoURL(baseURL, tickId string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	key := strings.ToLower(strings.TrimSpace(tickId))
	if base == "" || key == "" {
		return ""
	}
	q := "tickId=" + url.QueryEscape(key)
	if strings.HasSuffix(base, "/api-market") {
		return base + "/api/v1/common/mrc20/tick/info?" + q
	}
	return base + "/api-market/api/v1/common/mrc20/tick/info?" + q
}

func fetchMrc20TickInfoFromMetaidMarket(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	mrc20Id string,
) (*TokenChainMetadata, string, bool) {
	u := metaidMarketTickInfoURL(baseURL, mrc20Id)
	if u == "" {
		return nil, "", false
	}
	if client == nil {
		client = &http.Client{Timeout: 4 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", false
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", false
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", false
	}
	var env struct {
		Code int `json:"code"`
		Data *struct {
			Tick             string          `json:"tick"`
			TokenName        string          `json:"tokenName"`
			Decimals         json.RawMessage `json:"decimals"`
			Holders          int             `json:"holders"`
			MetaData         string          `json:"metaData"`
			Metadata         string          `json:"metadata"`
			FloorPrice       json.RawMessage `json:"floorPrice"`
			DeployerUserInfo *struct {
				Avatar string `json:"avatar"`
			} `json:"deployerUserInfo"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Code != 0 || env.Data == nil {
		return nil, "", false
	}

	decStr := strings.Trim(strings.TrimSpace(string(env.Data.Decimals)), `"`)
	dec, _ := strconv.Atoi(decStr)
	avatar := ""
	if env.Data.DeployerUserInfo != nil {
		avatar = env.Data.DeployerUserInfo.Avatar
	}
	icon := extractMrc20Icon(firstNonEmpty(env.Data.MetaData, env.Data.Metadata), avatar)
	floorStr, _ := parsePositiveDecimalRaw(env.Data.FloorPrice)

	meta := &TokenChainMetadata{
		HolderCount: env.Data.Holders,
		Symbol:      strings.TrimSpace(env.Data.Tick),
		TokenName:   strings.TrimSpace(env.Data.TokenName),
		Decimals:    dec,
		Icon:        icon,
	}
	return meta, floorStr, true
}

func parsePositiveDecimalRaw(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || strings.EqualFold(s, "null") {
		return "", false
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || r.Sign() <= 0 {
		return "", false
	}
	return formatRat(r, 8), true
}

func extractMrc20Icon(metaJSON string, deployerAvatar string) string {
	if trimmed := strings.TrimSpace(metaJSON); strings.HasPrefix(trimmed, "{") {
		var obj map[string]any
		if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
			for _, k := range []string{"icon", "avatar", "logo", "image"} {
				if v, ok := obj[k].(string); ok && strings.TrimSpace(v) != "" {
					return normalizeContentOrMetafileURI(v)
				}
			}
		}
	}
	if av := strings.TrimSpace(deployerAvatar); av != "" {
		return normalizeContentOrMetafileURI(av)
	}
	return ""
}

func normalizeContentOrMetafileURI(v string) string {
	s := strings.TrimSpace(v)
	if strings.HasPrefix(s, "/content/") {
		pinId := strings.TrimPrefix(s, "/content/")
		if pinId != "" {
			return "metafile://" + pinId
		}
	}
	return s
}
