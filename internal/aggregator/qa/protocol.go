package qa

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
	"github.com/metaid-developers/metaso-p2p/pkg/idaddress"
)

// lookupChains lists the chain names consulted when a raw pin id must be
// resolved without a record context (same chain first, then the rest). It
// mirrors the chains the indexer engine can register.
var lookupChains = []string{"btc", "mvc", "doge", "opcat"}

func protocolPathFromPinPath(path string) string {
	return metawebdoc.NormaliseProtocolPath(path)
}

func normaliseChain(chainName string) string {
	return strings.ToLower(strings.TrimSpace(chainName))
}

func operationOf(pin *aggregator.PinInscription) string {
	if pin == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(pin.Operation))
}

// targetPinID returns the version-target pin id of a modify/revoke pin (the
// `@<pinId>` path suffix or the `originalId` field), mirroring the
// publishedcontent / socialcontent rules.
func targetPinID(pin *aggregator.PinInscription) string {
	if pin == nil {
		return ""
	}
	if at := strings.Index(strings.TrimSpace(pin.Path), "@"); at >= 0 {
		if target := strings.Trim(strings.TrimSpace(pin.Path)[at+1:], "/"); target != "" && target != pin.Id {
			return target
		}
	}
	if target := strings.TrimPrefix(strings.TrimSpace(pin.OriginalId), "@"); target != "" && target != pin.Id {
		return target
	}
	return ""
}

// identityFromPin extracts the canonical publisher identity of a pin. The
// global id is canonicalised to the idq1… address form so one actor with
// upstream-inconsistent fields still aggregates under one key.
func identityFromPin(pin *aggregator.PinInscription) Identity {
	if pin == nil {
		return Identity{}
	}
	chain := normaliseChain(pin.ChainName)
	global := strings.TrimSpace(pin.GlobalMetaId)
	address := strings.TrimSpace(pin.Address)
	if address == "" {
		address = strings.TrimSpace(pin.CreateAddress)
	}
	if global == "" || (!strings.HasPrefix(global, "idq1") && strings.EqualFold(global, address)) {
		if encoded := idaddress.EncodeGlobalMetaId(address, chain); encoded != "" {
			global = encoded
		}
	}
	metaId := strings.TrimSpace(pin.MetaId)
	if metaId == "" {
		metaId = strings.TrimSpace(pin.CreateMetaId)
	}
	return Identity{GlobalMetaId: global, MetaId: metaId, Address: address}
}

func payloadObjectFromPin(pin *aggregator.PinInscription) (map[string]any, error) {
	if pin == nil {
		return nil, ErrMalformedPayload
	}
	raw := bytes.TrimSpace(pin.ContentBody)
	if len(raw) == 0 {
		raw = bytes.TrimSpace([]byte(pin.ContentSummary))
	}
	if len(raw) == 0 {
		// Body-less pins (e.g. a revoke without payload) are legal; callers
		// treat the empty payload as "keep previous fields".
		return nil, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, ErrMalformedPayload
	}
	return obj, nil
}

