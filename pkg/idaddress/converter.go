package idaddress

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/btcutil/base58"
)

// --- Bech32 decoding (needed by ConvertFromBitcoin) ---

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

var bech32CharsetMap = map[rune]int{
	'q': 0, 'p': 1, 'z': 2, 'r': 3, 'y': 4, '9': 5, 'x': 6, '8': 7,
	'g': 8, 'f': 9, '2': 10, 't': 11, 'v': 12, 'd': 13, 'w': 14, '0': 15,
	's': 16, '3': 17, 'j': 18, 'n': 19, '5': 20, '4': 21, 'k': 22, 'h': 23,
	'c': 24, 'e': 25, '6': 26, 'm': 27, 'u': 28, 'a': 29, '7': 30, 'l': 31,
}

// Bech32Encoding distinguishes between Bech32 and Bech32m.
type Bech32Encoding int

const (
	Bech32  Bech32Encoding = 1
	Bech32m Bech32Encoding = 2
)

func bech32HrpExpand(hrp string) []int {
	result := make([]int, 0, len(hrp)*2+1)
	for _, c := range hrp {
		result = append(result, int(c>>5))
	}
	result = append(result, 0)
	for _, c := range hrp {
		result = append(result, int(c&31))
	}
	return result
}

func bech32VerifyChecksum(hrp string, data []int, encoding Bech32Encoding) bool {
	values := append(bech32HrpExpand(hrp), data...)
	const bech32Const = 1
	const bech32mConst = 0x2bc830a3

	polymod := polymod(values)
	if encoding == Bech32 {
		return polymod == bech32Const
	}
	return polymod == bech32mConst
}

func convertBits8to5(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	acc := 0
	bits := uint(0)
	ret := make([]byte, 0, len(data)*int(fromBits)/int(toBits)+1)
	maxv := (1 << toBits) - 1

	for _, value := range data {
		acc = (acc << fromBits) | int(value)
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			ret = append(ret, byte((acc>>bits)&maxv))
		}
	}

	if pad {
		if bits > 0 {
			ret = append(ret, byte((acc<<(toBits-bits))&maxv))
		}
	} else if bits >= fromBits || ((acc<<(toBits-bits))&maxv) != 0 {
		return nil, errors.New("invalid padding")
	}

	return ret, nil
}

// Bech32Decode decodes a Bech32/Bech32m address string.
func Bech32Decode(addr string) (hrp string, version byte, program []byte, encoding Bech32Encoding, err error) {
	addr = strings.ToLower(addr)

	pos := strings.LastIndex(addr, "1")
	if pos < 1 || pos+7 > len(addr) || len(addr) > 90 {
		return "", 0, nil, 0, errors.New("invalid bech32 address format")
	}

	hrp = addr[:pos]
	data := addr[pos+1:]

	decoded := make([]int, 0, len(data))
	for _, c := range data {
		val, ok := bech32CharsetMap[c]
		if !ok {
			return "", 0, nil, 0, errors.New("invalid bech32 character")
		}
		decoded = append(decoded, val)
	}

	// Try Bech32m first, then Bech32.
	encoding = Bech32m
	if !bech32VerifyChecksum(hrp, decoded, Bech32m) {
		encoding = Bech32
		if !bech32VerifyChecksum(hrp, decoded, Bech32) {
			return "", 0, nil, 0, errors.New("invalid bech32 checksum")
		}
	}

	decoded = decoded[:len(decoded)-6]
	if len(decoded) < 1 {
		return "", 0, nil, 0, errors.New("invalid bech32 data length")
	}

	version = byte(decoded[0])

	// Convert remaining data from 5-bit to 8-bit.
	converted, err := convertBits8to5(toByteArray(decoded[1:]), 5, 8, false)
	if err != nil {
		return "", 0, nil, 0, err
	}

	program = converted

	if len(program) < 2 || len(program) > 40 {
		return "", 0, nil, 0, errors.New("invalid witness program length")
	}

	if version == 0 && encoding != Bech32 {
		return "", 0, nil, 0, errors.New("witness version 0 must use bech32")
	}
	if version != 0 && encoding != Bech32m {
		return "", 0, nil, 0, errors.New("witness version 1+ must use bech32m")
	}

	return hrp, version, program, encoding, nil
}

func toByteArray(ints []int) []byte {
	bytes := make([]byte, len(ints))
	for i, v := range ints {
		bytes[i] = byte(v)
	}
	return bytes
}

// --- Base58 encoding/decoding ---

func doubleSHA256(data []byte) []byte {
	first := sha256.Sum256(data)
	second := sha256.Sum256(first[:])
	return second[:]
}

// Base58Decode decodes a Base58 string into raw bytes.
func Base58Decode(input string) ([]byte, error) {
	decoded := base58.Decode(input)
	if len(decoded) == 0 && len(input) > 0 {
		return nil, errors.New("invalid base58 string")
	}
	return decoded, nil
}

