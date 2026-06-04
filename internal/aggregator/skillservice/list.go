package skillservice

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// now is a small indirection so tests can pin a deterministic clock if they
// ever need to assert on `aggregatedAt`. The default real-time
// implementation returns the current wall time in milliseconds.
var now = func() int64 { return time.Now().UnixMilli() }

// ListParams is the parsed, normalised filter/sort/paginate input the list
// endpoint accepts. Holding parsing in one place keeps the handler thin and
// lets unit tests exercise the aggregation engine without going through
// HTTP. See docs/specs for the wire-level query parameter meanings.
type ListParams struct {
	Size                 int
	Cursor               string
	Keyword              string
	Currency             string
	ChainName            string
	OutputType           string
	ProviderGlobalMetaId string
	SortBy               string // "rating" (default) | "updated" | "price"
	Order                string // "desc" (default) | "asc"
	IncludeInactive      bool
}

// Default visibility filter constants and tuning knobs.
const (
	defaultListSize = 20
	maxListSize     = 100

	// Smoothed-rating Bayesian prior. Same formula the spec mentions:
	// (avg*count + mean*prior) / (count + prior). With mean=4.0 and
	// prior=5 a brand-new service starts at 4.0 and converges to its
	// real average as more ratings come in.
	smoothMean  = 4.0
	smoothPrior = 5.0
)

