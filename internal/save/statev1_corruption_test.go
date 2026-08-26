package save

import (
	"encoding/binary"
	"testing"
)

func TestStateV1RejectsCorruptCollectionCountBeforeAllocation(t *testing.T) {
	payload := MarshalStateV1(&StateV1{Version: StateV1VersionConst})
	// Empty hashes, RNG fields, and the 28-byte clock place the unit count at
	// byte 64. A hostile count must be rejected while the payload is still
	// tiny; this guards the decoder against uint32-sized preallocations.
	if len(payload) < 68 {
		t.Fatalf("minimal StateV1 payload unexpectedly short: %d", len(payload))
	}
	binary.LittleEndian.PutUint32(payload[64:68], ^uint32(0))
	if _, err := UnmarshalStateV1(payload, "", ""); err == nil {
		t.Fatal("corrupt unit count was accepted")
	}
}

func TestStateV1RejectsShortBinaryReads(t *testing.T) {
	payload := MarshalStateV1(&StateV1{Version: StateV1VersionConst, CatalogHash: "catalog"})
	if len(payload) < 2 {
		t.Fatal("minimal StateV1 payload unexpectedly short")
	}
	payload = payload[:len(payload)-1]
	if _, err := UnmarshalStateV1(payload, "", ""); err == nil {
		t.Fatal("truncated StateV1 payload was accepted")
	}
}
