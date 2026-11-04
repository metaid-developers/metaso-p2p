package mrc721nft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// UpstreamClient fetches the manapi-compatible MRC-721 read views used to
// bootstrap the native aggregate: collection pageList, per-collection item
// pageList and per-item transfer history. All three endpoints paginate with
// an integer offset cursor (params cursor + size, default 20 / max 200) and
// answer {code:1,message:"ok",data:{list:[...],total:N}}.
type UpstreamClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewUpstreamClient builds an upstream client; a nil httpClient falls back
// to http.DefaultClient.
func NewUpstreamClient(baseURL string, httpClient *http.Client) *UpstreamClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &UpstreamClient{baseURL: strings.TrimSpace(baseURL), httpClient: httpClient}
}

// upstreamCollection mirrors the upstream collection DTO. Go's decoder
// matches JSON keys case-insensitively, so the duplicate lowercase/camelCase
// spellings (collectionname/collectionName, pinid, totalnum, createtime,
// totalsupply, royaltyrate, modify_history) all land on the same field; the
// pair values are identical upstream.
type upstreamCollection struct {
	CollectionName   string   `json:"collectionName"`
	Name             string   `json:"name"`
	TotalSupply      int64    `json:"totalSupply"`
	RoyaltyRate      int      `json:"royaltyRate"`
	Desc             string   `json:"desc"`
	Website          string   `json:"website"`
	Cover            string   `json:"cover"`
	Metadata         string   `json:"metadata"`
	PinId            string   `json:"pinid"`
	Address          string   `json:"address"`
	CreateAddress    string   `json:"createAddress"`
	MetaId           string   `json:"metaid"`
	CreateMetaId     string   `json:"createMetaId"`
	CreateTime       int64    `json:"createTime"`
	TotalNum         int64    `json:"totalnum"`
	ModifyHistory    []string `json:"modifyHistory"`
	GenesisChainName string   `json:"genesisChainName"`
	GenesisHeight    int64    `json:"genesisHeight"`
	GenesisTx        string   `json:"genesisTx"`
}

// upstreamItem mirrors the upstream item DTO (duplicate spellings decoded as
// above: metaData/metadata, contentString/contentSummary, txOutput/output,
// txValue/outputValue, itemPinNumber/number, address/ownerAddress,
// metaid/ownerMetaId, chainName/genesisChainName).
type upstreamItem struct {
	CollectionPinId  string `json:"collectionPinId"`
	CollectionName   string `json:"collectionName"`
	ItemPinId        string `json:"itemPinId"`
	Number           int64  `json:"number"`
	Name             string `json:"name"`
	Desc             string `json:"desc"`
	Cover            string `json:"cover"`
	Metadata         string `json:"metadata"`
	OwnerAddress     string `json:"ownerAddress"`
	OwnerMetaId      string `json:"ownerMetaId"`
	CreatorMinted    bool   `json:"creatorMinted"`
	ContentType      string `json:"contentType"`
	ContentSummary   string `json:"contentSummary"`
	Output           string `json:"output"`
	OutputValue      int64  `json:"outputValue"`
	Burned           bool   `json:"burned"`
	TransferCount    int64  `json:"transferCount"`
	GenesisChainName string `json:"genesisChainName"`
	GenesisHeight    int64  `json:"genesisHeight"`
	GenesisTx        string `json:"genesisTx"`
	CreateTime       int64  `json:"createTime"`
}

// upstreamTransfer mirrors one item/transfers history row.
type upstreamTransfer struct {
	ItemPinId   string `json:"itemPinId"`
	FromAddress string `json:"fromAddress"`
	ToAddress   string `json:"toAddress"`
	ChainName   string `json:"chainName"`
	Height      int64  `json:"height"`
	TxId        string `json:"txId"`
	Time        int64  `json:"time"`
}

// fetchPage GETs one offset page from a manapi-compatible endpoint and
// decodes the tolerant envelope into the typed rows.
func (c *UpstreamClient) fetchPage(ctx context.Context, endpoint string, query url.Values, rows any) (total int64, err error) {
	if c == nil || strings.TrimSpace(c.baseURL) == "" {
		return 0, errors.New("mrc721nft upstream base URL is required")
	}
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return 0, fmt.Errorf("parse mrc721nft upstream base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return 0, errors.New("mrc721nft upstream base URL requires scheme and host")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + endpoint
	parsed.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("create mrc721nft upstream request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("%s returned HTTP %d", endpoint, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return 0, fmt.Errorf("read %s response: %w", endpoint, err)
	}
	return decodePage(raw, endpoint, rows)
}

// decodePage accepts the manapi envelope shapes seen in the wild
// ({code,data:{list,total}}, the flat {code,list,total}, and
// ErrNoDataFound's {code:100,data:[]} where data is a bare array); code 0
// and 1 both mean success, code 100 means an empty page.
func decodePage(raw []byte, endpoint string, rows any) (total int64, err error) {
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Total   int64           `json:"total"`
		List    json.RawMessage `json:"list"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return 0, fmt.Errorf("decode %s response: %w", endpoint, err)
	}
	switch envelope.Code {
	case 0, 1:
		// success
	case 100:
		return 0, nil // no data found: empty page
	default:
		return 0, fmt.Errorf("%s failed: code=%d message=%s", endpoint, envelope.Code, envelope.Message)
	}
	list := envelope.List
	total = envelope.Total
	if d := envelope.Data; len(d) > 0 && d[0] == '{' {
		var inner struct {
			Total int64           `json:"total"`
			List  json.RawMessage `json:"list"`
		}
		if err := json.Unmarshal(d, &inner); err == nil {
			if len(inner.List) > 0 {
				list = inner.List
			}
			if inner.Total > 0 {
				total = inner.Total
			}
		}
	} else if len(d) > 0 && d[0] == '[' {
		// data is a bare array: treat it as the list itself.
		list = d
	}
	if len(list) == 0 {
		return total, nil
	}
	if err := json.Unmarshal(list, rows); err != nil {
		return total, fmt.Errorf("decode %s rows: %w", endpoint, err)
	}
	return total, nil
}

// ListCollections pages the upstream collection registry.
func (c *UpstreamClient) ListCollections(ctx context.Context, offset int64, size int) ([]upstreamCollection, int64, error) {
	q := url.Values{}
	q.Set("cursor", strconv.FormatInt(offset, 10))
	q.Set("size", strconv.Itoa(size))
	var rows []upstreamCollection
	total, err := c.fetchPage(ctx, "/api/mrc721/collection/pageList", q, &rows)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ListCollectionItems pages one collection's items. collectionPinId uses the
// upstream pinid= parameter semantics (collectionid).
func (c *UpstreamClient) ListCollectionItems(ctx context.Context, collectionPinId string, offset int64, size int) ([]upstreamItem, int64, error) {
	q := url.Values{}
	q.Set("pinid", collectionPinId)
	q.Set("cursor", strconv.FormatInt(offset, 10))
	q.Set("size", strconv.Itoa(size))
	var rows []upstreamItem
	total, err := c.fetchPage(ctx, "/api/mrc721/collection/items/pageList", q, &rows)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ListItemTransfers pages one item's confirmed transfer history.
func (c *UpstreamClient) ListItemTransfers(ctx context.Context, itemPinId string, offset int64, size int) ([]upstreamTransfer, int64, error) {
	q := url.Values{}
	q.Set("pinId", itemPinId)
	q.Set("cursor", strconv.FormatInt(offset, 10))
	q.Set("size", strconv.Itoa(size))
	var rows []upstreamTransfer
	total, err := c.fetchPage(ctx, "/api/mrc721/item/transfers", q, &rows)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
