package ftmarket

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

const defaultBackfillPageSize = 100

// BackfillOptions configures one MANAPI startup replay of /protocols/ft-sell
// and /protocols/ft-trade pins into the ftmarket read model.
type BackfillOptions struct {
	Context  context.Context
	Client   *BackfillClient
	Since    time.Time
	PageSize int
	Stats    *BackfillStats
}

// BackfillStats reports the outcome of one FT market historical replay.
type BackfillStats struct {
	SellFetched  int64
	SellApplied  int64
	SellSkipped  int64
	TradeFetched int64
	TradeApplied int64
	TradeSkipped int64
	Hydrated     int64
	Errors       int64
}

// BackfillClient fetches historical /protocols/ft-sell and /protocols/ft-trade
// pins from a MANAPI-compatible endpoint and hydrates truncated summaries from
// /content/:pinId or /pin/:pinId.
type BackfillClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewBackfillClient constructs a MANAPI client for FT market startup backfill.
func NewBackfillClient(baseURL string, httpClient *http.Client) *BackfillClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &BackfillClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: httpClient,
	}
}

// Backfill replays historical /protocols/ft-sell pins (oldest-first) followed
// by /protocols/ft-trade pins (oldest-first). It is strictly idempotent:
// re-running against already-ingested pins is a no-op because ingestSellPin
// deduplicates by sellPinId / chainOrderNewer and ingestTradePin deduplicates
// by (txId, listingKey).
func (a *Aggregator) Backfill(opts BackfillOptions) error {
	if a == nil || a.store == nil {
		return errors.New("ftmarket aggregator is not initialised")
	}
	if opts.Client == nil {
		return errors.New("ftmarket backfill client is required")
	}
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = defaultBackfillPageSize
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	stats := opts.Stats

	sellPins, sellHydrated, err := opts.Client.fetchPathPins(ctx, PathFtSell, opts.Since, pageSize)
	if err != nil {
		if stats != nil {
			stats.Errors++
		}
		return err
	}
	if stats != nil {
		stats.SellFetched = int64(len(sellPins))
		stats.Hydrated += sellHydrated
	}

	anyChanged := false
	// MANAPI returns newest-first; replay oldest-first so latest-wins overwrites
	// and cancellations settle in chain order.
	for i := len(sellPins) - 1; i >= 0; i-- {
		pin := sellPins[i]
		a.preResolvePinSensibleMapping(pin)
		a.mu.Lock()
		applied, ingestErr := a.ingestSellPin(pin)
		a.mu.Unlock()
		if ingestErr != nil {
			if stats != nil {
				stats.Errors++
			}
			return ingestErr
		}
		if applied {
			anyChanged = true
		}
		if stats != nil {
			if applied {
				stats.SellApplied++
			} else {
				stats.SellSkipped++
			}
		}
	}

	tradePins, tradeHydrated, err := opts.Client.fetchPathPins(ctx, PathFtTrade, opts.Since, pageSize)
	if err != nil {
		if stats != nil {
			stats.Errors++
		}
		return err
	}
	if stats != nil {
		stats.TradeFetched = int64(len(tradePins))
		stats.Hydrated += tradeHydrated
	}

	for i := len(tradePins) - 1; i >= 0; i-- {
		pin := tradePins[i]
		a.preResolvePinSensibleMapping(pin)
		a.mu.Lock()
		applied, ingestErr := a.ingestTradePin(pin)
		a.mu.Unlock()
		if ingestErr != nil {
			if stats != nil {
				stats.Errors++
			}
			return ingestErr
		}
		if applied {
			anyChanged = true
		}
		if stats != nil {
			if applied {
				stats.TradeApplied++
			} else {
				stats.TradeSkipped++
			}
		}
	}

	if anyChanged {
		a.invalidateMarketCache()
	}
	return nil
}

