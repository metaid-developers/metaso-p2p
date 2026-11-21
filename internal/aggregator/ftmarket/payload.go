package ftmarket

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

var (
	hex64Pattern    = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	outpointPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}:[0-9]+$`)
	mvcAddrPattern  = regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{20,}$`)
	btcAddrPattern  = regexp.MustCompile(`^(?:[13][a-km-zA-HJ-NP-Z1-9]{25,34}|(?:bc1|tb1|bcrt1)[a-zA-HJ-NP-Z0-9]{11,71})$`)
	mrc20IdPattern  = regexp.MustCompile(`^[0-9a-fA-F]{64}i[0-9]+$`)
	hex40Pattern    = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
)

type sellPayload struct {
	Active           bool
	ListingKey       string
	Asset            *AssetInfo
	Lot              *LotInfo
	SellerAddress    string
	SellerBtcAddress string
	SellerMetaId     string
	Rails            *SellRails
	AcceptedRails    []string
	BatchGroupId     string
	BatchIndex       int
	BatchTotal       int
	Note             string
	CreatedAt        int64
	ExpiresAt        int64
}

// parseSellPayload parses and strictly validates a /protocols/ft-sell v1.0 payload,
// including Contract C1.3 mutual exclusion between mvcTokenSell and me rails.
func parseSellPayload(raw []byte) (*sellPayload, error) {
	fields, err := decodePayloadObject(raw)
	if err != nil {
		return nil, err
	}

	p := &sellPayload{}
	rawActive, ok := fields["active"]
	if !ok {
		return nil, fmt.Errorf("missing active")
	}
	if p.Active, ok = parseBoolJSON(rawActive); !ok {
		return nil, fmt.Errorf("active is not a boolean")
	}

	if p.ListingKey, ok = getStringField(fields, "listingKey"); !ok {
		return nil, fmt.Errorf("missing listingKey")
	}
	p.ListingKey = strings.TrimSpace(p.ListingKey)

	var sellerObj struct {
		Address       string `json:"address"`
		BotMvcAddress string `json:"botMvcAddress"`
		BotBtcAddress string `json:"botBtcAddress"`
		BtcAddress    string `json:"btcAddress"`
		GlobalMetaId  string `json:"globalMetaId"`
		MetaId        string `json:"metaId"`
	}
	if rawSeller, hasSeller := fields["seller"]; hasSeller && !isJSONNull(rawSeller) {
		_ = json.Unmarshal(rawSeller, &sellerObj)
	}

	p.SellerAddress, _ = getStringField(fields, "sellerAddress")
	if p.SellerAddress == "" {
		p.SellerAddress = strings.TrimSpace(firstNonEmpty(sellerObj.BotMvcAddress, sellerObj.Address))
	}
	if p.SellerAddress == "" {
		return nil, fmt.Errorf("missing sellerAddress")
	}
	if !isValidChainAddress(p.SellerAddress) {
		return nil, fmt.Errorf("invalid sellerAddress %q", p.SellerAddress)
	}
	p.SellerBtcAddress, _ = getStringField(fields, "sellerBtcAddress")
	if p.SellerBtcAddress == "" {
		p.SellerBtcAddress = strings.TrimSpace(firstNonEmpty(sellerObj.BotBtcAddress, sellerObj.BtcAddress))
	}
	if p.SellerBtcAddress != "" && !isValidChainAddress(p.SellerBtcAddress) {
		return nil, fmt.Errorf("invalid sellerBtcAddress %q", p.SellerBtcAddress)
	}
	p.SellerMetaId, _ = getStringField(fields, "sellerMetaId")
	if p.SellerMetaId == "" {
		p.SellerMetaId = strings.TrimSpace(firstNonEmpty(sellerObj.GlobalMetaId, sellerObj.MetaId))
	}
	p.BatchGroupId, _ = getStringField(fields, "batchGroupId")
	p.BatchIndex, _ = getIntField(fields, "batchIndex")
	p.BatchTotal, _ = getIntField(fields, "batchTotal")
	p.Note, _ = getStringField(fields, "note")
	p.CreatedAt, _ = getInt64Field(fields, "createdAt")
	p.ExpiresAt, _ = getInt64Field(fields, "expiresAt")
	p.CreatedAt = unixSeconds(p.CreatedAt)
	p.ExpiresAt = unixSeconds(p.ExpiresAt)
	p.AcceptedRails = getStringSliceField(fields, "acceptedRails")

	// Cancellation payload only requires active=false, listingKey, sellerAddress.
	if !p.Active {
		if rawAsset, hasAsset := fields["asset"]; hasAsset && !isJSONNull(rawAsset) {
			if asset, aErr := parseAssetInfo(rawAsset); aErr == nil {
				p.Asset = asset
			}
		}
		if rawLot, hasLot := fields["lot"]; hasLot && !isJSONNull(rawLot) {
			if lot, lErr := parseLotInfo(rawLot, 0, false); lErr == nil {
				p.Lot = lot
			}
		}
		if p.Asset != nil && p.Lot != nil {
			p.ListingKey = canonicalizeListingKey(p.ListingKey, p.Asset.AssetKey, p.Lot.UtxoOutpoint)
		} else {
			p.ListingKey = strings.ToLower(p.ListingKey)
		}
		return p, nil
	}

	rawAsset, hasAsset := fields["asset"]
	if !hasAsset || isJSONNull(rawAsset) {
		return nil, fmt.Errorf("missing asset")
	}
	asset, err := parseAssetInfo(rawAsset)
	if err != nil {
		return nil, fmt.Errorf("invalid asset: %w", err)
	}
	p.Asset = asset

	rawLot, hasLot := fields["lot"]
	if !hasLot || isJSONNull(rawLot) {
		return nil, fmt.Errorf("missing lot")
	}
	lot, err := parseLotInfo(rawLot, asset.Decimals, true)
	if err != nil {
		return nil, fmt.Errorf("invalid lot: %w", err)
	}
	p.Lot = lot

	rawRails, hasRails := fields["rails"]
	if !hasRails || isJSONNull(rawRails) {
		return nil, fmt.Errorf("missing rails")
	}
	rails, err := parseSellRails(rawRails, asset.Chain)
	if err != nil {
		return nil, fmt.Errorf("invalid rails: %w", err)
	}
	p.Rails = rails

	// Enforce consistency between rails.mvcTokenSell tokenList outpoint and lot.utxoOutpoint.
	if rails.MvcTokenSell != nil && rails.MvcTokenSell.TokenListTxid != "" && hex64Pattern.MatchString(rails.MvcTokenSell.TokenListTxid) {
		tokenListOutpoint := fmt.Sprintf("%s:%d", strings.ToLower(rails.MvcTokenSell.TokenListTxid), rails.MvcTokenSell.TokenListOutputIndex)
		if !strings.EqualFold(lot.UtxoOutpoint, tokenListOutpoint) {
			return nil, fmt.Errorf("lot.utxoOutpoint %q does not match rails.mvcTokenSell tokenList outpoint %q", lot.UtxoOutpoint, tokenListOutpoint)
		}
	}

	if len(p.AcceptedRails) == 0 {
		p.AcceptedRails = deriveAcceptedRails(rails)
	} else {
		p.AcceptedRails = normalizeAcceptedRails(p.AcceptedRails)
	}

	// Normalize listingKey lowercase for the assetKey prefix if formatted cleanly.
	p.ListingKey = canonicalizeListingKey(p.ListingKey, asset.AssetKey, lot.UtxoOutpoint)

	return p, nil
}

