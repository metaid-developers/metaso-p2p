package publishedcontent

import (
	"strconv"
	"strings"
)

// MetaAppItem is the normalized wire model for /api/metaapp/* responses.
// On-chain payloads use several field-name conventions, so extraction is
// tolerant: see docs/specs/2026-07-26-metaapp-query-api.md for the key
// priority table.
type MetaAppItem struct {
	PinID       string `json:"pinId"`
	SourcePinID string `json:"sourcePinId"`
	ChainName   string `json:"chainName"`

	Title      string   `json:"title"`
	AppName    string   `json:"appName"`
	Intro      string   `json:"intro"`
	Tags       []string `json:"tags"`
	Icon       string   `json:"icon"`
	CoverImg   string   `json:"coverImg"`
	Runtime    string   `json:"runtime"`
	Version    string   `json:"version"`
	Content    string   `json:"content"`
	IndexFile  string   `json:"indexFile"`
	ForkedFrom string   `json:"forkedFrom"`
	Disabled   bool     `json:"disabled"`

	PublisherGlobalMetaId string `json:"publisherGlobalMetaId"`
	PublisherMetaId       string `json:"publisherMetaId"`
	PublisherAddress      string `json:"publisherAddress"`

	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`
}

// MetaAppDetail extends the list item with the raw payload for clients that
// need fields outside the normalized set.
type MetaAppDetail struct {
	MetaAppItem
	Prompt  string         `json:"prompt"`
	Payload map[string]any `json:"payload,omitempty"`
}

type MetaAppListParams struct {
	Keyword         string
	Tag             string
	ChainName       string
	Runtime         string
	Publisher       string
	Since           int64
	Until           int64
	IncludeDisabled bool
	Size            int
	Cursor          string
}

type MetaAppListResult struct {
	Items      []MetaAppItem `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
	HasMore    bool          `json:"hasMore"`
}

const (
	defaultMetaAppListSize = 20
	maxMetaAppListSize     = 100
)

func metaAppItemFromRecord(rec *Record) MetaAppItem {
	payload := rec.PayloadJSON
	tags := payloadStringSlice(payload, "tags")
	indexFile := payloadString(payload, "indexFile")
	if indexFile == "" {
		indexFile = "index.html"
	}
	return MetaAppItem{
		PinID:       rec.CurrentPinId,
		SourcePinID: rec.SourcePinId,
		ChainName:   rec.ChainName,

		Title:      payloadString(payload, "title", "name", "displayName"),
		AppName:    payloadString(payload, "appName", "appname"),
		Intro:      payloadString(payload, "intro", "description", "summary"),
		Tags:       tags,
		Icon:       payloadString(payload, "icon"),
		CoverImg:   payloadString(payload, "coverImg"),
		Runtime:    payloadString(payload, "runtime"),
		Version:    payloadString(payload, "version"),
		Content:    payloadString(payload, "content"),
		IndexFile:  indexFile,
		ForkedFrom: payloadString(payload, "forkedfrom", "forkedFrom"),
		Disabled:   payloadBool(payload, "disabled"),

		PublisherGlobalMetaId: rec.PublisherGlobalMetaId,
		PublisherMetaId:       rec.PublisherMetaId,
		PublisherAddress:      rec.PublisherAddress,

		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,
	}
}

func metaAppDetailFromRecord(rec *Record) *MetaAppDetail {
	if rec == nil {
		return nil
	}
	return &MetaAppDetail{
		MetaAppItem: metaAppItemFromRecord(rec),
		Prompt:      payloadString(rec.PayloadJSON, "prompt"),
		Payload:     rec.PayloadJSON,
	}
}

// payloadString returns the first non-empty value among keys, stringified.
// Numbers are formatted so numeric version fields still surface.
func payloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		raw, ok := payload[key]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return ""
}

func payloadStringSlice(payload map[string]any, key string) []string {
	raw, ok := payload[key]
	if !ok || raw == nil {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		switch v := item.(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				out = append(out, s)
			}
		case float64:
			out = append(out, strconv.FormatFloat(v, 'f', -1, 64))
		}
	}
	return out
}

func payloadBool(payload map[string]any, key string) bool {
	raw, ok := payload[key]
	if !ok || raw == nil {
		return false
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}
