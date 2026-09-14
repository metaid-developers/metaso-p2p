package metaweb

// Authoritative metaprotocol registry endpoints (v2 — MetaSo requirement
// "metaprotocol 权威注册表" §5, the exact counterpart of the IDBots
// metaprotocol-registry tool contract §3):
//
//	GET /api/metaweb/protocols         — registry list folded by registration
//	                                     key (one authoritative entry per
//	                                     protocol path + conflict count +
//	                                     the v1 rejected[] audit, unchanged);
//	GET /api/metaweb/protocols/check   — publish precheck (mempool
//	                                     registrations count as occupied);
//	GET /api/metaweb/protocols/detail  — one protocol's record, version
//	                                     chain, conflicts and invalid-modify
//	                                     audit.
//
// The fold itself lives in the publishedcontent aggregator (by_protocol_path
// index, maintained at write time and rebuilt at Init); this package only
// projects it onto the wire contract.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/api"
)

const (
	protocolsDefaultSize = 20
	protocolsMaxSize     = 100
)

// errInvalidProtocolCursor marks an undecodable list cursor (40000).
var errInvalidProtocolCursor = errors.New("invalid cursor")

// protocolItem is one folded registry row (contract §5.1). PinId is the
// source create pin (never changes); CurrentPinId tracks the latest version.
type protocolItem struct {
	ProtocolPath   string             `json:"protocolPath"`
	Title          string             `json:"title"`
	ProtocolName   string             `json:"protocolName"`
	Intro          string             `json:"intro"`
	Version        string             `json:"version"`
	ChainName      string             `json:"chainName"`
	PinId          string             `json:"pinId"`
	CurrentPinId   string             `json:"currentPinId"`
	CreatedAt      int64              `json:"createdAt"`
	UpdatedAt      int64              `json:"updatedAt"`
	Confirmed      bool               `json:"confirmed"`
	Author         creatorInfo        `json:"author"`
	ConflictsCount int                `json:"conflictsCount"`
	Conflicts      []protocolConflict `json:"conflicts,omitempty"`
}

// protocolConflict is the per-conflict detail attached when
// includeConflicts=true (and on the detail endpoint).
type protocolConflict struct {
	PinId        string      `json:"pinId"`
	CurrentPinId string      `json:"currentPinId"`
	ChainName    string      `json:"chainName"`
	CreatedAt    int64       `json:"createdAt"`
	UpdatedAt    int64       `json:"updatedAt"`
	Confirmed    bool        `json:"confirmed"`
	Title        string      `json:"title"`
	ProtocolName string      `json:"protocolName"`
	Version      string      `json:"version"`
	Author       creatorInfo `json:"author"`
}

type rejectedProtocol struct {
	PinId  string `json:"pinId"`
	Reason string `json:"reason"`
}

type protocolsData struct {
	Items      []protocolItem     `json:"items"`
	Rejected   []rejectedProtocol `json:"rejected"`
	HasMore    bool               `json:"hasMore"`
	NextCursor *string            `json:"nextCursor"`
}

// protocolCursor pins the key-pinned page position: the total order is
// createdAt desc, then chainName, then registration key.
type protocolCursor struct {
	CreatedAt int64  `json:"t"`
	ChainName string `json:"c"`
	RegKey    string `json:"k"`
}

