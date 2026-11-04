package mrc721

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MRC-721 pin id convention: `<64-hex txid>i<output index>` and MVC/SPACE
// mainnet addresses are base58 (same shapes the host-side parser enforces).
var (
	pinIdPattern   = regexp.MustCompile(`^[0-9a-f]{64}i[0-9]+$`)
	addressPattern = regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{20,}$`)
	hex64Pattern   = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
)

// sellIntentPayload is the validated, canonicalised view of one
// /protocols/mrc721sell pin payload. Key resolution is tolerant: the exact
// camelCase spelling wins, then a case-insensitive fallback covers the
// snake_case / lowercase duplicates seen in the wild (same stance as the
// IDBots host-side parser).
type sellIntentPayload struct {
	ItemPinId          string
	SellerAddress      string
	Active             bool
	CollectionName     string
	CollectionPinId    string
	ItemOutpoint       *ItemOutpoint
	SellerMetaId       string
	PriceHintSats      int64
	AcceptedCurrencies []string
	Settlement         []string
	Note               string
	CreatedAt          int64
	ExpiresAt          int64
}

// parseSellIntentPayload parses and validates one sell-intent content body.
// Required fields: itemPinId, sellerAddress, active (docs
// nft-sell-protocol-draft.md §2.2); everything else is advisory. Any
// violation returns an error — the ingest path logs and skips such pins.
func parseSellIntentPayload(raw []byte) (*sellIntentPayload, error) {
	fields, err := decodePayloadObject(raw)
	if err != nil {
		return nil, err
	}

	payload := &sellIntentPayload{}
	var ok bool

	if payload.ItemPinId, ok = payloadString(fields, "itemPinId"); !ok {
		return nil, fmt.Errorf("missing itemPinId")
	}
	if !pinIdPattern.MatchString(payload.ItemPinId) {
		return nil, fmt.Errorf("itemPinId %q is not a <64hex>i<n> pin id", payload.ItemPinId)
	}
	if payload.SellerAddress, ok = payloadString(fields, "sellerAddress"); !ok {
		return nil, fmt.Errorf("missing sellerAddress")
	}
	if !addressPattern.MatchString(payload.SellerAddress) {
		return nil, fmt.Errorf("sellerAddress %q is not a base58 MVC address", payload.SellerAddress)
	}
	activeValue, ok := payloadLookup(fields, "active")
	if !ok {
		return nil, fmt.Errorf("missing active")
	}
	if payload.Active, ok = payloadBool(activeValue); !ok {
		return nil, fmt.Errorf("active is not a boolean")
	}

	payload.CollectionName, _ = payloadString(fields, "collectionName")
	payload.CollectionPinId, _ = payloadString(fields, "collectionPinId")
	payload.ItemOutpoint = payloadOutpoint(fields)
	payload.SellerMetaId, _ = payloadString(fields, "sellerMetaId")
	payload.PriceHintSats, _ = payloadInt(fields, "priceHintSats")
	payload.AcceptedCurrencies = payloadStringList(fields, "acceptedCurrencies")
	payload.Settlement = payloadStringList(fields, "settlement")
	payload.Note, _ = payloadString(fields, "note")
	payload.CreatedAt, _ = payloadInt(fields, "createdAt")
	payload.ExpiresAt, _ = payloadInt(fields, "expiresAt")

	// Timestamps are epoch seconds; tolerate millisecond values the same
	// way the backfill cutoff rule does.
	if payload.CreatedAt > 100000000000 {
		payload.CreatedAt /= 1000
	}
	if payload.ExpiresAt > 100000000000 {
		payload.ExpiresAt /= 1000
	}
	return payload, nil
}

// decodePayloadObject turns a content body into a generic field map,
// tolerating raw JSON, quoted JSON strings, and base64-wrapped JSON bodies
// (MANAPI and the chain indexers hand over different encodings).
func decodePayloadObject(raw []byte) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty payload")
	}

	candidates := [][]byte{trimmed}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		if inner := bytes.TrimSpace([]byte(text)); len(inner) > 0 {
			candidates = append(candidates, inner)
		}
	}
	if decoded, err := base64.StdEncoding.DecodeString(string(trimmed)); err == nil {
		candidates = append(candidates, bytes.TrimSpace(decoded))
	} else if decoded, err := base64.RawStdEncoding.DecodeString(string(trimmed)); err == nil {
		candidates = append(candidates, bytes.TrimSpace(decoded))
	}

	for _, candidate := range candidates {
		if len(candidate) == 0 || candidate[0] != '{' {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(candidate))
		decoder.UseNumber()
		fields := map[string]json.RawMessage{}
		if err := decoder.Decode(&fields); err == nil {
			return fields, nil
		}
	}
	return nil, fmt.Errorf("payload is not a JSON object")
}

// payloadLookup resolves a field by its exact spelling first, then by a
// case-insensitive match (itemPinId vs itempinid), then ignoring underscores
// (collectionName vs collection_name) — the duplicate spellings seen across
// writers.
func payloadLookup(fields map[string]json.RawMessage, key string) (json.RawMessage, bool) {
	if raw, ok := fields[key]; ok {
		return raw, true
	}
	lower := strings.ToLower(key)
	folded := strings.ReplaceAll(lower, "_", "")
	for name, raw := range fields {
		nameLower := strings.ToLower(name)
		if nameLower == lower || strings.ReplaceAll(nameLower, "_", "") == folded {
			return raw, true
		}
	}
	return nil, false
}

func payloadString(fields map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := payloadLookup(fields, key)
	if !ok {
		return "", false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", false
	}
	text = strings.TrimSpace(text)
	return text, text != ""
}

func payloadBool(raw json.RawMessage) (bool, bool) {
	var value bool
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, true
	}
	// Tolerate "true"/"false" strings.
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

func payloadInt(fields map[string]json.RawMessage, key string) (int64, bool) {
	raw, ok := payloadLookup(fields, key)
	if !ok {
		return 0, false
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, false
	}
	value, err := strconv.ParseInt(string(number), 10, 64)
	if err != nil {
		// Tolerate float-formatted integers ("30000.0").
		if floatValue, floatErr := strconv.ParseFloat(string(number), 64); floatErr == nil {
			return int64(floatValue), true
		}
		return 0, false
	}
	return value, true
}

// payloadStringList reads a JSON array of strings, dropping empty and
// non-string entries (IDs emit `String(entry)` and filter falsy values).
func payloadStringList(fields map[string]json.RawMessage, key string) []string {
	raw, ok := payloadLookup(fields, key)
	if !ok {
		return nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		var text string
		if err := json.Unmarshal(entry, &text); err != nil {
			continue
		}
		if text = strings.TrimSpace(text); text != "" {
			out = append(out, text)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// payloadOutpoint reads the advisory itemOutpoint object.
func payloadOutpoint(fields map[string]json.RawMessage) *ItemOutpoint {
	raw, ok := payloadLookup(fields, "itemOutpoint")
	if !ok {
		return nil
	}
	var sub map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sub); err != nil {
		return nil
	}
	out := &ItemOutpoint{}
	if text, ok := payloadString(sub, "txid"); ok && hex64Pattern.MatchString(text) {
		out.TxID = strings.ToLower(text)
	} else if text != "" {
		out.TxID = text
	}
	out.Vout, _ = payloadInt(sub, "vout")
	out.Satoshis, _ = payloadInt(sub, "satoshis")
	return out
}
