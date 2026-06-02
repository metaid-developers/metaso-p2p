package federation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/metaid-developers/meta-socket/internal/presence"
)

type signableSnapshot struct {
	Protocol    string                 `json:"protocol"`
	Version     string                 `json:"version"`
	NodeID      string                 `json:"nodeId"`
	GeneratedAt int64                  `json:"generatedAt"`
	TTLSeconds  int64                  `json:"ttlSeconds"`
	Sequence    uint64                 `json:"sequence"`
	Items       []presence.OnlineEntry `json:"items"`
}

// CanonicalSnapshotPayload returns the deterministic JSON bytes covered by the snapshot signature.
func CanonicalSnapshotPayload(snapshot *presence.Snapshot) ([]byte, error) {
	if snapshot == nil {
		return nil, errors.New("canonical snapshot payload requires a snapshot")
	}

	items := snapshot.Items
	if items == nil {
		items = []presence.OnlineEntry{}
	} else {
		items = append([]presence.OnlineEntry(nil), items...)
	}

	payload := signableSnapshot{
		Protocol:    snapshot.Protocol,
		Version:     snapshot.Version,
		NodeID:      snapshot.NodeID,
		GeneratedAt: snapshot.GeneratedAt,
		TTLSeconds:  snapshot.TTLSeconds,
		Sequence:    snapshot.Sequence,
		Items:       items,
	}
	return json.Marshal(payload)
}

// SignSnapshot signs the canonical snapshot payload with secp256k1 ECDSA and returns DER hex.
func SignSnapshot(snapshot *presence.Snapshot, privateKeyHex string) (string, error) {
	payload, err := CanonicalSnapshotPayload(snapshot)
	if err != nil {
		return "", err
	}
	privateKey, err := parsePrivateKeyHex(privateKeyHex)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(payload)
	signature := ecdsa.Sign(privateKey, hash[:])
	return hex.EncodeToString(signature.Serialize()), nil
}

// VerifySnapshot checks snapshot node identity and verifies its DER-hex secp256k1 signature.
func VerifySnapshot(snapshot *presence.Snapshot, expectedNodeID string, publicKeyHex string) error {
	if snapshot == nil {
		return errors.New("verify snapshot requires a snapshot")
	}
	if snapshot.NodeID != expectedNodeID {
		return fmt.Errorf("snapshot nodeId %q does not match expected node %q", snapshot.NodeID, expectedNodeID)
	}
	signature, err := parseDERSignatureHex(snapshot.Signature)
	if err != nil {
		return err
	}
	publicKey, err := parsePublicKeyHex(publicKeyHex)
	if err != nil {
		return err
	}
	payload, err := CanonicalSnapshotPayload(snapshot)
	if err != nil {
		return err
	}

	hash := sha256.Sum256(payload)
	if !signature.Verify(hash[:], publicKey) {
		return errors.New("snapshot signature verification failed")
	}
	return nil
}

func parsePrivateKeyHex(privateKeyHex string) (*btcec.PrivateKey, error) {
	decoded, err := decodeHexField(privateKeyHex, "private key")
	if err != nil {
		return nil, err
	}
	if len(decoded) != btcec.PrivKeyBytesLen {
		return nil, fmt.Errorf("private key must be %d bytes, got %d", btcec.PrivKeyBytesLen, len(decoded))
	}

	keyValue := new(big.Int).SetBytes(decoded)
	if keyValue.Sign() <= 0 {
		return nil, errors.New("private key must be greater than zero")
	}
	if keyValue.Cmp(btcec.S256().N) >= 0 {
		return nil, errors.New("private key must be less than the secp256k1 group order")
	}

	privateKey, _ := btcec.PrivKeyFromBytes(decoded)
	return privateKey, nil
}

func parsePublicKeyHex(publicKeyHex string) (*btcec.PublicKey, error) {
	decoded, err := decodeHexField(publicKeyHex, "public key")
	if err != nil {
		return nil, err
	}
	if len(decoded) != btcec.PubKeyBytesLenCompressed {
		return nil, fmt.Errorf("public key must be compressed %d bytes, got %d", btcec.PubKeyBytesLenCompressed, len(decoded))
	}
	if decoded[0] != 0x02 && decoded[0] != 0x03 {
		return nil, errors.New("public key must use compressed secp256k1 prefix 0x02 or 0x03")
	}
	publicKey, err := btcec.ParsePubKey(decoded)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	return publicKey, nil
}

func parseDERSignatureHex(signatureHex string) (*ecdsa.Signature, error) {
	decoded, err := decodeHexField(signatureHex, "signature")
	if err != nil {
		return nil, err
	}
	signature, err := ecdsa.ParseDERSignature(decoded)
	if err != nil {
		return nil, fmt.Errorf("parse signature: %w", err)
	}
	return signature, nil
}

func decodeHexField(value string, name string) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, fmt.Errorf("%s is required", name)
	}
	decoded, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("decode %s hex: %w", name, err)
	}
	return decoded, nil
}