func (a *Aggregator) handleProtocols(c *gin.Context) {
	if a.protocolRegistry == nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}

	size := protocolsDefaultSize
	if raw := strings.TrimSpace(c.Query("size")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			api.RespErr(c, codeInvalidParam, "invalid size")
			return
		}
		if parsed > protocolsMaxSize {
			parsed = protocolsMaxSize
		}
		size = parsed
	}
	includeConflicts := false
	if raw := strings.TrimSpace(c.Query("includeConflicts")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			api.RespErr(c, codeInvalidParam, "invalid includeConflicts")
			return
		}
		includeConflicts = parsed
	}
	pathFilter := ""
	if raw := strings.TrimSpace(c.Query("path")); raw != "" {
		key, ok := publishedcontent.ProtocolRegistrationKey(raw)
		if !ok {
			api.RespErr(c, codeInvalidParam, "invalid path")
			return
		}
		pathFilter = key
	}
	query := strings.ToLower(strings.TrimSpace(c.Query("q")))
	publisher := strings.TrimSpace(c.Query("publisher"))
	after, err := decodeProtocolCursor(c.Query("cursor"))
	if err != nil {
		api.RespErr(c, codeInvalidParam, "invalid cursor")
		return
	}

	registrations, err := a.protocolRegistry.ProtocolRegistrations()
	if err != nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}

	rows := make([]protocolItem, 0, len(registrations))
	for _, registration := range registrations {
		rec := registration.Authoritative
		if rec == nil {
			continue
		}
		if pathFilter != "" && registration.RegKey != pathFilter {
			continue
		}
		item := a.protocolItemFromRecord(rec, registration.RegKey, len(registration.Conflicts))
		if query != "" &&
			!strings.Contains(strings.ToLower(item.ProtocolName), query) &&
			!strings.Contains(strings.ToLower(item.Title), query) &&
			!strings.Contains(strings.ToLower(item.ProtocolPath), query) {
			continue
		}
		if publisher != "" && !protocolPublisherMatches(rec, publisher) {
			continue
		}
		if includeConflicts && len(registration.Conflicts) > 0 {
			item.Conflicts = make([]protocolConflict, 0, len(registration.Conflicts))
			for _, conflict := range registration.Conflicts {
				item.Conflicts = append(item.Conflicts, a.protocolConflictFromRecord(conflict))
			}
		}
		rows = append(rows, item)
	}

	// Newest registration first; the (chainName, protocolPath) suffix keeps
	// the order total so the key-pinned cursor never skips or repeats a row.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].CreatedAt != rows[j].CreatedAt {
			return rows[i].CreatedAt > rows[j].CreatedAt
		}
		if rows[i].ChainName != rows[j].ChainName {
			return rows[i].ChainName < rows[j].ChainName
		}
		return rows[i].ProtocolPath < rows[j].ProtocolPath
	})

	filtered := rows[:0]
	for _, row := range rows {
		if after != nil && !protocolRowAfter(row, after) {
			continue
		}
		filtered = append(filtered, row)
	}

	hasMore := len(filtered) > size
	page := filtered
	if hasMore {
		page = filtered[:size]
	}
	data := protocolsData{Items: page, Rejected: a.protocolRejected(), HasMore: hasMore}
	if hasMore && len(page) > 0 {
		last := page[len(page)-1]
		cursor := encodeProtocolCursor(protocolCursor{CreatedAt: last.CreatedAt, ChainName: last.ChainName, RegKey: last.ProtocolPath})
		data.NextCursor = &cursor
	}
	api.RespSuccess(c, data)
}

// protocolRowAfter reports whether row sorts strictly after the cursor in
// the list's total order (createdAt desc, chainName asc, protocolPath asc).
func protocolRowAfter(row protocolItem, cursor *protocolCursor) bool {
	if row.CreatedAt != cursor.CreatedAt {
		return row.CreatedAt < cursor.CreatedAt
	}
	if row.ChainName != cursor.ChainName {
		return row.ChainName > cursor.ChainName
	}
	return row.ProtocolPath > cursor.RegKey
}

func (a *Aggregator) protocolRejected() []rejectedProtocol {
	raw, err := a.protocolRegistry.RejectedProtocolRecords()
	rejected := make([]rejectedProtocol, 0, len(raw))
	if err != nil {
		return rejected
	}
	for _, entry := range raw {
		rejected = append(rejected, rejectedProtocol{PinId: entry.PinId, Reason: entry.Reason})
	}
	return rejected
}

