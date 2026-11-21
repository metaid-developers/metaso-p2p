package paybuzz

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

type parsedPayload struct {
	Title            string
	Preview          string
	ContentType      string
	WordCount        int
	Encryption       EncryptionMeta
	AuthorMetaId     string
	AuthorMvcAddress string
	ShopMetaAppPinId string
	UnlockRules      UnlockRules
	CreatedAt        int64
}

func parsePaybuzzPayload(raw []byte) (*parsedPayload, error) {
	fields, err := decodeJSONObject(raw)
	if err != nil {
		return nil, err
	}

	title, ok := fieldString(fields, "title")
	if !ok {
		return nil, fmt.Errorf("missing title")
	}
	preview, _ := fieldString(fields, "preview")
	if utf8.RuneCountInString(preview) > 1000 {
		preview = string([]rune(preview)[:1000])
	}
	contentType, _ := fieldString(fields, "contentType")
	if contentType == "" {
		contentType = "text/markdown"
	}
	wordCount := 0
	if wc, hasWC := fieldInt(fields, "wordCount"); hasWC && wc > 0 {
		wordCount = int(wc)
	}

	encRaw, ok := fieldLookup(fields, "encryption")
	if !ok {
		return nil, fmt.Errorf("missing encryption")
	}
	enc, err := parseEncryptionMeta(encRaw)
	if err != nil {
		return nil, err
	}

	rulesRaw, ok := fieldLookup(fields, "unlockRules")
	if !ok {
		return nil, fmt.Errorf("missing unlockRules")
	}
	rules, err := parseUnlockRules(rulesRaw)
	if err != nil {
		return nil, err
	}

	authorMetaId, _ := fieldString(fields, "authorMetaId")
	authorMvcAddress, _ := fieldString(fields, "authorMvcAddress")
	shopMetaAppPinId, _ := fieldString(fields, "shopMetaAppPinId")
	createdAt, _ := fieldInt(fields, "createdAt")
	if createdAt > 100000000000 {
		createdAt /= 1000
	}

	return &parsedPayload{
		Title:            title,
		Preview:          preview,
		ContentType:      contentType,
		WordCount:        wordCount,
		Encryption:       enc,
		AuthorMetaId:     authorMetaId,
		AuthorMvcAddress: authorMvcAddress,
		ShopMetaAppPinId: shopMetaAppPinId,
		UnlockRules:      rules,
		CreatedAt:        createdAt,
	}, nil
}

func parseEncryptionMeta(raw json.RawMessage) (EncryptionMeta, error) {
	var sub map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sub); err != nil {
		return EncryptionMeta{}, fmt.Errorf("encryption must be an object")
	}
	algo, _ := fieldString(sub, "algo")
	if algo == "" {
		algo = "AES-256-GCM"
	}
	if !strings.EqualFold(algo, "AES-256-GCM") {
		return EncryptionMeta{}, fmt.Errorf("unsupported encryption.algo %q", algo)
	}
	ivHex, ok := fieldString(sub, "ivHex")
	if !ok {
		return EncryptionMeta{}, fmt.Errorf("missing encryption.ivHex")
	}
	authTagHex, ok := fieldString(sub, "authTagHex")
	if !ok {
		return EncryptionMeta{}, fmt.Errorf("missing encryption.authTagHex")
	}
	cipherSha256, ok := fieldString(sub, "cipherSha256")
	if !ok {
		return EncryptionMeta{}, fmt.Errorf("missing encryption.cipherSha256")
	}
	ciphertextRef, ok := fieldString(sub, "ciphertextPinOrBase64")
	if !ok {
		return EncryptionMeta{}, fmt.Errorf("missing encryption.ciphertextPinOrBase64")
	}
	keySaltHex, _ := fieldString(sub, "keySaltHex")

	return EncryptionMeta{
		Algo:                  "AES-256-GCM",
		IvHex:                 strings.ToLower(ivHex),
		AuthTagHex:            strings.ToLower(authTagHex),
		CipherSha256:          strings.ToLower(cipherSha256),
		KeySaltHex:            strings.ToLower(keySaltHex),
		CiphertextPinOrBase64: ciphertextRef,
	}, nil
}