// ServiceListItem is the wire shape returned by /api/bot-hub/skill-service/list.
// Fields here are the union of (a) chain-declared service fields from the
// persisted ServiceRecord, (b) provider profile fields resolved in-process
// via the userinfo aggregator, (c) rating aggregates from the rate index,
// and (d) the spec's identity / lifecycle envelope.
//
// JSON tags follow the spec verbatim; reordering them will break the
// frontend contract.
type ServiceListItem struct {
	Id                 string `json:"id"`
	CurrentPinId       string `json:"currentPinId"`
	SourceServicePinId string `json:"sourceServicePinId"`

	ServiceName   string `json:"serviceName"`
	DisplayName   string `json:"displayName"`
	Description   string `json:"description"`
	ServiceIcon   string `json:"serviceIcon"`
	ProviderSkill string `json:"providerSkill"`
	OutputType    string `json:"outputType"`

	Price          string `json:"price"`
	Currency       string `json:"currency"`
	SettlementKind string `json:"settlementKind"`
	PaymentChain   string `json:"paymentChain"`
	MRC20Ticker    any    `json:"mrc20Ticker"`
	MRC20Id        any    `json:"mrc20Id"`
	PaymentAddress string `json:"paymentAddress"`

	ProviderMetaId       string `json:"providerMetaId"`
	ProviderGlobalMetaId string `json:"providerGlobalMetaId"`
	ProviderAddress      string `json:"providerAddress"`
	ProviderName         string `json:"providerName"`
	ProviderAvatar       string `json:"providerAvatar"`
	ProviderAvatarId     string `json:"providerAvatarId,omitempty"`
	ProviderChatPubkey   string `json:"providerChatPubkey"`

	RatingAvg   float64 `json:"ratingAvg"`
	RatingCount int64   `json:"ratingCount"`

	Status    int    `json:"status"`
	Operation string `json:"operation"`
	Disabled  bool   `json:"disabled"`
	ChainName string `json:"chainName"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// ListResult is the full `data` block of the list response.
type ListResult struct {
	List          []ServiceListItem `json:"list"`
	NextCursor    string            `json:"nextCursor"`
	Total         *int64            `json:"total"`
	AggregatedAt  int64             `json:"aggregatedAt"`
	SchemaVersion string            `json:"schemaVersion"`
}

// List runs the full filter → sort → paginate pipeline and returns one
// page of results. It is intentionally a pure function over the persisted
// state so the handler stays a thin shim and tests can exercise it
// directly.
func (a *Aggregator) List(p ListParams) (*ListResult, error) {
	p = normaliseListParams(p)

	// Step 1: gather candidate records. When chainName is set we can
	// scan a single chain prefix; otherwise we fall back to listAll
	// (small catalog in v1; revisit when service counts grow past 10k).
	var records []*ServiceRecord
	var err error
	if p.ChainName != "" {
		records, err = a.listServicesByChain(p.ChainName)
	} else {
		records, err = a.listAllServices()
	}
	if err != nil {
		return nil, err
	}

	// Step 2: visibility + filter. Each predicate is a tiny helper so
	// failing tests pinpoint which filter rejected the record.
	filtered := make([]*ServiceRecord, 0, len(records))
	for _, rec := range records {
		if !p.IncludeInactive && !rec.IsVisibleDefault() {
			continue
		}
		if !matchesCurrencyFilter(rec, p.Currency) {
			continue
		}
		if !matchesOutputTypeFilter(rec, p.OutputType) {
			continue
		}
		// Provider name keyword match needs the resolved snapshot, so we
		// compute it once per record and stash it for step 3 too.
		filtered = append(filtered, rec)
	}

	// Resolve provider + rating in a single pass so step-3 sorting and
	// keyword matching can both consume the same snapshot.
	expanded := make([]expandedRecord, 0, len(filtered))
	for _, rec := range filtered {
		snap := a.ResolveProvider(rec)
		agg, _ := a.LoadRatingAggregate(rec.ChainName, rec.SourceServicePinId)
		exp := expandedRecord{rec: rec, profile: snap, rating: agg}
		if !matchesProviderFilter(exp, p.ProviderGlobalMetaId) {
			continue
		}
		if !matchesKeywordFilter(exp, p.Keyword) {
			continue
		}
		expanded = append(expanded, exp)
	}

	// Step 3: sort.
	sortExpanded(expanded, p.SortBy, p.Order)

	// Step 4: paginate. Cursor encodes the absolute index of the first
	// item on this page. We deliberately keep it opaque to the client
	// per the spec's contract.
	start, err := decodeListCursor(p.Cursor)
	if err != nil {
		return nil, err
	}
	if start < 0 || start >= len(expanded) {
		start = 0
		if p.Cursor != "" {
			// Cursor exists but points outside the current data;
			// surface "no more results" rather than silently
			// returning the first page.
			start = len(expanded)
		}
	}
	end := start + p.Size
	if end > len(expanded) {
		end = len(expanded)
	}
	page := expanded[start:end]
	nextCursor := ""
	if end < len(expanded) {
		nextCursor = encodeListCursor(end)
	}

	// Step 5: marshal to wire items.
	items := make([]ServiceListItem, 0, len(page))
	for _, exp := range page {
		items = append(items, a.toListItem(exp))
	}

	return &ListResult{
		List:          items,
		NextCursor:    nextCursor,
		Total:         nil, // cursor pagination; total is left null per spec
		AggregatedAt:  now(),
		SchemaVersion: "botHubSkillService.v1",
	}, nil
}

// expandedRecord pairs a raw ServiceRecord with the in-process resolved
// provider profile and rating aggregate. It lives just long enough to feed
// the keyword filter, sort, and wire-mapping steps; it is never persisted.
type expandedRecord struct {
	rec     *ServiceRecord
	profile ProfileSnapshot
	rating  *RatingAggregate
}

// --- helpers ----------------------------------------------------------------

func normaliseListParams(p ListParams) ListParams {
	p.Size = clampSize(p.Size)
	p.SortBy = normaliseSortBy(p.SortBy)
	p.Order = normaliseOrder(p.Order)
	p.ChainName = strings.ToLower(strings.TrimSpace(p.ChainName))
	p.Currency = strings.ToUpper(strings.TrimSpace(p.Currency))
	p.OutputType = strings.ToLower(strings.TrimSpace(p.OutputType))
	p.ProviderGlobalMetaId = strings.TrimSpace(p.ProviderGlobalMetaId)
	p.Keyword = strings.TrimSpace(p.Keyword)
	return p
}

func clampSize(size int) int {
	if size <= 0 {
		return defaultListSize
	}
	if size > maxListSize {
		return maxListSize
	}
	return size
}

func normaliseSortBy(sortBy string) string {
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "rating", "updated", "price":
		return strings.ToLower(strings.TrimSpace(sortBy))
	default:
		return "rating"
	}
}

func normaliseOrder(order string) string {
	switch strings.ToLower(strings.TrimSpace(order)) {
	case "asc":
		return "asc"
	default:
		return "desc"
	}
}

func matchesCurrencyFilter(rec *ServiceRecord, want string) bool {
	if want == "" {
		return true
	}
	// MVC is the legacy on-chain currency value; the API surfaces it as
	// SPACE per spec, so the filter accepts both.
	rc := strings.ToUpper(strings.TrimSpace(rec.Currency))
	if want == "SPACE" && rc == "MVC" {
		return true
	}
	return rc == want
}

func matchesOutputTypeFilter(rec *ServiceRecord, want string) bool {
	if want == "" {
		return true
	}
	return strings.EqualFold(rec.OutputType, want)
}

func matchesProviderFilter(exp expandedRecord, want string) bool {
	if want == "" {
		return true
	}
	candidates := []string{
		exp.profile.GlobalMetaId,
		exp.rec.ProviderGlobalMetaId,
		exp.profile.MetaId,
		exp.rec.ProviderMetaId,
		exp.profile.Address,
		exp.rec.ProviderAddress,
	}
	for _, candidate := range candidates {
		if strings.EqualFold(candidate, want) {
			return true
		}
	}
	return false
}

func matchesKeywordFilter(exp expandedRecord, kw string) bool {
	if kw == "" {
		return true
	}
	lc := strings.ToLower(kw)
	haystacks := []string{
		exp.rec.DisplayName,
		exp.rec.ServiceName,
		exp.rec.Description,
		exp.rec.ProviderSkill,
		exp.profile.Name,
	}
	for _, h := range haystacks {
		if h != "" && strings.Contains(strings.ToLower(h), lc) {
			return true
		}
	}
	return false
}

// smoothedScore implements the spec's recommended Bayesian rating prior:
// (avg*count + mean*prior) / (count + prior). Services with no ratings
// converge to mean=4.0 so they don't get pushed to the bottom of the list
// just for being new.
func smoothedScore(rec *ServiceRecord, agg *RatingAggregate) float64 {
	_ = rec
	count := float64(0)
	avg := 0.0
	if agg != nil && agg.Count > 0 {
		count = float64(agg.Count)
		avg = agg.Average()
	}
	return (avg*count + smoothMean*smoothPrior) / (count + smoothPrior)
}

// sortExpanded sorts in place. We always reorder so the wire result is
// deterministic even when several records share the same primary key.
func sortExpanded(items []expandedRecord, sortBy, order string) {
	sort.SliceStable(items, func(i, j int) bool {
		var less bool
		switch sortBy {
		case "updated":
			less = items[i].rec.UpdatedAt < items[j].rec.UpdatedAt
		case "price":
			pi := parsePriceForSort(items[i].rec.Price)
			pj := parsePriceForSort(items[j].rec.Price)
			less = pi < pj
		default: // "rating"
			si := smoothedScore(items[i].rec, items[i].rating)
			sj := smoothedScore(items[j].rec, items[j].rating)
			if si == sj {
				ci := ratingCount(items[i].rating)
				cj := ratingCount(items[j].rating)
				if ci == cj {
					less = items[i].rec.UpdatedAt < items[j].rec.UpdatedAt
				} else {
					less = ci < cj
				}
			} else {
				less = si < sj
			}
		}
		if order == "asc" {
			return less
		}
		return !less
	})
}

func ratingCount(agg *RatingAggregate) int64 {
	if agg == nil {
		return 0
	}
	return agg.Count
}

func parsePriceForSort(price string) float64 {
	if price == "" {
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(price), 64)
	if err != nil {
		return 0
	}
	return f
}

// encodeListCursor / decodeListCursor turn an absolute item index into the
// opaque base64 string the spec requires. We carry the value inside a tiny
// JSON object so we can extend the cursor schema later (e.g. fingerprint
// the query so an in-flight cursor never silently reflects a different
// sort).
type listCursorPayload struct {
	Offset int `json:"o"`
}

func encodeListCursor(offset int) string {
	raw, _ := json.Marshal(listCursorPayload{Offset: offset})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeListCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor")
	}
	var p listCursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return 0, fmt.Errorf("invalid cursor")
	}
	return p.Offset, nil
}

// toListItem turns an expandedRecord into the wire-level ServiceListItem.
// Asset URL resolution happens here so the persisted ServiceRecord stays
// device-agnostic (different deployments can serve different base URLs).
// MRC20 fields are emitted as `null` for non-MRC20 services to match the
// spec's JSON example; we use `any` so the JSON encoder produces null
// rather than empty strings.
func (a *Aggregator) toListItem(exp expandedRecord) ServiceListItem {
	rec := exp.rec
	payment := normalisePaymentMetadata(rec)

	avg := 0.0
	count := int64(0)
	if exp.rating != nil {
		avg = exp.rating.Average()
		count = exp.rating.Count
	}

	return ServiceListItem{
		Id:                 rec.CurrentPinId,
		CurrentPinId:       rec.CurrentPinId,
		SourceServicePinId: rec.SourceServicePinId,

		ServiceName:   rec.ServiceName,
		DisplayName:   rec.DisplayName,
		Description:   rec.Description,
		ServiceIcon:   a.ResolveAsset(rec.ServiceIcon),
		ProviderSkill: rec.ProviderSkill,
		OutputType:    rec.OutputType,

		Price:          rec.Price,
		Currency:       payment.currency,
		SettlementKind: payment.settlementKind,
		PaymentChain:   payment.paymentChain,
		MRC20Ticker:    payment.mrc20Ticker,
		MRC20Id:        payment.mrc20Id,
		PaymentAddress: payment.paymentAddress,

		ProviderMetaId:       firstNonEmpty(exp.profile.MetaId, rec.ProviderMetaId),
		ProviderGlobalMetaId: firstNonEmpty(exp.profile.GlobalMetaId, rec.ProviderGlobalMetaId),
		ProviderAddress:      firstNonEmpty(exp.profile.Address, rec.ProviderAddress),
		ProviderName:         exp.profile.Name,
		ProviderAvatar:       a.ResolveAsset(exp.profile.Avatar),
		ProviderAvatarId:     exp.profile.AvatarId,
		ProviderChatPubkey:   exp.profile.ChatPublicKey,

		RatingAvg:   avg,
		RatingCount: count,

		Status:    rec.Status,
		Operation: rec.Operation,
		Disabled:  rec.Disabled,
		ChainName: rec.ChainName,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,
	}
}