// protocolPublisherMatches filters by any publisher identity layer
// (globalMetaId / metaId case-insensitively, address exactly).
func protocolPublisherMatches(rec *publishedcontent.Record, publisher string) bool {
	if rec.PublisherAddress != "" && rec.PublisherAddress == publisher {
		return true
	}
	if rec.PublisherGlobalMetaId != "" && strings.EqualFold(rec.PublisherGlobalMetaId, publisher) {
		return true
	}
	if rec.PublisherMetaId != "" && strings.EqualFold(rec.PublisherMetaId, publisher) {
		return true
	}
	return false
}

// protocolItemFromRecord projects the authoritative record onto the wire
// item. ProtocolPath is the normalized registration key (lower-cased payload
// path). Title comes from the shared published-meta extraction (the same
// projection the search and pin-read endpoints use).
func (a *Aggregator) protocolItemFromRecord(rec *publishedcontent.Record, regKey string, conflictsCount int) protocolItem {
	extracted := metawebdoc.ExtractPublished(rec.ProtocolPath, rec.PayloadJSON, rec.PayloadText)
	currentPinId := rec.CurrentPinId
	if currentPinId == "" {
		currentPinId = rec.SourcePinId
	}
	return protocolItem{
		ProtocolPath:   regKey,
		Title:          extracted.Title,
		ProtocolName:   stringFieldOf(rec.PayloadJSON, "protocolName"),
		Intro:          stringFieldOf(rec.PayloadJSON, "intro"),
		Version:        payloadVersionOf(rec.PayloadJSON),
		ChainName:      rec.ChainName,
		PinId:          rec.SourcePinId,
		CurrentPinId:   currentPinId,
		CreatedAt:      metawebdoc.NormalizeUnixSeconds(rec.CreatedAt),
		UpdatedAt:      metawebdoc.NormalizeUnixSeconds(rec.UpdatedAt),
		Confirmed:      !rec.IsMempool,
		Author:         a.protocolAuthor(rec),
		ConflictsCount: conflictsCount,
	}
}

func (a *Aggregator) protocolConflictFromRecord(rec *publishedcontent.Record) protocolConflict {
	extracted := metawebdoc.ExtractPublished(rec.ProtocolPath, rec.PayloadJSON, rec.PayloadText)
	currentPinId := rec.CurrentPinId
	if currentPinId == "" {
		currentPinId = rec.SourcePinId
	}
	return protocolConflict{
		PinId:        rec.SourcePinId,
		CurrentPinId: currentPinId,
		ChainName:    rec.ChainName,
		CreatedAt:    metawebdoc.NormalizeUnixSeconds(rec.CreatedAt),
		UpdatedAt:    metawebdoc.NormalizeUnixSeconds(rec.UpdatedAt),
		Confirmed:    !rec.IsMempool,
		Title:        extracted.Title,
		ProtocolName: stringFieldOf(rec.PayloadJSON, "protocolName"),
		Version:      payloadVersionOf(rec.PayloadJSON),
		Author:       a.protocolAuthor(rec),
	}
}

func (a *Aggregator) protocolAuthor(rec *publishedcontent.Record) creatorInfo {
	return creatorInfo{
		Address:      rec.PublisherAddress,
		MetaId:       rec.PublisherMetaId,
		GlobalMetaId: rec.PublisherGlobalMetaId,
		Name:         a.creatorName(rec.PublisherGlobalMetaId, rec.PublisherMetaId),
	}
}

func encodeProtocolCursor(cursor protocolCursor) string {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return "k:" + base64.RawURLEncoding.EncodeToString(raw)
}

func decodeProtocolCursor(raw string) (*protocolCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if !strings.HasPrefix(raw, "k:") {
		return nil, errInvalidProtocolCursor
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, "k:"))
	if err != nil {
		return nil, errInvalidProtocolCursor
	}
	var cursor protocolCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return nil, errInvalidProtocolCursor
	}
	return &cursor, nil
}