func parseUnlockRules(raw json.RawMessage) (UnlockRules, error) {
	var sub map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sub); err != nil {
		return UnlockRules{}, fmt.Errorf("unlockRules must be an object")
	}

	out := UnlockRules{
		TokenPrices:          []TokenPriceRule{},
		VipMrc721Collections: []VipCollectionRule{},
	}

	if meVal, hasMe := fieldInt(sub, "mePriceMilli"); hasMe {
		if meVal < 0 {
			return UnlockRules{}, fmt.Errorf("unlockRules.mePriceMilli must be non-negative")
		}
		if meVal > 0 {
			out.MePriceMilli = &meVal
		}
	}

	if tokRaw, ok := fieldLookup(sub, "tokenPrices"); ok {
		trimmed := bytes.TrimSpace(tokRaw)
		if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
			var entries []json.RawMessage
			if err := json.Unmarshal(trimmed, &entries); err != nil {
				return UnlockRules{}, fmt.Errorf("unlockRules.tokenPrices must be an array")
			}
			seen := make(map[string]struct{}, len(entries))
			for i, entry := range entries {
				var tf map[string]json.RawMessage
				if err := json.Unmarshal(entry, &tf); err != nil {
					return UnlockRules{}, fmt.Errorf("unlockRules.tokenPrices[%d] must be an object", i)
				}
				mrc20Id, _ := fieldString(tf, "mrc20Id")
				codehash, _ := fieldString(tf, "codehash")
				genesis, _ := fieldString(tf, "genesis")
				mrc20Id = strings.ToLower(mrc20Id)
				codehash = strings.ToLower(codehash)
				genesis = strings.ToLower(genesis)

				assetKey, _ := fieldString(tf, "assetKey")
				assetKey = normalizeAssetKey(assetKey)
				if assetKey == "" {
					if mrc20Id != "" {
						assetKey = "mrc20:" + mrc20Id
					} else if codehash != "" && genesis != "" {
						assetKey = "mvc-ft:" + codehash + ":" + genesis
					}
				}
				if assetKey == "" || (!strings.HasPrefix(assetKey, "mrc20:") && !strings.HasPrefix(assetKey, "mvc-ft:")) {
					return UnlockRules{}, fmt.Errorf("unlockRules.tokenPrices[%d].assetKey invalid", i)
				}

				amountBase, ok := fieldDecimalString(tf, "amountBase")
				if !ok {
					return UnlockRules{}, fmt.Errorf("unlockRules.tokenPrices[%d].amountBase required", i)
				}
				bi, validInt := new(big.Int).SetString(amountBase, 10)
				if !validInt || bi.Sign() <= 0 || strings.HasPrefix(amountBase, "+") {
					return UnlockRules{}, fmt.Errorf("unlockRules.tokenPrices[%d].amountBase must be positive integer", i)
				}

				decimals := int64(8)
				if decVal, hasDec := fieldInt(tf, "decimals"); hasDec {
					if decVal < 0 {
						return UnlockRules{}, fmt.Errorf("unlockRules.tokenPrices[%d].decimals must be non-negative", i)
					}
					decimals = decVal
				}

				chain, _ := fieldString(tf, "chain")
				chain = strings.ToLower(chain)
				if chain == "" {
					if strings.HasPrefix(assetKey, "mrc20:") {
						chain = "btc"
					} else {
						chain = "mvc"
					}
				}
				standard, _ := fieldString(tf, "standard")
				if standard == "" {
					if chain == "btc" {
						standard = "mrc20"
					} else {
						standard = "metacontract-ft"
					}
				}
				if mrc20Id == "" && strings.HasPrefix(assetKey, "mrc20:") {
					mrc20Id = strings.TrimPrefix(assetKey, "mrc20:")
				}
				if (codehash == "" || genesis == "") && strings.HasPrefix(assetKey, "mvc-ft:") {
					parts := strings.SplitN(strings.TrimPrefix(assetKey, "mvc-ft:"), ":", 2)
					if len(parts) == 2 {
						if codehash == "" {
							codehash = parts[0]
						}
						if genesis == "" {
							genesis = parts[1]
						}
					}
				}

				symbol, _ := fieldString(tf, "symbol")
				if symbol == "" {
					symbol = "TOKEN"
				}
				amountDisplay, _ := fieldDecimalString(tf, "amountDisplay")
				if amountDisplay == "" {
					amountDisplay = bi.String()
				}
				recvAddr, ok := fieldString(tf, "receiveAddress")
				if !ok {
					recvAddr, _ = fieldString(tf, "sellerReceiveAddress")
				}
				if recvAddr == "" {
					return UnlockRules{}, fmt.Errorf("unlockRules.tokenPrices[%d].receiveAddress required", i)
				}

				if _, dup := seen[assetKey]; dup {
					continue
				}
				seen[assetKey] = struct{}{}
				out.TokenPrices = append(out.TokenPrices, TokenPriceRule{
					AssetKey:       assetKey,
					Chain:          chain,
					Standard:       standard,
					Mrc20Id:        mrc20Id,
					Codehash:       codehash,
					Genesis:        genesis,
					Symbol:         symbol,
					Decimals:       int(decimals),
					AmountBase:     bi.String(),
					AmountDisplay:  amountDisplay,
					ReceiveAddress: recvAddr,
				})
			}
		}
	}

	if vipRaw, ok := fieldLookup(sub, "vipMrc721Collections"); ok {
		trimmed := bytes.TrimSpace(vipRaw)
		if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
			var entries []json.RawMessage
			if err := json.Unmarshal(trimmed, &entries); err != nil {
				return UnlockRules{}, fmt.Errorf("unlockRules.vipMrc721Collections must be an array")
			}
			seenVip := make(map[string]struct{}, len(entries))
			for i, entry := range entries {
				var vf map[string]json.RawMessage
				if err := json.Unmarshal(entry, &vf); err != nil {
					return UnlockRules{}, fmt.Errorf("unlockRules.vipMrc721Collections[%d] must be an object", i)
				}
				colPinId, ok := fieldString(vf, "collectionPinId")
				if !ok {
					return UnlockRules{}, fmt.Errorf("unlockRules.vipMrc721Collections[%d].collectionPinId required", i)
				}
				colPinId = strings.ToLower(colPinId)
				if _, dup := seenVip[colPinId]; dup {
					continue
				}
				seenVip[colPinId] = struct{}{}
				colName, _ := fieldString(vf, "collectionName")
				if colName == "" {
					colName = "VIP Member Card"
				}
				out.VipMrc721Collections = append(out.VipMrc721Collections, VipCollectionRule{
					CollectionPinId: colPinId,
					CollectionName:  colName,
				})
			}
		}
	}

	if out.MePriceMilli == nil && len(out.TokenPrices) == 0 && len(out.VipMrc721Collections) == 0 {
		return UnlockRules{}, fmt.Errorf("unlockRules must specify at least one unlock option")
	}
	return out, nil
}

