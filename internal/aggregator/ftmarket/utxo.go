package ftmarket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
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

// UpstreamUTXOVerifier verifies MVC UTXOs against higun and BTC MRC-20 UTXOs against man-p2p.
type UpstreamUTXOVerifier struct {
	HigunBaseURL string
	ManBaseURL   string
	HTTPClient   *http.Client
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
	if v.HigunBaseURL == "" {
		return UTXOLivenessResult{Checked: false, Alive: true}
	}
	outpoint := strings.ToLower(strings.TrimSpace(rec.Lot.UtxoOutpoint))

	// Contract rail (TokenSell): check contract output spend status via higun /utxo/check.
	if rec.Rails != nil && rec.Rails.MvcTokenSell != nil {
		res := v.checkHigunRawUtxo(ctx, outpoint, rec.Overlay)
		return res
	}

	// Wallet direct-transfer rail (ME / fiat): verify against higun /ft/utxos and /ft/mempool/utxos.
	codehash := ""
	genesis := ""
	if rec.Asset.Codehash != nil {
		codehash = *rec.Asset.Codehash
	}
	if rec.Asset.Genesis != nil {
		genesis = *rec.Asset.Genesis
	}
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

// UpstreamTokenMetadataProvider queries man-p2p and higun for token metadata and holder counts.
type UpstreamTokenMetadataProvider struct {
	HigunBaseURL string
	ManBaseURL   string
	HTTPClient   *http.Client
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
		if p.ManBaseURL == "" || asset.Mrc20Id == nil || *asset.Mrc20Id == "" {
			return nil, false
		}
		u := p.ManBaseURL + "/api/mrc20/tick/info/" + url.PathEscape(*asset.Mrc20Id)
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
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &env); err != nil || (env.Code != 1 && env.Code != 0) || env.Data == nil {
			return nil, false
		}
		dec, _ := strconv.Atoi(strings.TrimSpace(env.Data.Decimals))
		return &TokenChainMetadata{
			HolderCount: int(env.Data.Holders),
			Symbol:      strings.TrimSpace(env.Data.Tick),
			TokenName:   strings.TrimSpace(env.Data.TokenName),
			Decimals:    dec,
		}, true

	case "mvc":
		if p.HigunBaseURL == "" || asset.Codehash == nil || asset.Genesis == nil {
			return nil, false
		}
		u, err := url.Parse(p.HigunBaseURL + "/ft/owners")
		if err != nil {
			return nil, false
		}
		q := u.Query()
		q.Set("codeHash", *asset.Codehash)
		q.Set("genesis", *asset.Genesis)
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
	return nil, false
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
	u := p.BaseURL + "/api/v1/mrc20/tick/info?tickId=" + url.QueryEscape(key)
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
			FloorPrice json.RawMessage `json:"floorPrice"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Data == nil || len(env.Data.FloorPrice) == 0 {
		return nil, false
	}
	floorStr := strings.Trim(strings.TrimSpace(string(env.Data.FloorPrice)), `"`)
	if floorStr == "" || floorStr == "null" || floorStr == "0" {
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
