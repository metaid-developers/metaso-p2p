package social

import (
	"encoding/hex"
	"strings"

	"github.com/metaid-developers/metaso-p2p/pkg/idaddress"
)

// normalizeIdentity is the single dual-identifier normalization pipeline every
// ingestion path and every new-endpoint query input flows through
// (spec 2026-10-29 §2.3):
//
//  1. IDAddress (id-prefixed, checksum passes) -> canonical = input.
//  2. Legacy 64-hex metaid -> canonical = GlobalMetaID resolved through the
//     indexed profile lookup. A miss is an unresolved reference (§2.5).
//  3. Chain address -> canonical = GlobalMetaID converted by the IDAddress
//     algorithm.
//  4. Anything else -> not ok.
//
// A profile-lookup dependency failure is returned wrapped in ErrUnavailable so
// callers can answer 50000 instead of misreporting a live identity as missing.
func (a *Aggregator) normalizeIdentity(input string) (canonical string, ok bool, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", false, nil
	}

	// Step 1: IDAddress form (checksum-validated, case-insensitive).
	if candidate := strings.ToLower(input); idaddress.ValidateIDAddress(candidate) {
		return candidate, true, nil
	}

	// Step 2: legacy 64-hex metaid resolved through the profile archive.
	if isLegacyHexMetaId(input) {
		if a == nil || a.profileLookup == nil {
			return "", false, nil
		}
		ref, lookupErr := a.profileLookup.LookupByMetaId(strings.ToLower(input))
		if lookupErr != nil {
			return "", false, unavailableWrap(lookupErr)
		}
		if ref == nil {
			return "", false, nil
		}
		if globalMetaId := strings.TrimSpace(ref.GlobalMetaId); globalMetaId != "" {
			return globalMetaId, true, nil
		}
		return "", false, nil
	}

	// Step 3: chain address converted by the IDAddress algorithm.
	if converted, convErr := idaddress.ConvertFromBitcoin(input); convErr == nil && converted != "" {
		return converted, true, nil
	}

	return "", false, nil
}

// isLegacyHexMetaId reports whether input is a legacy metaid: exactly 64
// lowercase/uppercase hex characters.
func isLegacyHexMetaId(input string) bool {
	if len(input) != 64 {
		return false
	}
	_, err := hex.DecodeString(input)
	return err == nil
}