func normalizeAssetKey(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func decodeJSONObject(raw []byte) (map[string]json.RawMessage, error) {
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
	for _, cand := range candidates {
		if len(cand) == 0 || cand[0] != '{' {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(cand))
		dec.UseNumber()
		fields := make(map[string]json.RawMessage)
		if err := dec.Decode(&fields); err == nil {
			return fields, nil
		}
	}
	return nil, fmt.Errorf("payload is not a JSON object")
}

func fieldLookup(fields map[string]json.RawMessage, key string) (json.RawMessage, bool) {
	if raw, ok := fields[key]; ok {
		return raw, true
	}
	lower := strings.ToLower(key)
	folded := strings.ReplaceAll(lower, "_", "")
	for k, raw := range fields {
		kl := strings.ToLower(k)
		if kl == lower || strings.ReplaceAll(kl, "_", "") == folded {
			return raw, true
		}
	}
	return nil, false
}

func fieldString(fields map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := fieldLookup(fields, key)
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

func fieldDecimalString(fields map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := fieldLookup(fields, key)
	if !ok {
		return "", false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.TrimSpace(text)
		return text, text != ""
	}
	var num json.Number
	if err := json.Unmarshal(raw, &num); err == nil {
		text = strings.TrimSpace(string(num))
		return text, text != ""
	}
	return "", false
}

func fieldInt(fields map[string]json.RawMessage, key string) (int64, bool) {
	raw, ok := fieldLookup(fields, key)
	if !ok {
		return 0, false
	}
	var num json.Number
	if err := json.Unmarshal(raw, &num); err != nil {
		return 0, false
	}
	if val, err := strconv.ParseInt(string(num), 10, 64); err == nil {
		return val, true
	}
	if fval, err := strconv.ParseFloat(string(num), 64); err == nil {
		return int64(fval), true
	}
	return 0, false
}
