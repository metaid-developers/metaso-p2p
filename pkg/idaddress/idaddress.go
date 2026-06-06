// Package idaddress provides GlobalMetaId encoding/decoding using the ID Address format.
// It converts chain-specific addresses (BTC, MVC, DOGE) to a unified "id"-prefixed
// format compatible with the MetaID protocol.
//
// The ID Address format is: id<version-char>1<data><checksum>
// - HRP: "id"
// - Version char: one of q/p/z/r/y/t (P2PKH/P2SH/P2WPKH/P2WSH/P2MS/P2TR)
// - Separator: "1"
// - Data: base32-encoded address payload
// - Checksum: 6-character BCH-based checksum
//
// This package ports the essential encoding logic from show-now-tmp/idaddress/,
// adapted for the metaso-p2p Go 1.26 project architecture.
package idaddress

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// HRP is the human-readable part of the ID address.
	HRP = "id"

	// Separator separates the HRP+version from the data.
	Separator = "1"

	// ChecksumLength is the BCH checksum length in base32 characters.
	ChecksumLength = 6
)

// AddressVersion defines the address type.
type AddressVersion byte

const (
	VersionP2PKH  AddressVersion = 0 // Pay-to-PubKey-Hash
	VersionP2SH   AddressVersion = 1 // Pay-to-Script-Hash
	VersionP2WPKH AddressVersion = 2 // Pay-to-Witness-PubKey-Hash
	VersionP2WSH  AddressVersion = 3 // Pay-to-Witness-Script-Hash
	VersionP2MS   AddressVersion = 4 // Pay-to-Multisig
	VersionP2TR   AddressVersion = 5 // Pay-to-Taproot (Schnorr)
)

// VersionChar maps address version to its base32 character.
var VersionChar = map[AddressVersion]string{
	VersionP2PKH:  "q",
	VersionP2SH:   "p",
	VersionP2WPKH: "z",
	VersionP2WSH:  "r",
	VersionP2MS:   "y",
	VersionP2TR:   "t",
}

// CharVersion maps base32 character back to address version.
var CharVersion = map[string]AddressVersion{
	"q": VersionP2PKH,
	"p": VersionP2SH,
	"z": VersionP2WPKH,
	"r": VersionP2WSH,
	"y": VersionP2MS,
	"t": VersionP2TR,
}

// Charset is the base32 character set used for encoding.
const Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

// CharsetMap maps each character to its base32 value.
var CharsetMap map[byte]int

func init() {
	CharsetMap = make(map[byte]int)
	for i := 0; i < len(Charset); i++ {
		CharsetMap[Charset[i]] = i
	}
}

// AddressInfo holds decoded ID address information.
type AddressInfo struct {
	Version AddressVersion
	Data    []byte
	Address string
}

// EncodeIDAddress encodes an address version and data into an ID address string.
func EncodeIDAddress(version AddressVersion, data []byte) (string, error) {
	versionChar, ok := VersionChar[version]
	if !ok {
		return "", fmt.Errorf("invalid version: %d", version)
	}

	if err := validateDataLength(version, data); err != nil {
		return "", err
	}

	// Convert 8-bit bytes to 5-bit values for base32 encoding.
	converted, err := convertBits(data, 8, 5, true)
	if err != nil {
		return "", err
	}

	// Compute checksum over HRP+version and data.
	hrpWithVersion := HRP + versionChar
	hrpExpand := hrpExpand(hrpWithVersion)
	checksum := createChecksum(hrpExpand, converted)

	// Combine data + checksum and encode to base32.
	combined := append(converted, checksum...)
	encoded := make([]byte, len(combined))
	for i, val := range combined {
		encoded[i] = Charset[val]
	}

	return hrpWithVersion + Separator + string(encoded), nil
}

