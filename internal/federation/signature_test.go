package federation

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/metaid-developers/metaso-p2p/internal/presence"
)

const (
	testPrivateKeyHex = "0000000000000000000000000000000000000000000000000000000000000001"
	testPublicKeyHex  = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
)

func TestCanonicalSnapshotPayloadExcludesSignature(t *testing.T) {
	snapshot := testSignatureSnapshot()
	snapshot.Signature = "must-not-be-signed"

	payload, err := CanonicalSnapshotPayload(snapshot)
	if err != nil {
		t.Fatalf("canonical payload: %v", err)
	}

	if strings.Contains(string(payload), "signature") || strings.Contains(string(payload), "must-not-be-signed") {
		t.Fatalf("canonical payload included signature data: %s", payload)
	}
	want := `{"protocol":"metaso-p2p-presence","version":"1.0.0","nodeId":"node-a","generatedAt":1710000001000,"ttlSeconds":30,"sequence":7,"items":[{"metaid":"meta-1","type":"pc","connectedAt":1710000000000,"lastSeenAt":1710000000500}]}`
	if string(payload) != want {
		t.Fatalf("canonical payload:\nwant: %s\n got: %s", want, payload)
	}
}

func TestSignatureDERHexVerifiesSamePayloadTwice(t *testing.T) {
	snapshot := testSignatureSnapshot()

	signature1, err := SignSnapshot(snapshot, testPrivateKeyHex)
	if err != nil {
		t.Fatalf("sign first snapshot: %v", err)
	}
	signature2, err := SignSnapshot(snapshot, testPrivateKeyHex)
	if err != nil {
		t.Fatalf("sign second snapshot: %v", err)
	}
	if signature1 != signature2 {
		t.Fatalf("signature should be deterministic for the same payload:\nfirst:  %s\nsecond: %s", signature1, signature2)
	}

	decoded, err := hex.DecodeString(signature1)
	if err != nil {
		t.Fatalf("signature should be hex: %v", err)
	}
	if len(decoded) == 0 || decoded[0] != 0x30 {
		t.Fatalf("signature should be DER-encoded hex, got %x", decoded)
	}

	snapshot.Signature = signature1
	if err := VerifySnapshot(snapshot, "node-a", testPublicKeyHex); err != nil {
		t.Fatalf("verify first signature: %v", err)
	}
	snapshot.Signature = signature2
	if err := VerifySnapshot(snapshot, "node-a", testPublicKeyHex); err != nil {
		t.Fatalf("verify second signature: %v", err)
	}
}

func TestVerifySnapshotFailsWhenItemsChange(t *testing.T) {
	snapshot := testSignatureSnapshot()
	signature, err := SignSnapshot(snapshot, testPrivateKeyHex)
	if err != nil {
		t.Fatalf("sign snapshot: %v", err)
	}
	snapshot.Signature = signature
	snapshot.Items[0].LastSeenAt++

	if err := VerifySnapshot(snapshot, "node-a", testPublicKeyHex); err == nil {
		t.Fatal("verify should fail after signed items change")
	}
}

func TestVerifySnapshotFailsWhenNodeIDDoesNotMatchExpectedRegistryNode(t *testing.T) {
	snapshot := testSignatureSnapshot()
	signature, err := SignSnapshot(snapshot, testPrivateKeyHex)
	if err != nil {
		t.Fatalf("sign snapshot: %v", err)
	}
	snapshot.Signature = signature

	if err := VerifySnapshot(snapshot, "node-b", testPublicKeyHex); err == nil {
		t.Fatal("verify should fail when snapshot nodeId does not match the registry node")
	}
}

func TestVerifySnapshotRejectsUncompressedPublicKey(t *testing.T) {
	snapshot := testSignatureSnapshot()
	signature, err := SignSnapshot(snapshot, testPrivateKeyHex)
	if err != nil {
		t.Fatalf("sign snapshot: %v", err)
	}
	snapshot.Signature = signature

	uncompressedPublicKeyHex := "0479be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798483ada7726a3c4655da4fbfc0e1108a8fd17b448a68554199c47d08ffb10d4b8"
	uncompressedPublicKey, err := hex.DecodeString(uncompressedPublicKeyHex)
	if err != nil {
		t.Fatalf("decode uncompressed public key fixture: %v", err)
	}
	if _, err := btcec.ParsePubKey(uncompressedPublicKey); err != nil {
		t.Fatalf("uncompressed public key fixture should parse as a valid btcec key: %v", err)
	}

	if err := VerifySnapshot(snapshot, "node-a", uncompressedPublicKeyHex); err == nil {
		t.Fatal("verify should reject uncompressed registry public keys")
	}
	if err := VerifySnapshot(snapshot, "node-a", testPublicKeyHex); err != nil {
		t.Fatalf("verify should still accept compressed registry public keys: %v", err)
	}
}

func TestSignatureHelpersRejectInvalidKeysAndPayloadsWithoutPanic(t *testing.T) {
	snapshot := testSignatureSnapshot()

	assertReturnsErrorWithoutPanic(t, "nil canonical payload", func() error {
		_, err := CanonicalSnapshotPayload(nil)
		return err
	})
	assertReturnsErrorWithoutPanic(t, "nil sign payload", func() error {
		_, err := SignSnapshot(nil, testPrivateKeyHex)
		return err
	})
	assertReturnsErrorWithoutPanic(t, "non-hex private key", func() error {
		_, err := SignSnapshot(snapshot, "not-hex")
		return err
	})
	assertReturnsErrorWithoutPanic(t, "zero private key", func() error {
		_, err := SignSnapshot(snapshot, strings.Repeat("0", 64))
		return err
	})

	snapshot.Signature = "not-hex"
	assertReturnsErrorWithoutPanic(t, "non-hex signature", func() error {
		return VerifySnapshot(snapshot, "node-a", testPublicKeyHex)
	})
	snapshot.Signature = "00"
	assertReturnsErrorWithoutPanic(t, "malformed signature", func() error {
		return VerifySnapshot(snapshot, "node-a", testPublicKeyHex)
	})
	validSignature, err := SignSnapshot(snapshot, testPrivateKeyHex)
	if err != nil {
		t.Fatalf("sign snapshot for public key errors: %v", err)
	}
	snapshot.Signature = validSignature
	assertReturnsErrorWithoutPanic(t, "non-hex public key", func() error {
		return VerifySnapshot(snapshot, "node-a", "not-hex")
	})
	assertReturnsErrorWithoutPanic(t, "invalid public key", func() error {
		return VerifySnapshot(snapshot, "node-a", strings.Repeat("00", 33))
	})
	assertReturnsErrorWithoutPanic(t, "nil verify payload", func() error {
		return VerifySnapshot(nil, "node-a", testPublicKeyHex)
	})
}

func testSignatureSnapshot() *presence.Snapshot {
	return &presence.Snapshot{
		Protocol:    ProtocolPresence,
		Version:     Version,
		NodeID:      "node-a",
		GeneratedAt: 1710000001000,
		TTLSeconds:  30,
		Sequence:    7,
		Items: []presence.OnlineEntry{
			{
				MetaId:      "meta-1",
				Type:        "pc",
				ConnectedAt: 1710000000000,
				LastSeenAt:  1710000000500,
			},
		},
		Signature: "",
	}
}

func assertReturnsErrorWithoutPanic(t *testing.T, name string, fn func() error) {
	t.Helper()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s panicked: %v", name, r)
		}
	}()
	if err := fn(); err == nil {
		t.Fatalf("%s should return an error", name)
	}
}
