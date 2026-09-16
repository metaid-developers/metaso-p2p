package metatask

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Payload acceptance states. Only PayloadOK is admitted to the settlement.
const (
	PayloadOK            = "ok"
	PayloadEmpty         = "empty"
	PayloadShort         = "short_item"
	PayloadMalformed     = "malformed_json"
	PayloadNotObject     = "not_object"
	PayloadLengthUnknown = "length_unknown"
)

// ErrPayloadRejected is wrapped by every §〇 payload rejection.
var ErrPayloadRejected = errors.New("payload rejected")

// PayloadVerdict is the outcome of applying the §〇 acceptance rules to one
// payload candidate.
type PayloadVerdict struct {
	State   string
	Reason  string
	Payload map[string]any
}

// Accepted reports whether the payload passed the §〇 rules.
func (v PayloadVerdict) Accepted() bool { return v.State == PayloadOK }

// acceptPayload applies the charter §〇 rules to one candidate payload:
//
//  1. the bytes must be non-empty (a missing payload is never treated as an
//     empty one);
//  2. when the indexer declares a content length, the byte count must equal it
//     exactly — a short item is rejected, never silently accepted (§〇.2/§〇.5);
//  3. the bytes must parse as a single JSON object. A payload truncated mid
//     document cannot parse, so §〇.5's silent truncation surfaces here rather
//     than flowing into the settlement as a half-record.
//
// declaredLength may be 0 when the source carries no length field (engine
// fixtures); the length check is then skipped and reported as such.
func acceptPayload(raw []byte, declaredLength int64, channel string) PayloadVerdict {
	if len(raw) == 0 {
		return PayloadVerdict{State: PayloadEmpty, Reason: "payload bytes empty (channel=" + channel + ")"}
	}
	if declaredLength > 0 && int64(len(raw)) != declaredLength {
		return PayloadVerdict{State: PayloadShort, Reason: fmt.Sprintf(
			"short item: declared contentLength=%d actual bytes=%d (channel=%s)", declaredLength, len(raw), channel)}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		return PayloadVerdict{State: PayloadMalformed, Reason: "payload does not parse as JSON (" + err.Error() + ")"}
	}
	if dec.More() {
		return PayloadVerdict{State: PayloadMalformed, Reason: "payload has trailing JSON after the object"}
	}
	obj, ok := decoded.(map[string]any)
	if !ok {
		return PayloadVerdict{State: PayloadNotObject, Reason: fmt.Sprintf(
			"payload parses as %T, not a JSON object", decoded)}
	}
	return PayloadVerdict{State: PayloadOK, Payload: obj}
}

// AcceptPayloadBytes is the exported entry point used by callers that hold raw
// payload bytes (e.g. the recompute tooling and the tests).
func AcceptPayloadBytes(raw []byte, declaredLength int64, channel string) PayloadVerdict {
	return acceptPayload(raw, declaredLength, channel)
}

// resolvePayload picks the payload bytes for a pin and applies §〇.
//
// Channels, in order of authority:
//   - "content": byte-exact bytes fetched from the pin content endpoint;
//   - "contentBody": base64 payload carried by the indexer's pin object;
//   - "contentSummary": the indexer's display summary — used only when the pin
//     object carries no byte-exact body (as in the engine's own fixtures) and
//     only if it survives the same §〇 checks.
//
// Whenever a declared length exists it is enforced on whichever channel was
// used, so a truncated summary is rejected instead of quietly becoming a record.
func resolvePayload(p *Pin) PayloadVerdict {
	if len(p.Payload) > 0 {
		v := acceptPayload(p.Payload, p.ContentLength, p.PayloadChannel)
		return v
	}
	if trimmed := strings.TrimSpace(p.ContentSummary); trimmed != "" {
		return acceptPayload([]byte(trimmed), p.ContentLength, "contentSummary")
	}
	return PayloadVerdict{State: PayloadEmpty, Reason: "no payload channel carried bytes"}
}

// DecodeBase64Payload decodes a base64 contentBody into raw bytes.
func DecodeBase64Payload(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode contentBody base64: %w", err)
	}
	return raw, nil
}

// payloadString reads a string field from a decoded payload.
func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	v, ok := payload[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	default:
		return ""
	}
}

// payloadPresent reports whether a field is "present and non-empty" under the
// on-chain engine's own rule, which is Python's str(value or "").strip().
//
// The engine therefore accepts ANY truthy JSON value, not just a string: a
// ballot publishing semantic_check as a non-empty list counts as present. That
// looseness is load-bearing for item-by-item parity — 4 of pilot #02's ballots
// ship semantic_check as ["quantifier_shape", ...] — so it is mirrored here
// rather than tightened. payloadTypeName records the observed type so a report
// can show that the field is not uniformly a string.
func payloadPresent(payload map[string]any, key string) bool {
	if payload == nil {
		return false
	}
	v, ok := payload[key]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(t) != ""
	case bool:
		return t
	case json.Number:
		f, err := t.Float64()
		return err != nil || f != 0
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	default:
		return false
	}
}

// payloadTypeName names the JSON type of a field for reporting.
func payloadTypeName(payload map[string]any, key string) string {
	if payload == nil {
		return "absent"
	}
	v, ok := payload[key]
	if !ok {
		return "absent"
	}
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "bool"
	case json.Number, float64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}

// payloadNumber reads a numeric field from a decoded payload.
func payloadNumber(payload map[string]any, key string) (float64, bool) {
	if payload == nil {
		return 0, false
	}
	v, ok := payload[key]
	if !ok || v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	case float64:
		return t, true
	case int:
		return float64(t), true
	}
	return 0, false
}