// Base58CheckDecode decodes a Base58Check encoded string.
func Base58CheckDecode(input string) (version byte, payload []byte, err error) {
	decoded, err := Base58Decode(input)
	if err != nil {
		return 0, nil, err
	}

	if len(decoded) < 5 {
		return 0, nil, errors.New("decoded data too short")
	}

	data := decoded[:len(decoded)-4]
	checksum := decoded[len(decoded)-4:]

	expectedChecksum := doubleSHA256(data)
	for i := 0; i < 4; i++ {
		if checksum[i] != expectedChecksum[i] {
			return 0, nil, errors.New("checksum mismatch")
		}
	}

	return data[0], data[1:], nil
}

// Base58CheckEncode encodes data with version byte using Base58Check.
func Base58CheckEncode(version byte, payload []byte) string {
	data := make([]byte, 1+len(payload))
	data[0] = version
	copy(data[1:], payload)

	checksum := doubleSHA256(data)

	result := make([]byte, len(data)+4)
	copy(result, data)
	copy(result[len(data):], checksum[:4])

	return base58.Encode(result)
}

// --- Bech32 encoding (needed by ConvertToBitcoin) ---

// Bech32Encode encodes data in Bech32/Bech32m format.
func Bech32Encode(hrp string, version byte, program []byte) (string, error) {
	encoding := Bech32
	if version != 0 {
		encoding = Bech32m
	}

	if len(program) < 2 || len(program) > 40 {
		return "", errors.New("invalid witness program length")
	}

	converted, err := convertBits8to5(program, 8, 5, true)
	if err != nil {
		return "", err
	}

	data := make([]int, 0, len(converted)+1)
	data = append(data, int(version))
	for _, b := range converted {
		data = append(data, int(b))
	}

	values := append(bech32HrpExpand(hrp), data...)
	values = append(values, 0, 0, 0, 0, 0, 0)

	const bech32Const = 1
	const bech32mConst = 0x2bc830a3

	poly := polymod(values)
	if encoding == Bech32 {
		poly ^= bech32Const
	} else {
		poly ^= bech32mConst
	}

	checksum := make([]int, 6)
	for i := 0; i < 6; i++ {
		checksum[i] = (poly >> (5 * (5 - i))) & 31
	}

	data = append(data, checksum...)
	var result strings.Builder
	result.WriteString(hrp)
	result.WriteRune('1')
	for _, d := range data {
		result.WriteByte(bech32Charset[d])
	}

	return result.String(), nil
}

// --- Address conversion ---

// ConvertFromBitcoin converts a Bitcoin/Dogecoin address to an ID address.
func ConvertFromBitcoin(bitcoinAddr string) (string, error) {
	// Try Base58 decoding (legacy addresses: P2PKH, P2SH).
	version, payload, err := Base58CheckDecode(bitcoinAddr)
	if err == nil {
		return convertFromLegacyAddress(version, payload)
	}

	// Try Bech32 decoding (SegWit addresses: P2WPKH, P2WSH, P2TR).
	hrp, witnessVersion, program, _, err := Bech32Decode(bitcoinAddr)
	if err == nil {
		return convertFromSegWitAddress(hrp, witnessVersion, program)
	}

	return "", fmt.Errorf("unsupported address format: %s", bitcoinAddr)
}

// convertFromLegacyAddress converts a legacy Base58Check address to ID format.
func convertFromLegacyAddress(version byte, payload []byte) (string, error) {
	switch version {
	case 0x00: // Bitcoin mainnet P2PKH
		return EncodeIDAddress(VersionP2PKH, payload)
	case 0x05: // Bitcoin mainnet P2SH
		return EncodeIDAddress(VersionP2SH, payload)
	case 0x6F: // Bitcoin testnet P2PKH
		return EncodeIDAddress(VersionP2PKH, payload)
	case 0xC4: // Bitcoin testnet P2SH
		return EncodeIDAddress(VersionP2SH, payload)
	case 0x1E: // Dogecoin mainnet P2PKH
		return EncodeIDAddress(VersionP2PKH, payload)
	case 0x16: // Dogecoin mainnet P2SH
		return EncodeIDAddress(VersionP2SH, payload)
	default:
		return "", fmt.Errorf("unsupported version byte: 0x%02x", version)
	}
}

// convertFromSegWitAddress converts a SegWit (Bech32/Bech32m) address to ID format.
func convertFromSegWitAddress(hrp string, witnessVersion byte, program []byte) (string, error) {
	if hrp != "bc" && hrp != "tb" {
		return "", fmt.Errorf("unsupported network: %s", hrp)
	}

	switch witnessVersion {
	case 0:
		switch len(program) {
		case 20:
			return EncodeIDAddress(VersionP2WPKH, program)
		case 32:
			return EncodeIDAddress(VersionP2WSH, program)
		default:
			return "", fmt.Errorf("invalid witness v0 program length: %d", len(program))
		}
	case 1:
		if len(program) == 32 {
			return EncodeIDAddress(VersionP2TR, program)
		}
		return "", fmt.Errorf("invalid taproot program length: %d", len(program))
	default:
		return "", fmt.Errorf("unsupported witness version: %d", witnessVersion)
	}
}