// ---------------------------------------------------------------------------
// GET /api/metaweb/protocols/check — the publish precheck (contract §5.2).
// ---------------------------------------------------------------------------

type protocolCheckExisting struct {
	PinId        string      `json:"pinId"`
	CurrentPinId string      `json:"currentPinId"`
	Title        string      `json:"title"`
	ProtocolName string      `json:"protocolName"`
	Version      string      `json:"version"`
	CreatedAt    int64       `json:"createdAt"`
	Confirmed    bool        `json:"confirmed"`
	Author       creatorInfo `json:"author"`
}

type protocolCheckData struct {
	Path      string                 `json:"path"`
	Available bool                   `json:"available"`
	Existing  *protocolCheckExisting `json:"existing"`
}

func (a *Aggregator) handleProtocolCheck(c *gin.Context) {
	if a.protocolRegistry == nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}
	raw := c.Query("path")
	if strings.TrimSpace(raw) == "" {
		api.RespErr(c, codeInvalidParam, "path is required")
		return
	}
	key, ok := publishedcontent.ProtocolRegistrationKey(raw)
	if !ok {
		api.RespErr(c, codeInvalidParam, "invalid path: "+strings.TrimSpace(raw))
		return
	}

	registration, err := a.protocolRegistry.ProtocolRegistrationByPath(key)
	if err != nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}
	data := protocolCheckData{Path: key, Available: true}
	// Mempool registrations count as occupied (anti-squatting, §5.2).
	if registration != nil && registration.Authoritative != nil {
		rec := registration.Authoritative
		extracted := metawebdoc.ExtractPublished(rec.ProtocolPath, rec.PayloadJSON, rec.PayloadText)
		currentPinId := rec.CurrentPinId
		if currentPinId == "" {
			currentPinId = rec.SourcePinId
		}
		data.Available = false
		data.Existing = &protocolCheckExisting{
			PinId:        rec.SourcePinId,
			CurrentPinId: currentPinId,
			Title:        extracted.Title,
			ProtocolName: stringFieldOf(rec.PayloadJSON, "protocolName"),
			Version:      payloadVersionOf(rec.PayloadJSON),
			CreatedAt:    metawebdoc.NormalizeUnixSeconds(rec.CreatedAt),
			Confirmed:    !rec.IsMempool,
			Author:       a.protocolAuthor(rec),
		}
	}
	api.RespSuccess(c, data)
}

// ---------------------------------------------------------------------------
// GET /api/metaweb/protocols/detail — one protocol's full view (contract
// §5.3): record (item fields + raw payload passthrough), the version chain
// (existing chain mechanism, payload versions best-effort), the same-key
// conflicts, and the invalid-modify audit.
// ---------------------------------------------------------------------------

type protocolRecordDetail struct {
	protocolItem
	Payload map[string]any `json:"payload"`
}

type protocolVersionItem struct {
	PinId       string      `json:"pinId"`
	Version     string      `json:"version"`
	Timestamp   int64       `json:"timestamp"`
	Author      creatorInfo `json:"author"`
	Attribution string      `json:"attribution"`
}

type protocolDetailData struct {
	Record          protocolRecordDetail            `json:"record"`
	Versions        []protocolVersionItem           `json:"versions"`
	Conflicts       []protocolConflict              `json:"conflicts"`
	InvalidModifies []*publishedcontent.InvalidModify `json:"invalidModifies"`
}

