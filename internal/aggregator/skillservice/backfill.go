package skillservice

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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

type BackfillOptions struct {
	Context  context.Context
	Client   *BackfillClient
	Paths    []string
	Since    time.Time
	PageSize int
}

type BackfillClient struct {
	baseURL    string
	httpClient *http.Client
}

const defaultBackfillPageSize = 100

var defaultBackfillPaths = []string{PathSkillService, PathSkillServiceRate}

func DefaultBackfillPaths() []string {
	return append([]string(nil), defaultBackfillPaths...)
}

func NewBackfillClient(baseURL string, httpClient *http.Client) *BackfillClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &BackfillClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: httpClient,
	}
}

func (a *Aggregator) Backfill(opts BackfillOptions) error {
	if a == nil {
		return errors.New("skillservice backfill aggregator is required")
	}
	client := opts.Client
	if client == nil {
		return errors.New("skillservice backfill client is required")
	}
	paths := normaliseBackfillPaths(opts.Paths)
	for _, path := range paths {
		if path == PathSkillService {
			// The standalone backfill has no userinfo resolver. Mark the
			// canonical provider indexes stale before mutating records so the
			// main service rebuilds them with profile resolution on restart,
			// including when this backfill exits early with an error.
			if err := a.invalidateHomepageProviderGlobalIndexState(); err != nil {
				return err
			}
			break
		}
	}
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = defaultBackfillPageSize
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}

	for _, path := range paths {
		if path == PathSkillService {
			if err := a.backfillServices(ctx, client, opts.Since, pageSize); err != nil {
				return err
			}
			continue
		}
		pins, err := fetchBackfillPins(ctx, client, path, opts.Since, pageSize)
		if err != nil {
			return err
		}
		if err := a.replayBackfillPins(pins); err != nil {
			return err
		}
	}
	return nil
}

func fetchBackfillPins(ctx context.Context, client *BackfillClient, path string, since time.Time, pageSize int) ([]manapiPin, error) {
	cursor := ""
	seenCursors := make(map[string]struct{})
	pins := make([]manapiPin, 0, pageSize)
	for {
		seenCursors[cursor] = struct{}{}
		page, err := client.ListPath(ctx, path, cursor, pageSize)
		if err != nil {
			return nil, err
		}
		if len(page.Pins) == 0 {
			break
		}

		allOlder := true
		for _, pin := range page.Pins {
			if manapiTimestampBefore(pin.Timestamp, since) {
				continue
			}
			allOlder = false
			pins = append(pins, pin)
		}
		if allOlder || page.NextCursor == "" || len(page.Pins) < pageSize {
			break
		}
		if _, seen := seenCursors[page.NextCursor]; seen {
			return nil, fmt.Errorf("repeated MANAPI cursor %q for path %s", page.NextCursor, path)
		}
		cursor = page.NextCursor
	}
	return pins, nil
}

func (a *Aggregator) backfillServices(ctx context.Context, client *BackfillClient, since time.Time, pageSize int) error {
	// Old service creates can receive a new version years later. Discover all
	// sources first, then apply the lookback window to each source's versions.
	basePins, err := fetchBackfillPins(ctx, client, PathSkillService, time.Time{}, pageSize)
	if err != nil {
		return err
	}

	sources := make(map[string]manapiPin)
	selected := make(map[string]manapiPin)
	versionSources := make(map[string]string)
	for _, pin := range basePins {
		if strings.EqualFold(strings.TrimSpace(pin.Operation), OperationCreate) {
			sourceID := strings.TrimSpace(pin.ID)
			sources[sourceID] = pin
			recordVersionSources(versionSources, sourceID, pin)
		}
	}

	for sourceID, source := range sources {
		versions, err := fetchTargetedServicePins(ctx, client, source, since, pageSize)
		if err != nil {
			return err
		}
		if manapiTimestampBefore(source.Timestamp, since) && len(versions) == 0 {
			continue
		}
		selected[sourceID] = source
		for _, version := range versions {
			selected[strings.TrimSpace(version.ID)] = version
			recordVersionSources(versionSources, sourceID, version)
		}
	}

	// Some MANAPI deployments include version pins in the exact protocol-path
	// response. Keep accepting those records and pull in their source create.
	for _, pin := range basePins {
		if strings.EqualFold(strings.TrimSpace(pin.Operation), OperationCreate) || manapiTimestampBefore(pin.Timestamp, since) {
			continue
		}
		selected[strings.TrimSpace(pin.ID)] = pin
		targetID := firstNonEmpty(pinTargetFromPath(pin.Path), strings.TrimPrefix(strings.TrimSpace(pin.OriginalId), "@"))
		sourceID := versionSources[targetID]
		if sourceID == "" {
			sourceID = targetID
		}
		if source, ok := sources[sourceID]; ok {
			selected[sourceID] = source
			recordVersionSources(versionSources, sourceID, pin)
		}
	}

	pins := make([]manapiPin, 0, len(selected))
	for _, pin := range selected {
		if strings.TrimSpace(pin.ID) != "" {
			pins = append(pins, pin)
		}
	}
	// Build the complete version-to-source index before replay. Several MVC
	// versions can share one block timestamp/height, so a chronological sort
	// alone cannot guarantee that every target version has already replayed.
	for pinID, sourceID := range versionSources {
		if pinID == "" || sourceID == "" || pinID == sourceID {
			continue
		}
		if err := a.mapPinToSource(sources[sourceID].ChainName, pinID, sourceID); err != nil {
			return err
		}
	}
	return a.replayBackfillPins(pins)
}