type manapiFtPin struct {
	Id                 string `json:"id"`
	Number             int64  `json:"number"`
	Path               string `json:"path"`
	OriginalPath       string `json:"originalPath"`
	Operation          string `json:"operation"`
	ContentBody        string `json:"contentBody"`
	ContentSummary     string `json:"contentSummary"`
	ContentLength      int64  `json:"contentLength"`
	ContentType        string `json:"contentType"`
	Creator            string `json:"creator"`
	CreateAddress      string `json:"createAddress"`
	CreateMetaId       string `json:"createMetaId"`
	MetaId             string `json:"metaId"`
	MetaIdLower        string `json:"metaid"`
	GlobalMetaId       string `json:"globalMetaId"`
	Address            string `json:"address"`
	ChainName          string `json:"chainName"`
	GenesisTransaction string `json:"genesisTransaction"`
	GenesisHeight      int64  `json:"genesisHeight"`
	Timestamp          int64  `json:"timestamp"`
}

func (p manapiFtPin) toAggregatorPin() *aggregator.PinInscription {
	metaId := strings.TrimSpace(p.MetaId)
	if metaId == "" {
		metaId = strings.TrimSpace(p.MetaIdLower)
	}
	return &aggregator.PinInscription{
		Id:                 strings.TrimSpace(p.Id),
		Number:             p.Number,
		Path:               strings.TrimSpace(p.Path),
		OriginalPath:       strings.TrimSpace(p.OriginalPath),
		Operation:          strings.TrimSpace(p.Operation),
		ContentBody:        decodeMANAPIFtBody(p.ContentBody, p.ContentSummary),
		ContentSummary:     strings.TrimSpace(p.ContentSummary),
		ContentType:        strings.TrimSpace(p.ContentType),
		Creator:            strings.TrimSpace(p.Creator),
		CreateAddress:      strings.TrimSpace(p.CreateAddress),
		CreateMetaId:       strings.TrimSpace(p.CreateMetaId),
		MetaId:             metaId,
		GlobalMetaId:       strings.TrimSpace(p.GlobalMetaId),
		Address:            strings.TrimSpace(p.Address),
		ChainName:          strings.TrimSpace(p.ChainName),
		GenesisTransaction: strings.TrimSpace(p.GenesisTransaction),
		GenesisHeight:      p.GenesisHeight,
		Timestamp:          p.Timestamp,
	}
}

func decodeMANAPIFtBody(body, summary string) []byte {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return []byte(strings.TrimSpace(summary))
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "\"") {
		return []byte(trimmed)
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if decoded, err := enc.DecodeString(trimmed); err == nil && len(decoded) > 0 && json.Valid(decoded) {
			return decoded
		}
	}
	return []byte(trimmed)
}

func needsFtContentHydration(item manapiFtPin, pin *aggregator.PinInscription) bool {
	if pin == nil || pin.Id == "" {
		return false
	}
	if strings.ToLower(strings.TrimSpace(pin.Operation)) != OperationCreate {
		return false
	}
	raw := bytes.TrimSpace(pinPayloadBytes(pin))
	if len(raw) == 0 {
		return true
	}
	if item.ContentLength > 0 && int64(len(raw)) < item.ContentLength {
		return true
	}
	return !json.Valid(raw)
}

func (c *BackfillClient) fetchPathPins(ctx context.Context, path string, since time.Time, pageSize int) ([]*aggregator.PinInscription, int64, error) {
	if c == nil || strings.TrimSpace(c.baseURL) == "" {
		return nil, 0, errors.New("MANAPI base URL is required")
	}
	cursor := ""
	seenCursors := make(map[string]struct{})
	out := make([]*aggregator.PinInscription, 0, pageSize)
	var hydrated int64

	for {
		seenCursors[cursor] = struct{}{}
		items, nextCursor, err := c.listPage(ctx, path, cursor, pageSize)
		if err != nil {
			return nil, hydrated, err
		}
		if len(items) == 0 {
			break
		}

		allOlder := true
		for i := range items {
			if ftBackfillTimestampBefore(items[i].Timestamp, since) {
				continue
			}
			allOlder = false
			pin := items[i].toAggregatorPin()
			if needsFtContentHydration(items[i], pin) {
				if c.hydratePinPayload(ctx, pin) {
					hydrated++
				}
			}
			out = append(out, pin)
		}
		if allOlder {
			break
		}
		if nextCursor == "" || len(items) < pageSize {
			break
		}
		if _, seen := seenCursors[nextCursor]; seen {
			return nil, hydrated, fmt.Errorf("repeated MANAPI cursor %q for path %s", nextCursor, path)
		}
		cursor = nextCursor
	}
	return out, hydrated, nil
}

