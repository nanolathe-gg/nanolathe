package checkpoint

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
)

func authoredIdentity() Identity {
	var identity Identity
	for i := range identity.Content {
		identity.Content[i] = byte(i)
		identity.Config[i] = byte(i + 32)
	}
	return identity
}

func writeAuthoredCapture(t *testing.T, identity Identity, out io.Writer, changed bool) Digests {
	t.Helper()
	c, err := NewCapture(identity, out)
	if err != nil {
		t.Fatal(err)
	}
	// Named constants in published order also lock each exported ID against
	// the independently authored byte and digest vectors below.
	owners := [...]Owner{
		OwnerRuntime, OwnerUnits, OwnerOrders, OwnerScripts, OwnerWorld,
		OwnerVisibility, OwnerMovement, OwnerPaths, OwnerEconomy,
		OwnerConstruction, OwnerCombat, OwnerEffects, OwnerComputersScenario,
	}
	for _, owner := range owners {
		e, err := c.Section(owner, owner == OwnerRuntime || owner == OwnerWorld)
		if err != nil {
			t.Fatal(err)
		}
		switch owner {
		case OwnerRuntime:
			e.Field("authored.tick")
			e.U32(0x01020304)
			e.Bool(true)
			e.String("entry")
		case OwnerWorld:
			e.Field("authored.rows")
			e.Count(2)
			if changed {
				e.String("ROW")
			} else {
				e.String("row")
			}
			e.Bytes(nil)
		}
	}
	digests, err := c.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return digests
}

func TestCaptureAuthoredByteAndHashVectors(t *testing.T) {
	// The stream is authored independently of Encoder. Digest vectors were
	// computed with Python hashlib over these bytes and the specified domains.
	wantBytes := "4e4c4350535441540200" +
		"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f" +
		"202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f" +
		"0d00" +
		"010001040302010105000000656e747279" +
		"020000030000040000" +
		"0500010200000003000000726f7700000000" +
		"0600000700000800000900000a00000b00000c00000d0000"
	const wantFull = "23323535a67f63b3ccf20042aa2804d0cb8dd3d104ce7b69fd6c7faa356cd410"
	wantOwners := [OwnerCount]string{
		"e85be6bf21191ea75e609038f5b1f757fa4ae5a78a8725f334ad44883e2d60c4",
		"5b334fa83efe1f29a2d2a41317cad7771ff31e6154fd02641188a23b5035588c",
		"36d00ce627c7d8590783e55047a4b3ded13fc06d15bfef8e8179607c78bc2bf2",
		"1f996708a46487b9ee8ac7bade99e06694d8472ce4c48f426d3dacb681ad19ef",
		"9fa252deedd6a0a9b94c1231e66d3beda0528c6e6d5ad2d15f0bf203cc52d804",
		"42dbf52e5fd5f1b1d14da5ba62d1db09329e6b7408f23a1340fcedcee8153da5",
		"74bd27580f362ac52a7e4d8b07b2bcbde0d849ed1a1bce347482a2a86ac51977",
		"00b304c534c7097f21c75b8c01db7b16c8113d0c67f2bf2aca69ee9e95384d6c",
		"23ff4228fd82254e55fd07318e16e699565fe27c361d91b7e3b0d37069fe2965",
		"4c567c54140a261445df32dc36bd63e7cbbbcf8ee7b3ee9297084ab7948ea40a",
		"473f194eb3b6d74190b07458e893882905c80672a6aac82647243fd85c96f318",
		"682d685170cadae625ff9ecc64f59c820479e961a86b2dd98f105f3fa246ea6a",
		"9b4b1b3da38175adde5f01ffd0ca530d1ef8bd1233a0ab853446b0ccfe49868a",
	}
	var out bytes.Buffer
	got := writeAuthoredCapture(t, authoredIdentity(), &out, false)
	if hex.EncodeToString(out.Bytes()) != wantBytes {
		t.Fatalf("stream = %x, want %s", out.Bytes(), wantBytes)
	}
	if hex.EncodeToString(got.Full[:]) != wantFull {
		t.Fatalf("full = %x, want %s", got.Full, wantFull)
	}
	for i, owner := range got.Owners {
		if hex.EncodeToString(owner[:]) != wantOwners[i] {
			t.Errorf("owner %d = %x, want %s", i+1, owner, wantOwners[i])
		}
	}
	if got.Full != sha256.Sum256(out.Bytes()) {
		t.Fatal("diagnostic bytes and full digest disagree")
	}
	if hashOnly := writeAuthoredCapture(t, authoredIdentity(), nil, false); got != hashOnly {
		t.Fatal("hash-only capture differs from diagnostic byte capture")
	}
}