func recordVersionSources(index map[string]string, sourceID string, pin manapiPin) {
	if index == nil || strings.TrimSpace(sourceID) == "" {
		return
	}
	for _, pinID := range append([]string{pin.ID}, pin.ModifyHistory...) {
		pinID = strings.Trim(strings.TrimSpace(pinID), "@/")
		if pinID != "" {
			index[pinID] = sourceID
		}
	}
}

func fetchTargetedServicePins(ctx context.Context, client *BackfillClient, seed manapiPin, since time.Time, pageSize int) ([]manapiPin, error) {
	queue := make([]string, 0)
	queued := make(map[string]struct{})
	enqueue := func(paths ...string) {
		for _, path := range paths {
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}
			if _, ok := queued[path]; ok {
				continue
			}
			queued[path] = struct{}{}
			queue = append(queue, path)
		}
	}
	enqueue(seed.targetedVersionBackfillPaths()...)

	seenPins := make(map[string]struct{})
	versions := make([]manapiPin, 0)
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		pins, err := fetchBackfillPins(ctx, client, path, since, pageSize)
		if err != nil {
			return nil, err
		}
		for _, pin := range pins {
			pinID := strings.TrimSpace(pin.ID)
			if pinID == "" {
				continue
			}
			if _, ok := seenPins[pinID]; !ok {
				seenPins[pinID] = struct{}{}
				versions = append(versions, pin)
			}
			enqueue(pin.targetedVersionBackfillPaths()...)
		}
	}
	return versions, nil
}

func (a *Aggregator) replayBackfillPins(pins []manapiPin) error {
	sort.SliceStable(pins, func(i, j int) bool {
		left := serviceRevision{
			Timestamp:     normaliseServiceTimestampMillis(pins[i].Timestamp),
			GenesisHeight: pins[i].GenesisHeight,
			PinID:         strings.TrimSpace(pins[i].ID),
		}
		right := serviceRevision{
			Timestamp:     normaliseServiceTimestampMillis(pins[j].Timestamp),
			GenesisHeight: pins[j].GenesisHeight,
			PinID:         strings.TrimSpace(pins[j].ID),
		}
		return serviceRevisionAfter(right, left)
	})
	for _, pin := range pins {
		if _, err := a.HandleBlockPin(pin.toAggregatorPin()); err != nil {
			return err
		}
	}
	return nil
}

func normaliseBackfillPaths(paths []string) []string {
	allowed := make(map[string]struct{}, len(defaultBackfillPaths))
	for _, path := range defaultBackfillPaths {
		allowed[path] = struct{}{}
	}
	if len(paths) == 0 {
		return DefaultBackfillPaths()
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		normalised := protocolPathFromPinPath(path)
		if _, ok := allowed[normalised]; ok {
			out = append(out, normalised)
		}
	}
	if len(out) == 0 {
		return DefaultBackfillPaths()
	}
	return out
}

type backfillPage struct {
	Pins       []manapiPin
	NextCursor string
}