// DecodeIDAddress decodes an ID address string into AddressInfo.
func DecodeIDAddress(addr string) (*AddressInfo, error) {
	addr = strings.ToLower(addr)

	if !strings.HasPrefix(addr, HRP) {
		return nil, errors.New("invalid HRP: must start with 'id'")
	}

	sepIndex := strings.LastIndex(addr, Separator)
	if sepIndex == -1 {
		return nil, errors.New("separator not found")
	}

	if sepIndex < len(HRP)+1 {
		return nil, errors.New("invalid address format")
	}

	versionChar := addr[len(HRP):sepIndex]
	version, ok := CharVersion[versionChar]
	if !ok {
		return nil, fmt.Errorf("invalid version character: %s", versionChar)
	}

	dataStr := addr[sepIndex+1:]
	if len(dataStr) < ChecksumLength {
		return nil, errors.New("address too short")
	}

	// Decode base32.
	decoded := make([]int, len(dataStr))
	for i, c := range dataStr {
		val, ok := CharsetMap[byte(c)]
		if !ok {
			return nil, fmt.Errorf("invalid character: %c", c)
		}
		decoded[i] = val
	}

	// Verify checksum.
	hrpWithVersion := HRP + versionChar
	hrpExpand := hrpExpand(hrpWithVersion)
	if !verifyChecksum(hrpExpand, decoded) {
		return nil, errors.New("checksum verification failed")
	}

	// Remove checksum and convert 5-bit back to 8-bit.
	data := decoded[:len(decoded)-ChecksumLength]
	convertedInts, err := convertBits(data, 5, 8, false)
	if err != nil {
		return nil, err
	}
	converted := intSliceToBytes(convertedInts)

	if err := validateDataLength(version, converted); err != nil {
		return nil, err
	}

	return &AddressInfo{
		Version: version,
		Data:    converted,
		Address: addr,
	}, nil
}

// ValidateIDAddress returns true if the address is a valid ID address.
func ValidateIDAddress(addr string) bool {
	_, err := DecodeIDAddress(addr)
	return err == nil
}

// GetAddressType returns a human-readable description of the address version.
func GetAddressType(version AddressVersion) string {
	switch version {
	case VersionP2PKH:
		return "Pay-to-PubKey-Hash"
	case VersionP2SH:
		return "Pay-to-Script-Hash"
	case VersionP2WPKH:
		return "Pay-to-Witness-PubKey-Hash"
	case VersionP2WSH:
		return "Pay-to-Witness-Script-Hash"
	case VersionP2MS:
		return "Pay-to-Multisig"
	case VersionP2TR:
		return "Pay-to-Taproot"
	default:
		return "Unknown"
	}
}

// EncodeGlobalMetaId encodes a chain address into a GlobalMetaId (ID address format).
// metaid is the user's blockchain address (BTC address, DOGE address, etc.).
// chainName is the chain identifier ("btc", "mvc", "doge").
func EncodeGlobalMetaId(metaid string, chainName string) string {
	if metaid == "" || metaid == "errorAddr" {
		return ""
	}

	// Use the address converter to produce the ID address format.
	idAddr, err := ConvertFromBitcoin(metaid)
	if err != nil {
		// If chain address cannot be parsed, produce a fallback format.
		// This should not happen in normal operation.
		return ""
	}
	return idAddr
}

// DecodeGlobalMetaId decodes a GlobalMetaId back to the original chain address.
// Returns the chain address (metaid), chain name, and any error.
func DecodeGlobalMetaId(globalMetaId string) (metaid string, chainName string, err error) {
	if globalMetaId == "" {
		return "", "", fmt.Errorf("empty globalMetaId")
	}

	// The GlobalMetaId is an ID address. Try to convert to known chain formats.
	// Try BTC mainnet first.
	addr, err := ConvertToBitcoin(globalMetaId, "mainnet")
	if err == nil {
		return addr, "btc", nil
	}

	// Try BTC testnet.
	addr, err = ConvertToBitcoin(globalMetaId, "testnet")
	if err == nil {
		return addr, "btc", nil
	}

	// Try Dogecoin.
	addr, err = ConvertToDogecoin(globalMetaId)
	if err == nil {
		return addr, "doge", nil
	}

	return "", "", fmt.Errorf("unsupported globalMetaId format: %s", globalMetaId)
}

