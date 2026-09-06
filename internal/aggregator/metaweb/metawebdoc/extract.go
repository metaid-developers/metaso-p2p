package metawebdoc

import (
	"strconv"
	"strings"
)

// Field caps from the search spec: summary ~200 runes, content excerpt 1024
// runes, simplebuzz title 60 runes.
const (
	SummaryMaxRunes      = 200
	ContentMaxRunes      = 1024
	SimpleBuzzTitleRunes = 60
)

// Extracted carries the derived search/pin-read meta fields for one record.
// Tags and Extra are never nil so wire responses always render arrays/objects.
type Extracted struct {
	Title          string
	Summary        string
	Tags           []string
	ContentExcerpt string
	Extra          map[string]any
}

// SkillServiceFields mirrors the chain-declared fields of a
// /protocols/skill-service record that the extraction rules need.
type SkillServiceFields struct {
	DisplayName    string
	ServiceName    string
	Description    string
	ProviderSkill  string
	Price          string
	Currency       string
	SettlementKind string
}

// ExtractForPath dispatches on the protocol key of protocolPath. Unknown
// paths get the generic best-effort treatment (title/name, intro/description).
func ExtractForPath(protocolPath string, payload map[string]any, payloadText string) Extracted {
	if ProtocolKeyForPath(protocolPath) == KeySkillService {
		return ExtractSkillService(SkillServiceFields{
			DisplayName:    stringField(payload, "displayName"),
			ServiceName:    stringField(payload, "serviceName"),
			Description:    stringField(payload, "description"),
			ProviderSkill:  stringField(payload, "providerSkill"),
			Price:          stringField(payload, "price"),
			Currency:       stringField(payload, "currency"),
			SettlementKind: stringField(payload, "settlementKind"),
		})
	}
	return ExtractPublished(protocolPath, payload, payloadText)
}

// ExtractPublished applies the search spec's extraction table for the
// protocols carried by the publishedcontent generic record pipeline.
// payloadText is the raw body for non-JSON pins (e.g. plain-text simplebuzz).
func ExtractPublished(protocolPath string, payload map[string]any, payloadText string) Extracted {
	out := Extracted{Tags: []string{}, Extra: map[string]any{}}
	switch ProtocolKeyForPath(protocolPath) {
	case KeySimpleNote:
		out.Title = stringField(payload, "title")
		out.Summary = stringField(payload, "subtitle")
		if out.Summary == "" {
			out.Summary = StripMarkdown(stringField(payload, "content"))
		}
		out.Tags = stringSliceField(payload, "tags")
		out.ContentExcerpt = CapRunes(StripMarkdown(stringField(payload, "content")), ContentMaxRunes)
		if contentType := stringField(payload, "contentType"); contentType != "" {
			out.Extra["contentType"] = contentType
		}
	case KeySimpleBuzz:
		body := strings.TrimSpace(payloadText)
		if content := stringField(payload, "content"); content != "" {
			body = content
		}
		plain := StripMarkdown(body)
		titleLine, rest := splitFirstLine(plain)
		out.Title = CapRunes(strings.TrimSpace(titleLine), SimpleBuzzTitleRunes)
		out.Summary = CapRunes(strings.TrimSpace(rest), SummaryMaxRunes)
		out.ContentExcerpt = CapRunes(plain, ContentMaxRunes)
	case KeyMetaApp:
		out.Title = firstNonEmpty(
			stringField(payload, "appName", "appname"),
			stringField(payload, "title", "name", "displayName"),
		)
		out.Summary = stringField(payload, "intro", "description", "summary")
		out.Tags = stringSliceField(payload, "tags")
		content := firstNonEmpty(stringField(payload, "content"), out.Summary)
		out.ContentExcerpt = CapRunes(content, ContentMaxRunes)
		setNonEmpty(out.Extra, "runtime", stringField(payload, "runtime"))
		setNonEmpty(out.Extra, "version", stringField(payload, "version"))
		setNonEmpty(out.Extra, "icon", stringField(payload, "icon"))
	case KeyMetaBotSkill:
		out.Title = stringField(payload, "name")
		out.Summary = stringField(payload, "description")
		out.ContentExcerpt = CapRunes(out.Summary, ContentMaxRunes)
		setNonEmpty(out.Extra, "version", stringField(payload, "version"))
	case KeyMetaProtocol:
		out.Title = firstNonEmpty(stringField(payload, "title"), stringField(payload, "protocolName"))
		out.Summary = stringField(payload, "intro")
		out.ContentExcerpt = CapRunes(firstNonEmpty(stringField(payload, "content"), out.Summary), ContentMaxRunes)
	case KeySimpleQuestion:
		out.Title = stringField(payload, "title")
		out.Summary = StripMarkdown(stringField(payload, "content"))
		out.Tags = stringSliceField(payload, "tags")
		out.ContentExcerpt = CapRunes(stringField(payload, "content"), ContentMaxRunes)
		if contentType := stringField(payload, "contentType"); contentType != "" {
			out.Extra["contentType"] = contentType
		}
	case KeySimpleAnswer:
		plain := StripMarkdown(stringField(payload, "content"))
		titleLine, rest := splitFirstLine(plain)
		out.Title = CapRunes(strings.TrimSpace(titleLine), SimpleBuzzTitleRunes)
		out.Summary = CapRunes(strings.TrimSpace(rest), SummaryMaxRunes)
		if out.Title == "" {
			out.Title = CapRunes(plain, SimpleBuzzTitleRunes)
		}
		out.Tags = stringSliceField(payload, "tags")
		out.ContentExcerpt = CapRunes(plain, ContentMaxRunes)
		setNonEmpty(out.Extra, "answerTo", stringField(payload, "answerTo"))
	default:
		// Unknown paths (pin-read remote fallback): best-effort generic fields.
		out.Title = firstNonEmpty(stringField(payload, "title"), stringField(payload, "name"))
		out.Summary = firstNonEmpty(stringField(payload, "intro"), stringField(payload, "description"))
		out.Tags = stringSliceField(payload, "tags")
		out.ContentExcerpt = CapRunes(firstNonEmpty(stringField(payload, "content"), out.Summary), ContentMaxRunes)
		if out.ContentExcerpt == "" {
			out.ContentExcerpt = CapRunes(StripMarkdown(payloadText), ContentMaxRunes)
		}
	}
	out.Summary = CapRunes(out.Summary, SummaryMaxRunes)
	if out.Tags == nil {
		out.Tags = []string{}
	}
	return out
}