func stringField(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		raw, ok := obj[key]
		if !ok || raw == nil {
			continue
		}
		if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func stringSliceField(obj map[string]any, key string) []string {
	raw, ok := obj[key]
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

// questionPayload is the parsed /protocols/simplequestion body.
type questionPayload struct {
	Title       string
	Content     string
	Tags        []string
	ContentType string
	Attachments []string
}

func parseQuestion(pin *aggregator.PinInscription) (*questionPayload, error) {
	obj, err := payloadObjectFromPin(pin)
	if err != nil {
		return nil, fmt.Errorf("simplequestion %s: %w", pin.Id, err)
	}
	if obj == nil {
		return &questionPayload{}, nil
	}
	return &questionPayload{
		Title:       stringField(obj, "title"),
		Content:     stringField(obj, "content"),
		Tags:        stringSliceField(obj, "tags"),
		ContentType: stringField(obj, "contentType"),
		Attachments: attachmentsFromPayload(obj),
	}, nil
}

// answerPayload is the parsed /protocols/simpleanswer body.
type answerPayload struct {
	AnswerTo    string
	Content     string
	Tags        []string
	ContentType string
	Attachments []string
}

func parseAnswer(pin *aggregator.PinInscription) (*answerPayload, error) {
	obj, err := payloadObjectFromPin(pin)
	if err != nil {
		return nil, fmt.Errorf("simpleanswer %s: %w", pin.Id, err)
	}
	if obj == nil {
		return &answerPayload{}, nil
	}
	answerTo := stringField(obj, "answerTo", "answer_to")
	if answerTo == "" {
		return nil, fmt.Errorf("simpleanswer %s: answerTo is required: %w", pin.Id, ErrMalformedPayload)
	}
	content := stringField(obj, "content")
	if content == "" {
		return nil, fmt.Errorf("simpleanswer %s: content is required: %w", pin.Id, ErrMalformedPayload)
	}
	return &answerPayload{
		AnswerTo:    answerTo,
		Content:     content,
		Tags:        stringSliceField(obj, "tags"),
		ContentType: stringField(obj, "contentType"),
		Attachments: attachmentsFromPayload(obj),
	}, nil
}

// attachmentsFromPayload reads the payload's `attachments` array. Q&A keeps
// plain `metafile://<pinId>.<ext>` strings; object entries reuse the
// simplebuzz uri/url shape.
func attachmentsFromPayload(obj map[string]any) []string {
	raw, ok := obj["attachments"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		switch typed := item.(type) {
		case string:
			if s := strings.TrimSpace(typed); s != "" {
				out = append(out, s)
			}
		case map[string]any:
			if uri := stringField(typed, "uri", "url"); uri != "" {
				out = append(out, uri)
			}
		}
	}
	return out
}

// likePayload is the parsed /protocols/paylike body: IsLike 1 = like,
// −1 = dislike, 0 = cancel. Bool payloads are accepted for leniency
// (true → 1, false → 0).
type likePayload struct {
	LikeTo string
	IsLike int
}

func parseLike(pin *aggregator.PinInscription) (*likePayload, error) {
	obj, err := payloadObjectFromPin(pin)
	if err != nil {
		return nil, fmt.Errorf("paylike %s: %w", pin.Id, err)
	}
	target := stringField(obj, "likeTo", "like_to", "targetPinId")
	if target == "" {
		return nil, fmt.Errorf("paylike %s: likeTo is required: %w", pin.Id, ErrMalformedPayload)
	}
	isLike, ok := likeValue(obj["isLike"])
	if !ok {
		return nil, fmt.Errorf("paylike %s: isLike must be 1, -1 or 0: %w", pin.Id, ErrMalformedPayload)
	}
	return &likePayload{LikeTo: target, IsLike: isLike}, nil
}

func likeValue(raw any) (int, bool) {
	switch typed := raw.(type) {
	case float64:
		switch int64(typed) {
		case 1, -1, 0:
			return int(typed), true
		}
	case bool:
		if typed {
			return 1, true
		}
		return 0, true
	case string:
		if value, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
			switch value {
			case 1, -1, 0:
				return value, true
			}
		}
	}
	return 0, false
}

// commentPayload is the parsed /protocols/paycomment body. Content is stored
// in full (capped at commentContentMaxRunes at index time) so the comment
// thread endpoint can serve readable bodies.
type commentPayload struct {
	CommentTo   string
	Content     string
	ContentType string
}

// commentContentMaxRunes caps the stored comment body; comments are short and
// this endpoint is the only place to read them (see the v2 contract).
const commentContentMaxRunes = 2000

func parseComment(pin *aggregator.PinInscription) (*commentPayload, error) {
	obj, err := payloadObjectFromPin(pin)
	if err != nil {
		return nil, fmt.Errorf("paycomment %s: %w", pin.Id, err)
	}
	target := stringField(obj, "commentTo", "comment_to", "targetPinId")
	if target == "" && operationOf(pin) == OperationCreate {
		return nil, fmt.Errorf("paycomment %s: commentTo is required: %w", pin.Id, ErrMalformedPayload)
	}
	// A modify/revoke payload may omit commentTo (the version-target already
	// identifies the comment); an empty value keeps the previous target.
	return &commentPayload{
		CommentTo:   target,
		Content:     metawebdoc.CapRunes(stringField(obj, "content"), commentContentMaxRunes),
		ContentType: stringField(obj, "contentType"),
	}, nil
}