func TestCaptureOwnerDomainsAndIdentity(t *testing.T) {
	base := writeAuthoredCapture(t, authoredIdentity(), nil, false)
	changed := writeAuthoredCapture(t, authoredIdentity(), nil, true)
	if base.Full == changed.Full {
		t.Fatal("changed payload did not change full digest")
	}
	for i := range base.Owners {
		if equal := base.Owners[i] == changed.Owners[i]; equal == (i == int(OwnerWorld)-1) {
			t.Fatalf("owner %d isolated payload change incorrectly: equal %t", i+1, equal)
		}
	}
	for _, config := range []bool{false, true} {
		identity := authoredIdentity()
		if config {
			identity.Config[3] ^= 1
		} else {
			identity.Content[3] ^= 1
		}
		bound := writeAuthoredCapture(t, identity, nil, false)
		if base.Full == bound.Full {
			t.Fatal("identity did not change full digest")
		}
		for i := range base.Owners {
			if base.Owners[i] == bound.Owners[i] {
				t.Fatalf("identity did not change owner %d digest", i+1)
			}
		}
	}
}

func TestCaptureSectionOrderAndCompleteness(t *testing.T) {
	tests := []struct {
		name   string
		owners []Owner
	}{
		{"none", nil},
		{"missing", []Owner{OwnerRuntime}},
		{"repeat", []Owner{OwnerRuntime, OwnerRuntime}},
		{"skip", []Owner{OwnerRuntime, OwnerOrders}},
		{"backwards", []Owner{OwnerUnits}},
		{"zero", []Owner{0}},
		{"unknown", []Owner{14}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewCapture(Identity{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, owner := range tt.owners {
				_, err = c.Section(owner, false)
				if err != nil {
					break
				}
			}
			digests, finishErr := c.Finish()
			if finishErr == nil || digests != (Digests{}) {
				t.Fatalf("invalid section sequence accepted: %v, %v", digests, finishErr)
			}
			if _, err := c.Section(OwnerRuntime, false); err != finishErr {
				t.Fatalf("section error changed: %v, want %v", err, finishErr)
			}
		})
	}
}

func TestCaptureClosedAndAbsentSections(t *testing.T) {
	for _, misuse := range []string{"absent", "advanced", "copied advanced", "finished", "copied finished", "finish twice", "section after finish"} {
		t.Run(misuse, func(t *testing.T) {
			var out bytes.Buffer
			c, err := NewCapture(Identity{}, &out)
			if err != nil {
				t.Fatal(err)
			}
			first, err := c.Section(OwnerRuntime, misuse != "absent")
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(misuse, "copied") {
				copied := *first
				first = &copied
			}
			if misuse != "absent" {
				for owner := OwnerUnits; owner <= OwnerComputersScenario; owner++ {
					if _, err := c.Section(owner, false); err != nil {
						t.Fatal(err)
					}
				}
			}
			if misuse == "finished" || misuse == "copied finished" || misuse == "finish twice" || misuse == "section after finish" {
				if _, err := c.Finish(); err != nil {
					t.Fatal(err)
				}
			}
			before := out.Len()
			if misuse == "section after finish" {
				if _, err := c.Section(OwnerRuntime, true); err == nil {
					t.Fatal("section after finish accepted")
				}
			} else if misuse != "finish twice" {
				first.Field("late.payload")
				first.Bytes(nil)
				if first.Err() == nil || !strings.Contains(first.Err().Error(), "runtime") || !strings.Contains(first.Err().Error(), "late.payload") {
					t.Fatalf("missing section misuse context: %v", first.Err())
				}
			}
			if got, err := c.Finish(); err == nil || got != (Digests{}) {
				t.Fatalf("misuse retained usable digests: %v, %v", got, err)
			}
			if out.Len() != before {
				t.Fatal("misuse wrote bytes")
			}
		})
	}
}

func TestCaptureFailuresInvalidateAllDigests(t *testing.T) {
	const headerSize = 8 + 2 + 32 + 32 + 2
	cause := errors.New("capture sink failure")
	for _, allowance := range []int{0, 9, headerSize, headerSize + 2, headerSize + 5} {
		for _, sinkErr := range []error{nil, cause} {
			w := &faultSink{remaining: allowance, err: sinkErr}
			c, err := NewCapture(Identity{}, w)
			want := sinkErr
			if want == nil {
				want = io.ErrShortWrite
			}
			if err == nil {
				e, sectionErr := c.Section(OwnerRuntime, true)
				if sectionErr == nil {
					e.Field("authored.tick")
					e.U32(7)
				}
				calls := w.calls
				var got Digests
				got, err = c.Finish()
				if got != (Digests{}) || calls != w.calls {
					t.Fatal("failed sink produced digests or accepted more writes")
				}
			} else if c != nil {
				t.Fatal("header failure returned a capture")
			}
			if !errors.Is(err, want) {
				t.Fatalf("allowance %d: %v, want %v", allowance, err, want)
			}
		}
	}
	var out bytes.Buffer
	c, err := NewCapture(Identity{}, &out)
	if err != nil {
		t.Fatal(err)
	}
	e, err := c.Section(OwnerRuntime, true)
	if err != nil {
		t.Fatal(err)
	}
	e.Field("wind.scalar")
	e.F32(math.Float32frombits(0x7fc00001))
	before := out.Len()
	if got, err := c.Finish(); err == nil || got != (Digests{}) || !strings.Contains(err.Error(), "runtime") || !strings.Contains(err.Error(), "wind.scalar") {
		t.Fatalf("NaN did not invalidate capture with context: %v, %v", got, err)
	}
	e.U8(1)
	if out.Len() != before {
		t.Fatal("NaN allowed a later payload")
	}
}

func TestCaptureHashBuffersPreserveSectionBytes(t *testing.T) {
	var out, expected bytes.Buffer
	identity := authoredIdentity()
	c, err := NewCapture(identity, &out)
	if err != nil {
		t.Fatal(err)
	}
	expected.WriteString("NLCPSTAT")
	expected.Write([]byte{2, 0})
	expected.Write(identity.Content[:])
	expected.Write(identity.Config[:])
	expected.Write([]byte{13, 0})
	var want Digests
	for owner := OwnerRuntime; owner <= OwnerComputersScenario; owner++ {
		e, err := c.Section(owner, true)
		if err != nil {
			t.Fatal(err)
		}
		var section bytes.Buffer
		section.Write([]byte{byte(owner), 0, 1})
		// Sizes on both sides of the hash buffer boundary, in one-byte writes.
		for i := 0; i < 4090+int(owner); i++ {
			value := byte(i*7 + int(owner))
			e.U8(value)
			section.WriteByte(value)
		}
		expected.Write(section.Bytes())
		var domain bytes.Buffer
		domain.WriteString("NLCPSECT")
		domain.Write([]byte{2, 0})
		domain.Write(identity.Content[:])
		domain.Write(identity.Config[:])
		domain.Write(section.Bytes())
		want.Owners[owner-1] = sha256.Sum256(domain.Bytes())
	}
	want.Full = sha256.Sum256(expected.Bytes())
	got, err := c.Finish()
	if err != nil || got != want || !bytes.Equal(out.Bytes(), expected.Bytes()) {
		t.Fatalf("buffered canonical digest/bytes differ: %v", err)
	}
}