func (c *BackfillClient) ListPath(ctx context.Context, path, cursor string, size int) (backfillPage, error) {
	if c == nil || strings.TrimSpace(c.baseURL) == "" {
		return backfillPage{}, errors.New("MANAPI base URL is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestURL, err := c.listURL(path, cursor, size)
	if err != nil {
		return backfillPage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return backfillPage{}, fmt.Errorf("create MANAPI backfill request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return backfillPage{}, fmt.Errorf("fetch MANAPI backfill pins: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return backfillPage{}, fmt.Errorf("MANAPI backfill returned HTTP %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return backfillPage{}, fmt.Errorf("read MANAPI backfill response: %w", err)
	}
	page, err := decodeBackfillPage(raw)
	if err != nil {
		return backfillPage{}, err
	}
	return page, nil
}

func (c *BackfillClient) listURL(path, cursor string, size int) (string, error) {
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("parse MANAPI base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("MANAPI base URL requires scheme and host")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/pin/path/list"
	}
	q := parsed.Query()
	q.Set("cursor", cursor)
	q.Set("size", strconv.Itoa(size))
	q.Set("path", path)
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

func decodeBackfillPage(raw []byte) (backfillPage, error) {
	var envelope struct {
		Code       int         `json:"code"`
		Message    string      `json:"message"`
		List       []manapiPin `json:"list"`
		NextCursor string      `json:"nextCursor"`
		Cursor     string      `json:"cursor"`
		Data       struct {
			List       []manapiPin `json:"list"`
			NextCursor string      `json:"nextCursor"`
			Cursor     string      `json:"cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return backfillPage{}, fmt.Errorf("decode MANAPI backfill response: %w", err)
	}
	if envelope.Code != 0 && envelope.Code != 1 {
		return backfillPage{}, fmt.Errorf("MANAPI backfill failed: code=%d message=%s", envelope.Code, envelope.Message)
	}
	page := backfillPage{
		Pins:       envelope.Data.List,
		NextCursor: firstNonEmpty(envelope.Data.NextCursor, envelope.Data.Cursor, envelope.NextCursor, envelope.Cursor),
	}
	if page.Pins == nil {
		page.Pins = envelope.List
	}
	return page, nil
}

type manapiPin struct {
	ID             string             `json:"id"`
	Path           string             `json:"path"`
	OriginalPath   string             `json:"originalPath"`
	Operation      string             `json:"operation"`
	ContentType    string             `json:"contentType"`
	ContentBody    manapiContentBytes `json:"contentBody"`
	ContentSummary string             `json:"contentSummary"`
	MetaId         string             `json:"metaId"`
	GlobalMetaId   string             `json:"globalMetaId"`
	Address        string             `json:"address"`
	CreateMetaId   string             `json:"createMetaId"`
	CreateAddress  string             `json:"createAddress"`
	ChainName      string             `json:"chainName"`
	Timestamp      int64              `json:"timestamp"`
	GenesisHeight  int64              `json:"genesisHeight"`
	OriginalId     string             `json:"originalId"`
	ModifyHistory  []string           `json:"modify_history"`
}

func (p manapiPin) targetedVersionBackfillPaths() []string {
	ids := make([]string, 0, len(p.ModifyHistory)+1)
	ids = append(ids, p.ID)
	ids = append(ids, p.ModifyHistory...)
	paths := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.Trim(strings.TrimSpace(id), "@/")
		if id == "" {
			continue
		}
		path := "@" + id
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func (p manapiPin) toAggregatorPin() *aggregator.PinInscription {
	body := p.ContentBody.Bytes()
	if len(bytes.TrimSpace(body)) == 0 && strings.TrimSpace(p.ContentSummary) != "" {
		body = []byte(strings.TrimSpace(p.ContentSummary))
	}
	return &aggregator.PinInscription{
		Id:             strings.TrimSpace(p.ID),
		Path:           strings.TrimSpace(p.Path),
		OriginalPath:   strings.TrimSpace(p.OriginalPath),
		Operation:      strings.TrimSpace(p.Operation),
		ContentType:    strings.TrimSpace(p.ContentType),
		ContentBody:    body,
		ContentSummary: strings.TrimSpace(p.ContentSummary),
		MetaId:         strings.TrimSpace(p.MetaId),
		GlobalMetaId:   strings.TrimSpace(p.GlobalMetaId),
		Address:        strings.TrimSpace(p.Address),
		CreateMetaId:   strings.TrimSpace(p.CreateMetaId),
		CreateAddress:  strings.TrimSpace(p.CreateAddress),
		ChainName:      strings.TrimSpace(p.ChainName),
		GenesisHeight:  p.GenesisHeight,
		Timestamp:      p.Timestamp,
		OriginalId:     strings.TrimSpace(p.OriginalId),
	}
}

type manapiContentBytes []byte

func (b *manapiContentBytes) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*b = nil
		return nil
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			*b = nil
			return nil
		}
		if decoded, ok := maybeDecodeBase64Content(text); ok {
			*b = decoded
			return nil
		}
		*b = []byte(text)
		return nil
	}

	var raw json.RawMessage
	if err := json.Unmarshal(trimmed, &raw); err == nil {
		*b = append((*b)[:0], raw...)
		return nil
	}
	*b = append((*b)[:0], trimmed...)
	return nil
}

func (b manapiContentBytes) Bytes() []byte {
	return append([]byte(nil), b...)
}

func maybeDecodeBase64Content(text string) ([]byte, bool) {
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		return nil, false
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(text)
		if err != nil || len(decoded) == 0 {
			continue
		}
		if json.Valid(decoded) {
			return decoded, true
		}
	}
	return nil, false
}

func manapiTimestampBefore(timestamp int64, since time.Time) bool {
	if since.IsZero() || timestamp <= 0 {
		return false
	}
	cutoffMillis := since.UnixMilli()
	if timestamp > 100000000000 {
		return timestamp < cutoffMillis
	}
	return timestamp < since.Unix()
}
