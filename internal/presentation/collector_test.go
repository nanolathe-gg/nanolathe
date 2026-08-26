package presentation

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
)

func TestCollectorAdmitsTypedEventsInStableOrder(t *testing.T) {
	c := NewCollector(Limits{MaxEvents: 20, MaxEffectEvents: 20, MaxSounds: 2})
	if !c.EmitCOBSFX(Event{Tick: 7, Source: pool.Handle(3), Piece: 4, SFXType: 0x101, SFXClass: SFXWhiteSmoke, Lifetime: 8}) {
		t.Fatal("COB SFX was rejected")
	}
	if !c.EmitSmokeEnd(Event{Tick: 7, Source: pool.Handle(3), Graphic: "smoke", ExpiryTick: 12}) {
		t.Fatal("smoke end was rejected")
	}
	if !c.EmitSound(Event{Tick: 7, Alias: "weapon_fire", SoundID: 5}) {
		t.Fatal("sound was rejected")
	}
	got := c.Events()
	if len(got) != 3 {
		t.Fatalf("event count = %d, want 3", len(got))
	}
	for i, e := range got {
		want := uint64(i + 1)
		if e.ID != uint32(i+1) || e.Sequence != want || e.Tick != 7 {
			t.Fatalf("event %d identity = id %d seq %d tick %d", i, e.ID, e.Sequence, e.Tick)
		}
	}
	if got[0].Kind != KindCOBSFX || got[1].Kind != KindSmokeEnd || got[2].Kind != KindSound {
		t.Fatalf("admission order/kinds = %v, %v, %v", got[0].Kind, got[1].Kind, got[2].Kind)
	}
	if got[0].Piece != 4 || got[0].SFXType != 0x101 || got[0].SFXClass != SFXWhiteSmoke || got[0].Lifetime != 8 {
		t.Fatal("typed COB SFX fields were not preserved")
	}
}

func TestCollectorBoundsRejectWithoutConsumingIdentity(t *testing.T) {
	c := NewCollector(Limits{MaxEvents: 4, MaxEffectEvents: 2, MaxSounds: 1})
	if !c.EmitExplosion(Event{Tick: 1}) {
		t.Fatal("first effect was rejected")
	}
	if !c.EmitImpact(Event{Tick: 1}) {
		t.Fatal("second effect was rejected")
	}
	if !c.EmitSound(Event{Tick: 1, Alias: "hit"}) {
		t.Fatal("first sound was rejected")
	}
	if c.EmitNanolathe(Event{Tick: 1}) {
		t.Fatal("effect over bound was admitted")
	}
	if c.EmitSound(Event{Tick: 1, Alias: "second"}) {
		t.Fatal("sound over bound was admitted")
	}
	if c.Admit(Event{Tick: 1, Kind: KindInvalid}) {
		t.Fatal("invalid kind was admitted")
	}
	if c.Admit(Event{Tick: 1, Kind: KindImpact, Lifetime: -1}) {
		t.Fatal("negative lifetime was admitted")
	}
	if c.EmitLHTFlash(Event{Tick: 2}) {
		t.Fatal("effect bound should reject the third effect")
	}
	got := c.Events()
	if len(got) != 3 || got[2].ID != 3 || got[2].Sequence != 3 {
		t.Fatalf("rejected admissions consumed identity: %#v", got)
	}
	if !c.Overflow() || c.Dropped() != 5 {
		t.Fatalf("overflow status = %v dropped %d, want true/5", c.Overflow(), c.Dropped())
	}
	batch := c.Snapshot()
	if !batch.Overflow || batch.Dropped != 5 || len(batch.Events) != 3 {
		t.Fatalf("snapshot admission status = %#v", batch)
	}
}

func TestSnapshotEventsAreCopiedAndCarryExplicitLifetime(t *testing.T) {
	c := NewCollector(Limits{MaxEvents: 4, MaxEffectEvents: 4, MaxSounds: 4})
	input := Event{
		Tick: 9, Source: pool.Handle(2), Target: pool.Handle(5), Piece: 8,
		SFXType: 3, SFXClass: SFXVector, Graphic: "flash", Alias: "impact", Lifetime: 0, ExpiryTick: 15,
		Mode: 2, Team: 1, PaletteRow: 4, Magnitude: 12,
	}
	if !c.EmitImpact(input) {
		t.Fatal("impact was rejected")
	}
	out := c.SnapshotEvents()
	if len(out) != 1 || out[0].ID != 1 || out[0].Kind != KindImpact || out[0].Sequence != 1 || out[0].Lifetime != 0 || out[0].ExpiryTick != 15 {
		t.Fatalf("snapshot event = %#v", out)
	}
	if out[0].Source != pool.Handle(2) || out[0].Target != pool.Handle(5) || out[0].Piece != 8 || out[0].SFXClass != SFXVector || out[0].Magnitude != 12 {
		t.Fatal("snapshot event lost typed fields")
	}
	input.Graphic = "changed"
	if c.Events()[0].Graphic != "flash" || c.SnapshotEvents()[0].Graphic != "flash" {
		t.Fatal("collector or snapshot event aliases input")
	}
	out[0].Kind = KindInvalid
	if c.SnapshotEvents()[0].Kind != KindImpact {
		t.Fatal("snapshot conversion returned collector-backed storage")
	}
}

func TestCollectorResetStartsNewAdmissionWindow(t *testing.T) {
	c := NewCollector(Limits{MaxEvents: 1, MaxEffectEvents: 1, MaxSounds: 1})
	if !c.EmitShake(Event{Tick: 3, Magnitude: 2}) {
		t.Fatal("shake was rejected")
	}
	c.Reset()
	if c.Dropped() != 0 || c.Overflow() {
		t.Fatalf("reset admission status = dropped %d overflow %v", c.Dropped(), c.Overflow())
	}
	if !c.EmitSound(Event{Tick: 4}) {
		t.Fatal("event was rejected after reset")
	}
	got := c.Events()
	if len(got) != 1 || got[0].ID != 2 || got[0].Sequence != 2 {
		t.Fatalf("reset identity = %#v", got)
	}
}

func TestCollectorInvalidAdmissionIsDroppedWithoutOverflow(t *testing.T) {
	c := NewCollector(Limits{MaxEvents: 2, MaxEffectEvents: 2, MaxSounds: 2})
	if c.Admit(Event{Kind: KindInvalid}) {
		t.Fatal("invalid event was admitted")
	}
	if c.Dropped() != 1 || c.Overflow() {
		t.Fatalf("invalid admission status = dropped %d overflow %v", c.Dropped(), c.Overflow())
	}
}