func (c *BackfillClient) listPage(ctx context.Context, path, cursor string, size int) ([]manapiFtPin, string, error) {
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, "", fmt.Errorf("parse MANAPI base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, "", errors.New("MANAPI base URL requires scheme and host")
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(basePath, "/pin/path/list") {
		parsed.Path = basePath + "/pin/path/list"
	}
	q := parsed.Query()
	q.Set("path", path)
	q.Set("cursor", cursor)
	q.Set("size", strconv.Itoa(size))
	parsed.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("MANAPI %s returned HTTP %d", path, resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	var env struct {
		Code       int           `json:"code"`
		Message    string        `json:"message"`
		List       []manapiFtPin `json:"list"`
		NextCursor string        `json:"nextCursor"`
		Cursor     string        `json:"cursor"`
		Data       struct {
			List       []manapiFtPin `json:"list"`
			NextCursor string        `json:"nextCursor"`
			Cursor     string        `json:"cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, "", fmt.Errorf("decode MANAPI %s response: %w", path, err)
	}
	if env.Code != 0 && env.Code != 1 {
		return nil, "", fmt.Errorf("MANAPI %s failed: code=%d message=%s", path, env.Code, env.Message)
	}
	items := env.Data.List
	if items == nil {
		items = env.List
	}
	nextCursor := firstNonEmpty(env.Data.NextCursor, env.Data.Cursor, env.NextCursor, env.Cursor)
	return items, nextCursor, nil
}

func (c *BackfillClient) hydratePinPayload(ctx context.Context, pin *aggregator.PinInscription) bool {
	if c == nil || pin == nil || pin.Id == "" {
		return false
	}
	if raw, err := c.fetchContentByID(ctx, pin.Id); err == nil && len(bytes.TrimSpace(raw)) > 0 {
		pin.ContentBody = bytes.TrimSpace(raw)
		if json.Valid(pin.ContentBody) {
			return true
		}
	}
	if fullPin, err := c.fetchPinByID(ctx, pin.Id); err == nil && fullPin != nil && len(bytes.TrimSpace(fullPin.ContentBody)) > 0 {
		pin.ContentBody = bytes.TrimSpace(fullPin.ContentBody)
		return json.Valid(pin.ContentBody)
	}
	return false
}

func (c *BackfillClient) fetchContentByID(ctx context.Context, pinId string) ([]byte, error) {
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, err
	}
	basePath := strings.TrimRight(strings.TrimSuffix(parsed.Path, "/pin/path/list"), "/")
	parsed.Path = basePath + "/content/" + url.PathEscape(strings.TrimSpace(pinId))
	parsed.RawQuery = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("MANAPI content HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (c *BackfillClient) fetchPinByID(ctx context.Context, pinId string) (*aggregator.PinInscription, error) {
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, err
	}
	basePath := strings.TrimRight(strings.TrimSuffix(parsed.Path, "/pin/path/list"), "/")
	parsed.Path = basePath + "/pin/" + url.PathEscape(strings.TrimSpace(pinId))
	parsed.RawQuery = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("MANAPI pin HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var env struct {
		Code int          `json:"code"`
		Data *manapiFtPin `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || (env.Code != 0 && env.Code != 1) || env.Data == nil {
		return nil, errors.New("MANAPI pin response empty or invalid")
	}
	return env.Data.toAggregatorPin(), nil
}

func ftBackfillTimestampBefore(timestamp int64, since time.Time) bool {
	if since.IsZero() || timestamp <= 0 {
		return false
	}
	if timestamp > 100000000000 {
		return timestamp < since.UnixMilli()
	}
	return timestamp < since.Unix()
}
