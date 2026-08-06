package socialcontent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

func protocolPathFromPinPath(path string) string {
	base := strings.TrimRight(strings.TrimSpace(path), "/")
	if at := strings.Index(base, "@"); at > 0 {
		base = base[:at]
	}
	if idx := strings.LastIndex(base, ":/"); idx >= 0 {
		if candidate := base[idx+1:]; strings.HasPrefix(candidate, "/protocols/") {
			base = candidate
		}
	}
	return strings.ToLower(base)
}

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

func operation(pin *aggregator.PinInscription) string {
	if pin == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(pin.Operation))
}

func authorFromPin(pin *aggregator.PinInscription) AuthorItem {
	if pin == nil {
		return AuthorItem{}
	}
	global := strings.TrimSpace(pin.GlobalMetaId)
	meta := strings.TrimSpace(pin.MetaId)
	address := strings.TrimSpace(pin.Address)
	if meta == "" {
		meta = strings.TrimSpace(pin.CreateMetaId)
	}
	if address == "" {
		address = strings.TrimSpace(pin.CreateAddress)
	}
	return AuthorItem{GlobalMetaId: global, MetaId: meta, Address: address}
}

func payloadObject(raw []byte) (map[string]any, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, ErrMalformedPayload
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, ErrMalformedPayload
	}
	return obj, nil
}

func stringField(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := obj[key]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func boolField(obj map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		value, ok := obj[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return typed, true
		case string:
			switch strings.ToLower(strings.TrimSpace(typed)) {
			case "true", "1", "yes":
				return true, true
			case "false", "0", "no":
				return false, true
			}
		}
	}
	return false, false
}

func postRecordFromPin(pin *aggregator.PinInscription, sourcePinId string, current *PostRecord) *PostRecord {
	author := authorFromPin(pin)
	record := &PostRecord{
		SourcePinId:        sourcePinId,
		CurrentPinId:       pin.Id,
		ChainName:          strings.ToLower(strings.TrimSpace(pin.ChainName)),
		ProtocolPath:       PathSimpleBuzz,
		AuthorGlobalMetaId: author.GlobalMetaId,
		AuthorMetaId:       author.MetaId,
		AuthorAddress:      author.Address,
		ContentType:        strings.TrimSpace(pin.ContentType),
		CreatedAt:          pin.Timestamp,
		UpdatedAt:          pin.Timestamp,
	}
	if current != nil {
		*record = *current
		record.CurrentPinId = pin.Id
		record.UpdatedAt = pin.Timestamp
		if record.CreatedAt == 0 {
			record.CreatedAt = pin.Timestamp
		}
		if record.AuthorGlobalMetaId == "" {
			record.AuthorGlobalMetaId = author.GlobalMetaId
		}
		if record.AuthorMetaId == "" {
			record.AuthorMetaId = author.MetaId
		}
		if record.AuthorAddress == "" {
			record.AuthorAddress = author.Address
		}
		record.Hidden = false
	}

	raw := bytes.TrimSpace(pin.ContentBody)
	if len(raw) == 0 {
		raw = bytes.TrimSpace([]byte(pin.ContentSummary))
	}
	if len(raw) > 0 {
		if obj, err := payloadObject(raw); err == nil {
			record.PayloadJSON = obj
			record.PayloadText = ""
		} else {
			record.PayloadJSON = nil
			record.PayloadText = string(raw)
		}
	}
	return record
}

func parseLike(pin *aggregator.PinInscription) (*LikeEvent, error) {
	obj, err := payloadObject(pin.ContentBody)
	if err != nil {
		return nil, fmt.Errorf("paylike %s: %w", pin.Id, err)
	}
	target := stringField(obj, "likeTo", "like_to", "targetPinId", "targetPinID")
	if target == "" {
		return nil, fmt.Errorf("paylike %s: target is required: %w", pin.Id, ErrMalformedPayload)
	}
	isLike, ok := boolField(obj, "isLike", "is_like", "liked")
	if !ok {
		return nil, fmt.Errorf("paylike %s: isLike is required: %w", pin.Id, ErrMalformedPayload)
	}
	author := authorFromPin(pin)
	return &LikeEvent{
		PinId:             pin.Id,
		ChainName:         strings.ToLower(strings.TrimSpace(pin.ChainName)),
		TargetPinId:       target,
		ActorGlobalMetaId: author.GlobalMetaId,
		ActorMetaId:       author.MetaId,
		ActorAddress:      author.Address,
		IsLike:            isLike,
		Timestamp:         pin.Timestamp,
	}, nil
}

func parseComment(pin *aggregator.PinInscription) (*CommentRecord, error) {
	obj, err := payloadObject(pin.ContentBody)
	if err != nil {
		return nil, fmt.Errorf("paycomment %s: %w", pin.Id, err)
	}
	target := stringField(obj, "commentTo", "comment_to", "targetPinId", "targetPinID")
	if target == "" {
		return nil, fmt.Errorf("paycomment %s: target is required: %w", pin.Id, ErrMalformedPayload)
	}
	author := authorFromPin(pin)
	return &CommentRecord{
		PinId:              pin.Id,
		ChainName:          strings.ToLower(strings.TrimSpace(pin.ChainName)),
		TargetPinId:        target,
		AuthorGlobalMetaId: author.GlobalMetaId,
		AuthorMetaId:       author.MetaId,
		AuthorAddress:      author.Address,
		Content:            stringField(obj, "content", "text", "body"),
		ContentType:        stringField(obj, "contentType", "content_type"),
		Timestamp:          pin.Timestamp,
	}, nil
}