// ExtractSkillService applies the extraction table for /protocols/skill-service
// records, sourced from the skillservice aggregator's typed ServiceRecord.
func ExtractSkillService(in SkillServiceFields) Extracted {
	out := Extracted{Tags: []string{}, Extra: map[string]any{}}
	out.Title = firstNonEmpty(in.DisplayName, in.ServiceName)
	out.Summary = CapRunes(in.Description, SummaryMaxRunes)
	if skill := strings.TrimSpace(in.ProviderSkill); skill != "" {
		out.Tags = []string{skill}
	}
	out.ContentExcerpt = CapRunes(in.Description, ContentMaxRunes)
	setNonEmpty(out.Extra, "price", in.Price)
	setNonEmpty(out.Extra, "currency", in.Currency)
	setNonEmpty(out.Extra, "providerSkill", in.ProviderSkill)
	setNonEmpty(out.Extra, "settlementKind", in.SettlementKind)
	return out
}

// TextForProtocol unwraps the protocol's LLM-ready body field for the
// pin-read endpoint (markdown passthrough, no stripping). Returns "" when the
// payload carries no known content field; the caller renders that as null.
func TextForProtocol(protocolKey string, payload map[string]any, payloadText string) string {
	if payload == nil {
		// Plain text/markdown bodies pass through as-is.
		return strings.TrimSpace(payloadText)
	}
	switch protocolKey {
	case KeySimpleNote, KeySimpleBuzz, KeyMetaProtocol, KeySimpleQuestion, KeySimpleAnswer:
		return stringField(payload, "content")
	case KeyMetaBotSkill, KeySkillService:
		return stringField(payload, "description")
	case KeyMetaApp:
		return stringField(payload, "intro")
	default:
		return ""
	}
}

// Attachment is one payload-declared attachment (simplebuzz-style). Size is
// nil when the payload does not declare it.
type Attachment struct {
	URI         string
	ContentType string
	Size        *int64
}

// ExtractAttachments reads the payload's `attachments` array. Entries without
// a uri/url are skipped.
func ExtractAttachments(payload map[string]any) []Attachment {
	if payload == nil {
		return nil
	}
	raw, ok := payload["attachments"].([]any)
	if !ok {
		return nil
	}
	out := make([]Attachment, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		uri := firstNonEmpty(stringField(entry, "uri"), stringField(entry, "url"))
		if uri == "" {
			continue
		}
		attachment := Attachment{
			URI:         uri,
			ContentType: firstNonEmpty(stringField(entry, "contentType"), stringField(entry, "mimeType"), stringField(entry, "type")),
		}
		if size, ok := entry["size"].(float64); ok {
			value := int64(size)
			attachment.Size = &value
		}
		out = append(out, attachment)
	}
	return out
}

// splitFirstLine returns the first line of s and the remainder.
func splitFirstLine(s string) (string, string) {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx], s[idx+1:]
	}
	return s, ""
}

func setNonEmpty(extra map[string]any, key, value string) {
	if strings.TrimSpace(value) != "" {
		extra[key] = value
	}
}

// stringField returns the first non-empty payload value among keys.
// Numbers are formatted so numeric version fields still surface.
func stringField(payload map[string]any, keys ...string) string {
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

func stringSliceField(payload map[string]any, key string) []string {
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
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