func (a *Aggregator) handleProtocolDetail(c *gin.Context) {
	if a.protocolRegistry == nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}

	var rec *publishedcontent.Record
	var registration *publishedcontent.ProtocolRegistration
	if raw := strings.TrimSpace(c.Query("path")); raw != "" {
		// path wins over pinId when both are given (contract §5.3).
		key, ok := publishedcontent.ProtocolRegistrationKey(raw)
		if !ok {
			api.RespErr(c, codeInvalidParam, "invalid path: "+raw)
			return
		}
		found, err := a.protocolRegistry.ProtocolRegistrationByPath(key)
		if err != nil {
			api.RespErr(c, codeUnavailable, "aggregation unavailable")
			return
		}
		if found == nil || found.Authoritative == nil {
			api.RespErr(c, codeNotFound, "protocol not found")
			return
		}
		registration = found
		rec = found.Authoritative
	} else if pinId := strings.TrimSpace(c.Query("pinId")); pinId != "" {
		if !pinIDPattern.MatchString(pinId) {
			api.RespErr(c, codeInvalidParam, "malformed pinId")
			return
		}
		if a.pinLookup == nil {
			api.RespErr(c, codeUnavailable, "aggregation unavailable")
			return
		}
		found, err := a.pinLookup.LookupByAnyPinId(pinId)
		if err != nil {
			api.RespErr(c, codeUnavailable, "aggregation unavailable")
			return
		}
		if found == nil || found.ProtocolPath != publishedcontent.PathMetaProtocol {
			api.RespErr(c, codeNotFound, "protocol not found")
			return
		}
		rec = found
		// The record may be a conflict entry of its key (or an invalid
		// payload no key holds): fold the same-key siblings so the audit
		// lists every other registration, excluding the record itself.
		if key, ok := publishedcontent.ProtocolRegistrationKey(stringFieldOf(rec.PayloadJSON, "path")); ok {
			if found, err := a.protocolRegistry.ProtocolRegistrationByPath(key); err == nil && found != nil {
				registration = found
			}
		}
	} else {
		api.RespErr(c, codeInvalidParam, "path or pinId is required")
		return
	}

	regKey, _ := publishedcontent.ProtocolRegistrationKey(stringFieldOf(rec.PayloadJSON, "path"))
	conflicts := make([]protocolConflict, 0)
	if registration != nil {
		if registration.Authoritative != nil && registration.Authoritative.SourcePinId != rec.SourcePinId {
			conflicts = append(conflicts, a.protocolConflictFromRecord(registration.Authoritative))
		}
		for _, conflict := range registration.Conflicts {
			if conflict.SourcePinId == rec.SourcePinId {
				continue
			}
			conflicts = append(conflicts, a.protocolConflictFromRecord(conflict))
		}
	}

	// Version chain: the existing mechanism (chain projection modify_history
	// authoritative + local fallback), oldest first, with payload versions
	// backfilled best-effort.
	versions := make([]protocolVersionItem, 0)
	current := firstNonEmptyString(rec.CurrentPinId, rec.SourcePinId)
	chain, err := a.VersionChain(current)
	if err != nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}
	for _, entry := range a.fillPayloadVersions(chain.entries) {
		versions = append(versions, protocolVersionItem{
			PinId:     entry.PinId,
			Version:   entry.PayloadVersion,
			Timestamp: entry.CreatedAt,
			Author: creatorInfo{
				Address:      entry.Address,
				MetaId:       entry.MetaId,
				GlobalMetaId: entry.GlobalMetaId,
				Name:         a.creatorName(entry.GlobalMetaId, entry.MetaId),
			},
			Attribution: chain.attribution,
		})
	}

	invalidModifies, err := a.protocolRegistry.InvalidModifies(rec.ChainName, rec.SourcePinId)
	if err != nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}
	if invalidModifies == nil {
		invalidModifies = []*publishedcontent.InvalidModify{}
	}

	var payload map[string]any
	if rec.PayloadExposed {
		// Raw passthrough: protocolContent stays an unparsed string — MetaSo
		// never interprets the descriptor body (contract §5.3).
		payload = rec.PayloadJSON
	}
	api.RespSuccess(c, protocolDetailData{
		Record: protocolRecordDetail{
			protocolItem: a.protocolItemFromRecord(rec, regKey, len(conflicts)),
			Payload:      payload,
		},
		Versions:        versions,
		Conflicts:       conflicts,
		InvalidModifies: invalidModifies,
	})
}