func parseAssetInfo(raw json.RawMessage) (*AssetInfo, error) {
	var in struct {
		AssetKey  string  `json:"assetKey"`
		Chain     string  `json:"chain"`
		Standard  string  `json:"standard"`
		Codehash  *string `json:"codehash"`
		Genesis   *string `json:"genesis"`
		Mrc20Id   *string `json:"mrc20Id"`
		Symbol    string  `json:"symbol"`
		TokenName string  `json:"tokenName"`
		Decimals  *int    `json:"decimals"`
		Icon      string  `json:"icon"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}

	chain := strings.ToLower(strings.TrimSpace(in.Chain))
	standard := strings.ToLower(strings.TrimSpace(in.Standard))
	if standard == "mvc-ft" {
		standard = "metacontract-ft"
	}
	if chain == "" {
		switch standard {
		case "mrc20":
			chain = "btc"
		case "metacontract-ft":
			chain = "mvc"
		}
	}
	if chain != "btc" && chain != "mvc" {
		return nil, fmt.Errorf("unsupported chain %q", in.Chain)
	}
	if standard == "" {
		if chain == "btc" {
			standard = "mrc20"
		} else {
			standard = "metacontract-ft"
		}
	}

	var codehashPtr, genesisPtr, mrc20IdPtr *string
	var canonicalKey string
	switch chain {
	case "btc":
		if standard != "mrc20" {
			return nil, fmt.Errorf("btc asset must use mrc20 standard, got %q", standard)
		}
		mrc20Id := ""
		if in.Mrc20Id != nil {
			mrc20Id = strings.TrimSpace(*in.Mrc20Id)
		}
		if mrc20Id == "" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(in.AssetKey)), "mrc20:") {
			mrc20Id = strings.TrimSpace(in.AssetKey[len("mrc20:"):])
		}
		if mrc20Id == "" {
			return nil, fmt.Errorf("missing mrc20Id")
		}
		mrc20Id = normalizeMrc20Id(mrc20Id)
		if !mrc20IdPattern.MatchString(mrc20Id) {
			return nil, fmt.Errorf("invalid mrc20Id %q", mrc20Id)
		}
		canonicalKey = "mrc20:" + mrc20Id
		mrc20IdPtr = &mrc20Id
	case "mvc":
		if standard != "metacontract-ft" {
			return nil, fmt.Errorf("mvc asset must use metacontract-ft standard, got %q", standard)
		}
		codehash := ""
		genesis := ""
		if in.Codehash != nil {
			codehash = strings.ToLower(strings.TrimSpace(*in.Codehash))
		}
		if in.Genesis != nil {
			genesis = strings.ToLower(strings.TrimSpace(*in.Genesis))
		}
		if (codehash == "" || genesis == "") && strings.HasPrefix(strings.ToLower(strings.TrimSpace(in.AssetKey)), "mvc-ft:") {
			parts := strings.Split(strings.TrimSpace(in.AssetKey[len("mvc-ft:"):]), ":")
			if len(parts) == 2 {
				if codehash == "" {
					codehash = strings.ToLower(strings.TrimSpace(parts[0]))
				}
				if genesis == "" {
					genesis = strings.ToLower(strings.TrimSpace(parts[1]))
				}
			}
		}
		if !hex40Pattern.MatchString(codehash) || genesis == "" || !isValidHex(genesis) {
			return nil, fmt.Errorf("invalid mvc codehash/genesis")
		}
		canonicalKey = "mvc-ft:" + codehash + ":" + genesis
		codehashPtr = &codehash
		genesisPtr = &genesis
	}

	if supplied := strings.ToLower(strings.TrimSpace(in.AssetKey)); supplied != "" && supplied != canonicalKey {
		return nil, fmt.Errorf("assetKey %q does not match canonical key %q", in.AssetKey, canonicalKey)
	}

	symbol := strings.TrimSpace(in.Symbol)
	if symbol == "" {
		return nil, fmt.Errorf("missing symbol")
	}
	decimals := 0
	if in.Decimals != nil {
		decimals = *in.Decimals
	}
	if decimals < 0 || decimals > 18 {
		return nil, fmt.Errorf("decimals %d out of range [0,18]", decimals)
	}

	return &AssetInfo{
		AssetKey:  canonicalKey,
		Chain:     chain,
		Standard:  standard,
		Codehash:  codehashPtr,
		Genesis:   genesisPtr,
		Mrc20Id:   mrc20IdPtr,
		Symbol:    symbol,
		TokenName: strings.TrimSpace(in.TokenName),
		Decimals:  decimals,
		Icon:      strings.TrimSpace(in.Icon),
	}, nil
}

func parseLotInfo(raw json.RawMessage, decimals int, requireAll bool) (*LotInfo, error) {
	var in struct {
		AmountBase    string `json:"amountBase"`
		AmountDisplay string `json:"amountDisplay"`
		UtxoOutpoint  string `json:"utxoOutpoint"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	amountBase := strings.TrimSpace(in.AmountBase)
	outpoint := strings.ToLower(strings.TrimSpace(in.UtxoOutpoint))
	if requireAll {
		if amountBase == "" {
			return nil, fmt.Errorf("missing amountBase")
		}
		v, ok := new(big.Int).SetString(amountBase, 10)
		if !ok || v.Sign() <= 0 {
			return nil, fmt.Errorf("amountBase %q must be a positive integer", amountBase)
		}
		amountBase = v.String()
		if !outpointPattern.MatchString(outpoint) {
			return nil, fmt.Errorf("invalid utxoOutpoint %q", in.UtxoOutpoint)
		}
	}
	amountDisplay := strings.TrimSpace(in.AmountDisplay)
	if amountDisplay == "" && amountBase != "" {
		if v, ok := new(big.Int).SetString(amountBase, 10); ok && v.Sign() > 0 {
			amountDisplay = formatBaseAmount(v, decimals)
		}
	}
	return &LotInfo{
		AmountBase:    amountBase,
		AmountDisplay: amountDisplay,
		UtxoOutpoint:  outpoint,
	}, nil
}

func parseSellRails(raw json.RawMessage, chain string) (*SellRails, error) {
	var in struct {
		MvcTokenSell *struct {
			SellTxid             string `json:"sellTxid"`
			Txid                 string `json:"txid"`
			TokenListTxid        string `json:"tokenListTxid"`
			TokenListOutputIndex *int64 `json:"tokenListOutputIndex"`
			PriceSats            int64  `json:"priceSats"`
			SellerAddress        string `json:"sellerAddress"`
			SeedTxid             string `json:"seedTxid"`
			SeedVout             *int64 `json:"seedVout"`
			PrevTokenTxid        string `json:"prevTokenTxid"`
			ContractAddress      string `json:"contractAddress"`
			TokenSellJson        string `json:"tokenSellJson"`
		} `json:"mvcTokenSell"`
		BtcPsbt *BtcPsbtRail `json:"btcPsbt"`
		Me      *struct {
			AmountMilli  int64 `json:"amountMilli"`
			PriceMeMilli int64 `json:"priceMeMilli"`
			AutoFulfill  bool  `json:"autoFulfill"`
		} `json:"me"`
		Fiat *FiatRail `json:"fiat"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}

	var rails SellRails
	rails.BtcPsbt = in.BtcPsbt
	rails.Fiat = in.Fiat

	if in.MvcTokenSell != nil {
		m := &MvcTokenSellRail{
			SellTxid:        strings.ToLower(strings.TrimSpace(firstNonEmpty(in.MvcTokenSell.SellTxid, in.MvcTokenSell.Txid))),
			TokenListTxid:   strings.ToLower(strings.TrimSpace(in.MvcTokenSell.TokenListTxid)),
			PriceSats:       in.MvcTokenSell.PriceSats,
			SellerAddress:   strings.TrimSpace(in.MvcTokenSell.SellerAddress),
			SeedTxid:        strings.ToLower(strings.TrimSpace(in.MvcTokenSell.SeedTxid)),
			PrevTokenTxid:   strings.ToLower(strings.TrimSpace(in.MvcTokenSell.PrevTokenTxid)),
			ContractAddress: strings.TrimSpace(in.MvcTokenSell.ContractAddress),
			TokenSellJson:   strings.TrimSpace(in.MvcTokenSell.TokenSellJson),
		}
		hasOutIdx := false
		if in.MvcTokenSell.TokenListOutputIndex != nil {
			m.TokenListOutputIndex = *in.MvcTokenSell.TokenListOutputIndex
			hasOutIdx = true
		}
		hasSeedVout := false
		if in.MvcTokenSell.SeedVout != nil {
			m.SeedVout = *in.MvcTokenSell.SeedVout
			hasSeedVout = true
		}

		// Fallback extraction from tokenSellJson when explicit fields are omitted.
		if m.TokenSellJson != "" {
			var tsFields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(m.TokenSellJson), &tsFields); err == nil && tsFields != nil {
				if sellTx, ok := getStringField(tsFields, "sell_txid"); ok {
					sellTx = strings.ToLower(sellTx)
					if m.SellTxid == "" {
						m.SellTxid = sellTx
					}
					if m.TokenListTxid == "" {
						m.TokenListTxid = sellTx
					}
				}
				if !hasOutIdx {
					if outIdx, ok := getInt64Field(tsFields, "sell_output_index"); ok {
						m.TokenListOutputIndex = outIdx
					}
				}
				if m.SeedTxid == "" {
					if seedTx, ok := getStringField(tsFields, "seed_txid"); ok {
						m.SeedTxid = strings.ToLower(seedTx)
					}
				}
				if !hasSeedVout {
					if seedVout, ok := getInt64Field(tsFields, "seed_vout"); ok {
						m.SeedVout = seedVout
					}
				}
			}
		}
		rails.MvcTokenSell = m
	}

	if in.Me != nil {
		amtMilli := in.Me.AmountMilli
		if amtMilli == 0 && in.Me.PriceMeMilli > 0 {
			amtMilli = in.Me.PriceMeMilli
		}
		rails.Me = &MeRail{
			AmountMilli: amtMilli,
			AutoFulfill: in.Me.AutoFulfill,
		}
	}

	// Contract C1.3 hard constraint: on the same utxoOutpoint, mvcTokenSell (contract lock rail)
	// and me (wallet direct-transfer rail) MUST NOT both be non-nil.
	if rails.MvcTokenSell != nil && rails.Me != nil {
		return nil, fmt.Errorf("mutual exclusion violation: mvcTokenSell and me rails cannot both be non-null on the same utxoOutpoint")
	}

	if rails.MvcTokenSell != nil {
		if chain != "mvc" {
			return nil, fmt.Errorf("mvcTokenSell rail requires chain=mvc")
		}
		if rails.MvcTokenSell.PriceSats <= 0 {
			return nil, fmt.Errorf("mvcTokenSell.priceSats must be > 0")
		}
		if rails.MvcTokenSell.TokenListOutputIndex < 0 {
			return nil, fmt.Errorf("mvcTokenSell.tokenListOutputIndex must be >= 0")
		}
		if rails.MvcTokenSell.SeedVout < 0 {
			return nil, fmt.Errorf("mvcTokenSell.seedVout must be >= 0")
		}
	}

	if rails.BtcPsbt != nil {
		if chain != "btc" {
			return nil, fmt.Errorf("btcPsbt rail requires chain=btc")
		}
		if rails.BtcPsbt.PriceSats <= 0 {
			return nil, fmt.Errorf("btcPsbt.priceSats must be > 0")
		}
	}

	if rails.Me != nil {
		if rails.Me.AmountMilli <= 0 {
			return nil, fmt.Errorf("me.amountMilli must be > 0")
		}
	}

	if rails.Fiat != nil {
		rails.Fiat.Currency = strings.ToUpper(strings.TrimSpace(rails.Fiat.Currency))
		if rails.Fiat.Currency == "" || rails.Fiat.AmountCents <= 0 {
			return nil, fmt.Errorf("fiat requires currency and amountCents > 0")
		}
	}

	if rails.MvcTokenSell == nil && rails.BtcPsbt == nil && rails.Me == nil && rails.Fiat == nil {
		return nil, fmt.Errorf("at least one settlement rail must be specified")
	}
	return &rails, nil
}

func deriveAcceptedRails(r *SellRails) []string {
	if r == nil {
		return []string{}
	}
	var out []string
	if r.MvcTokenSell != nil {
		out = append(out, "mvc-tokensell")
	}
	if r.BtcPsbt != nil {
		out = append(out, "btc-psbt")
	}
	if r.Me != nil {
		out = append(out, "me-escrow")
	}
	if r.Fiat != nil {
		out = append(out, "fiat-escrow")
	}
	if out == nil {
		return []string{}
	}
	return out
}

func normalizeAcceptedRails(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		v := strings.ToLower(strings.TrimSpace(raw))
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func canonicalizeListingKey(listingKey, assetKey, outpoint string) string {
	trimmed := strings.TrimSpace(listingKey)
	if trimmed == "" {
		return assetKey + ":" + outpoint
	}
	// If it starts with assetKey case-insensitively, normalize the prefix and outpoint hex.
	if strings.HasPrefix(strings.ToLower(trimmed), assetKey+":") {
		suffix := strings.TrimSpace(trimmed[len(assetKey)+1:])
		if outpointPattern.MatchString(suffix) {
			suffix = strings.ToLower(suffix)
		}
		return assetKey + ":" + suffix
	}
	return strings.ToLower(trimmed)
}

func normalizeMrc20Id(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 65 {
		if idx := strings.IndexAny(id, "iI"); idx == 64 {
			return strings.ToLower(id[:64]) + "i" + id[65:]
		}
	}
	return strings.ToLower(id)
}

func isValidHex(s string) bool {
	if len(s) == 0 || len(s)%2 != 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func isValidChainAddress(addr string) bool {
	addr = strings.TrimSpace(addr)
	return mvcAddrPattern.MatchString(addr) || btcAddrPattern.MatchString(addr)
}

func decodePayloadObject(raw []byte) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty payload")
	}
	candidates := [][]byte{trimmed}
	var quoted string
	if err := json.Unmarshal(trimmed, &quoted); err == nil {
		if inner := bytes.TrimSpace([]byte(quoted)); len(inner) > 0 {
			candidates = append(candidates, inner)
		}
	}
	if decoded, err := base64.StdEncoding.DecodeString(string(trimmed)); err == nil {
		candidates = append(candidates, bytes.TrimSpace(decoded))
	} else if decoded, err := base64.RawStdEncoding.DecodeString(string(trimmed)); err == nil {
		candidates = append(candidates, bytes.TrimSpace(decoded))
	}
	for _, c := range candidates {
		if len(c) == 0 || c[0] != '{' {
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(c, &obj); err == nil && obj != nil {
			return obj, nil
		}
	}
	return nil, fmt.Errorf("payload is not a JSON object")
}

func isJSONNull(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func getStringField(fields map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := fields[key]
	if !ok || isJSONNull(raw) {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	return s, true
}

func getIntField(fields map[string]json.RawMessage, key string) (int, bool) {
	v, ok := getInt64Field(fields, key)
	return int(v), ok
}

func getInt64Field(fields map[string]json.RawMessage, key string) (int64, bool) {
	raw, ok := fields[key]
	if !ok || isJSONNull(raw) {
		return 0, false
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		s = strings.TrimSpace(s)
		if bi, ok := new(big.Int).SetString(s, 10); ok && bi.IsInt64() {
			return bi.Int64(), true
		}
	}
	return 0, false
}

func parseBoolJSON(raw json.RawMessage) (bool, bool) {
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

func getStringSliceField(fields map[string]json.RawMessage, key string) []string {
	raw, ok := fields[key]
	if !ok || isJSONNull(raw) {
		return nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	return nil
}