// --- Internal helpers: base32, checksum, bit conversion ---

// convertBits converts data between bit widths.
func convertBits(data interface{}, fromBits, toBits int, pad bool) ([]int, error) {
	var byteData []byte
	switch v := data.(type) {
	case []byte:
		byteData = v
	case []int:
		byteData = intSliceToBytes(v)
	default:
		return nil, errors.New("invalid data type")
	}

	acc := 0
	bits := 0
	ret := []int{}
	maxv := (1 << toBits) - 1
	maxAcc := (1 << (fromBits + toBits - 1)) - 1

	for _, value := range byteData {
		if int(value) < 0 || int(value)>>fromBits != 0 {
			return nil, errors.New("invalid data value")
		}
		acc = ((acc << fromBits) | int(value)) & maxAcc
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			ret = append(ret, (acc>>bits)&maxv)
		}
	}

	if pad {
		if bits > 0 {
			ret = append(ret, (acc<<(toBits-bits))&maxv)
		}
	} else if bits >= fromBits || ((acc<<(toBits-bits))&maxv) != 0 {
		return nil, errors.New("invalid padding")
	}

	return ret, nil
}

// hrpExpand expands the HRP for checksum computation.
func hrpExpand(hrp string) []int {
	ret := make([]int, len(hrp)*2+1)
	for i, c := range hrp {
		ret[i] = int(c) >> 5
		ret[i+len(hrp)+1] = int(c) & 31
	}
	ret[len(hrp)] = 0
	return ret
}

// polymod computes the BCH polynomial modulus.
func polymod(values []int) int {
	gen := []int{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := 1
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ v
		for i := 0; i < 5; i++ {
			if (top>>i)&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

// createChecksum creates a 6-value checksum for the given HRP expansion and data.
func createChecksum(hrpExpand []int, data []int) []int {
	values := append(hrpExpand, data...)
	values = append(values, []int{0, 0, 0, 0, 0, 0}...)
	mod := polymod(values) ^ 1
	ret := make([]int, 6)
	for i := 0; i < 6; i++ {
		ret[i] = (mod >> (5 * (5 - i))) & 31
	}
	return ret
}

// verifyChecksum returns true if the checksum embedded in the data matches.
func verifyChecksum(hrpExpand []int, data []int) bool {
	values := append(hrpExpand, data...)
	return polymod(values) == 1
}

// validateDataLength checks that the data length matches the address version expectation.
func validateDataLength(version AddressVersion, data []byte) error {
	switch version {
	case VersionP2PKH, VersionP2SH, VersionP2WPKH:
		if len(data) != 20 {
			return fmt.Errorf("invalid data length for version %d: expected 20, got %d", version, len(data))
		}
	case VersionP2WSH:
		if len(data) != 32 {
			return fmt.Errorf("invalid data length for version %d: expected 32, got %d", version, len(data))
		}
	case VersionP2TR:
		if len(data) != 32 {
			return fmt.Errorf("invalid data length for version %d: expected 32, got %d", version, len(data))
		}
	case VersionP2MS:
		if len(data) < 2 {
			return errors.New("invalid multisig data: too short")
		}
		m := int(data[0])
		n := int(data[1])
		if m == 0 || n == 0 || m > n || n > 15 {
			return fmt.Errorf("invalid multisig parameters: m=%d, n=%d", m, n)
		}
		expectedLen := 2 + n*33
		if len(data) != expectedLen {
			return fmt.Errorf("invalid multisig data length: expected %d, got %d", expectedLen, len(data))
		}
	default:
		return fmt.Errorf("unknown version: %d", version)
	}
	return nil
}

// intSliceToBytes converts a slice of ints to a slice of bytes.
func intSliceToBytes(data []int) []byte {
	ret := make([]byte, len(data))
	for i, v := range data {
		ret[i] = byte(v)
	}
	return ret
}