// ConvertToBitcoin converts an ID address back to a Bitcoin address.
func ConvertToBitcoin(idAddr string, network string) (string, error) {
	info, err := DecodeIDAddress(idAddr)
	if err != nil {
		return "", err
	}

	hrp := "bc"
	if network == "testnet" {
		hrp = "tb"
	}

	switch info.Version {
	case VersionP2PKH:
		var version byte
		if network == "mainnet" {
			version = 0x00
		} else {
			version = 0x6F
		}
		return Base58CheckEncode(version, info.Data), nil

	case VersionP2SH:
		var version byte
		if network == "mainnet" {
			version = 0x05
		} else {
			version = 0xC4
		}
		return Base58CheckEncode(version, info.Data), nil

	case VersionP2WPKH:
		if len(info.Data) != 20 {
			return "", fmt.Errorf("invalid P2WPKH data length: %d", len(info.Data))
		}
		return Bech32Encode(hrp, 0, info.Data)

	case VersionP2WSH:
		if len(info.Data) != 32 {
			return "", fmt.Errorf("invalid P2WSH data length: %d", len(info.Data))
		}
		return Bech32Encode(hrp, 0, info.Data)

	case VersionP2TR:
		if len(info.Data) != 32 {
			return "", fmt.Errorf("invalid P2TR data length: %d", len(info.Data))
		}
		return Bech32Encode(hrp, 1, info.Data)

	default:
		return "", fmt.Errorf("cannot convert version %d to Bitcoin address", info.Version)
	}
}

// ConvertToDogecoin converts an ID address back to a Dogecoin address.
func ConvertToDogecoin(idAddr string) (string, error) {
	info, err := DecodeIDAddress(idAddr)
	if err != nil {
		return "", err
	}

	var version byte
	switch info.Version {
	case VersionP2PKH:
		version = 0x1E
	case VersionP2SH:
		version = 0x16
	default:
		return "", fmt.Errorf("cannot convert version %d to Dogecoin address", info.Version)
	}

	return Base58CheckEncode(version, info.Data), nil
}

// ParseBitcoinAddress parses a Bitcoin/Dogecoin address and returns its type.
func ParseBitcoinAddress(addr string) (*BitcoinAddress, error) {
	version, payload, err := Base58CheckDecode(addr)
	if err != nil {
		return nil, err
	}

	result := &BitcoinAddress{
		Data: payload,
	}

	switch version {
	case 0x00:
		result.Type = "P2PKH"
		result.Network = "mainnet"
	case 0x05:
		result.Type = "P2SH"
		result.Network = "mainnet"
	case 0x6F:
		result.Type = "P2PKH"
		result.Network = "testnet"
	case 0xC4:
		result.Type = "P2SH"
		result.Network = "testnet"
	case 0x1E:
		result.Type = "P2PKH"
		result.Network = "dogecoin"
	case 0x16:
		result.Type = "P2SH"
		result.Network = "dogecoin"
	default:
		return nil, fmt.Errorf("unknown version: 0x%02x", version)
	}

	return result, nil
}

// BitcoinAddress holds parsed Bitcoin/Dogecoin address information.
type BitcoinAddress struct {
	Type    string
	Network string
	Data    []byte
}

// AddressConverter helps convert addresses between chain formats and ID format.
type AddressConverter struct {
	defaultNetwork string
}

// NewAddressConverter creates a new address converter with the given default network.
func NewAddressConverter(defaultNetwork string) *AddressConverter {
	return &AddressConverter{defaultNetwork: defaultNetwork}
}

// ToID converts any supported chain address to ID address format.
func (ac *AddressConverter) ToID(addr string) (string, error) {
	return ConvertFromBitcoin(addr)
}

// FromID converts an ID address to the specified network's format.
func (ac *AddressConverter) FromID(idAddr, network string) (string, error) {
	if network == "" {
		network = ac.defaultNetwork
	}
	switch network {
	case "bitcoin", "mainnet", "testnet":
		return ConvertToBitcoin(idAddr, network)
	case "dogecoin":
		return ConvertToDogecoin(idAddr)
	default:
		return "", fmt.Errorf("unsupported network: %s", network)
	}
}

// Batch converts multiple addresses to ID format.
func (ac *AddressConverter) Batch(addrs []string) ([]string, []error) {
	results := make([]string, len(addrs))
	errs := make([]error, len(addrs))
	for i, addr := range addrs {
		results[i], errs[i] = ac.ToID(addr)
	}
	return results, errs
}

// init registers hex encoding for base58 (used by btcd/btcutil/base58).
func init() {
	_ = hex.EncodeToString([]byte{})
}
