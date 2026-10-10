package replay

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// File layout, version 1. Every integer is a netproto version-1 primitive
// (shortest unsigned varint unless named otherwise):
//
//	magic        8 bytes "NLREPLAY"
//	version      u16
//	header size  u32, then that many header bytes (encodeHeader)
//	chunks       until the end of the file, each
//	               compressed size u32, raw size u32,
//	               raw-size bytes of entries as one DEFLATE stream
//
// A chunk's raw bytes open with the stream state at its start — cursor tick
// u32, last command position u64, last checksum tick u32, last command tick
// u32 — so a chunk decodes on its own, then hold whole entries:
//
//	1 command   tick − cursor u32 (≥1), seat u8, position − last position
//	            u64 (≥1), payload size u32 and bytes (a SeatCommand
//	            encoding in the header kind's command context)
//	2 pumps     count u32 (≥1), ticks per pump u32 (≥1)
//	3 checksum  tick − cursor u32, digest
//	4 end       final tick u32 (the cursor), reason u8
//
// The cursor is the last tick of the last recorded pump. A file that stops
// inside a chunk, or after a whole chunk without an end entry, plays up to
// its last whole chunk. These are Nanolathe file contracts, not retail ones.

// Magic opens every replay file.
const Magic = "NLREPLAY"

// FormatVersion is the replay format this build writes and reads.
const FormatVersion uint16 = 1

// Bounds of format version 1. They are admission limits a reader checks
// before it allocates, not retail claims.
const (
	// MaxHeaderBytes bounds the encoded header, whose largest field is the
	// configuration.
	MaxHeaderBytes = 4 << 20
	// MaxConfigBytes is the battle configuration's own ceiling
	// (DESIGN_MULTIPLAYER §8.6).
	MaxConfigBytes = 2 << 20
	// ChunkTargetBytes is the raw size at which a Writer seals a chunk on
	// its own. A chunk may exceed it by one entry.
	ChunkTargetBytes = 64 << 10
	// MaxChunkRawBytes bounds a chunk's raw size: the target plus the
	// largest entry, a command at netproto.MaxCommandBytes, rounded up.
	MaxChunkRawBytes = 8 << 20
	// MaxChunkCompressedBytes bounds a chunk's compressed size: DEFLATE adds
	// a few bytes per stored block to incompressible input.
	MaxChunkCompressedBytes = MaxChunkRawBytes + 64<<10
	// MaxSeats bounds the header's seat list, the configuration's seat
	// count.
	MaxSeats = 10
	// maxMapNameBytes, maxNicknameBytes and maxNameBytes are the
	// configuration's own text bounds (§8.6).
	maxMapNameBytes  = 1024
	maxNicknameBytes = 16
	maxNameBytes     = 255
	// maxSeat is the highest seat number, ten seats.
	maxSeat = 9
	// maxColor is the highest player colour.
	maxColor = 9
)

// The refusals a caller can tell apart with errors.Is.
var (
	// ErrUnsupportedVersion: the file is a replay in a format version this
	// build does not read.
	ErrUnsupportedVersion = errors.New("nanolathe: replay format version not supported")
	// ErrCorrupt: the bytes are not a replay, or a whole part of one is
	// malformed.
	ErrCorrupt = errors.New("nanolathe: replay file is damaged")
	// ErrTruncated: the recording stops without its end entry, after its
	// last whole chunk. Everything before it plays.
	ErrTruncated = errors.New("nanolathe: replay stops without its end entry")
	// ErrIncompatible: this install cannot compose the recorded battle — its
	// content, map, mod, rules or configuration identity differs.
	ErrIncompatible = errors.New("nanolathe: replay needs other content, mod or rules")
	// ErrMismatch: playback computed another state than the recording did.
	ErrMismatch = errors.New("nanolathe: replay playback diverged from the recording")
)

// formatError is the one diagnostic shape of a refused file.
func formatError(path, expected string) error {
	return fmt.Errorf("%w: logical path %s, providers searched [replay format v%d], expected %s", ErrCorrupt, path, FormatVersion, expected)
}

// Kind is what a replay records, the header's kind byte. The configuration's
// own session kind (§8.6 field 1) knows only online battles, so a replay
// names single-player battles itself.
type Kind uint8

// The kinds of format version 1.
const (
	KindSkirmish       Kind = 1 // a single-player skirmish
	KindSurvival       Kind = 2 // a single-player Survival battle
	KindOnlineSkirmish Kind = 3 // one seat's recording of an online skirmish
	KindOnlineSurvival Kind = 4 // one seat's recording of online Survival
)

// Online reports whether the replay records a relay stream.
func (k Kind) Online() bool { return k == KindOnlineSkirmish || k == KindOnlineSurvival }

// Survival reports whether the replay records a Survival battle.
func (k Kind) Survival() bool { return k == KindSurvival || k == KindOnlineSurvival }

func (k Kind) valid() bool { return k >= KindSkirmish && k <= KindOnlineSurvival }

// commandContext is the codec context the kind's command payloads use.
func (k Kind) commandContext() session.CommandContext {
	if k.Online() {
		return session.OnlineCommand
	}
	return session.SinglePlayerReplay
}

// configKind is the configuration session kind a replay kind composes.
func (k Kind) configKind() session.MatchSessionKind {
	if k.Survival() {
		return session.MatchOnlineSurvival
	}
	return session.MatchOnlineSkirmish
}

// kindFor is the replay kind of a configuration recorded online or not.
func kindFor(config session.MatchSessionKind, online bool) (Kind, bool) {
	switch {
	case config == session.MatchOnlineSkirmish && !online:
		return KindSkirmish, true
	case config == session.MatchOnlineSurvival && !online:
		return KindSurvival, true
	case config == session.MatchOnlineSkirmish:
		return KindOnlineSkirmish, true
	case config == session.MatchOnlineSurvival:
		return KindOnlineSurvival, true
	}
	return 0, false
}

func (k Kind) String() string {
	switch k {
	case KindSkirmish:
		return "skirmish"
	case KindSurvival:
		return "survival"
	case KindOnlineSkirmish:
		return "online skirmish"
	case KindOnlineSurvival:
		return "online survival"
	}
	return fmt.Sprintf("kind %d", uint8(k))
}

// EndReason is why a recording ended, the end entry's reason byte.
type EndReason uint8

// The end reasons of format version 1.
const (
	// EndFinished: the battle reached its end.
	EndFinished EndReason = 1
	// EndLeft: the player left before the battle ended.
	EndLeft EndReason = 2
	// EndStopped: the recording stopped early because the battle could no
	// longer be recorded exactly (Writer.Stop); it plays to that point.
	EndStopped EndReason = 3
	// EndRoomFailed: an online battle's connection or room failed, a desync
	// included.
	EndRoomFailed EndReason = 4
)

func (r EndReason) valid() bool { return r >= EndFinished && r <= EndRoomFailed }

func (r EndReason) String() string {
	switch r {
	case EndFinished:
		return "finished"
	case EndLeft:
		return "left"
	case EndStopped:
		return "recording stopped"
	case EndRoomFailed:
		return "room failed"
	}
	return fmt.Sprintf("reason %d", uint8(r))
}

// Header is what a replay records before its stream: what the battle is,
// what composes it and who played, for playback and for a list screen.
type Header struct {
	Kind Kind
	// Config is the battle's encoded configuration (session.EncodeMatchConfig)
	// verbatim; playback decodes and freezes exactly these bytes.
	Config []byte
	// Identity is the recording seat's match identity (DESIGN_MULTIPLAYER
	// §8.2). Build is advisory, as online. Content is the frozen simulation
	// inputs' digest (session.Session.SimulationContentDigest). A zero
	// Content or Map means the recording host could not establish it, and
	// playback does not compare it.
	Identity netproto.Identity
	// InitialChecksum is the unit checksum of the battle prepared for its
	// first tick (session.Session.UnitStateChecksum at tick 0).
	InitialChecksum [32]byte
	// LocalSeat is the recording seat: the single-player human, or the
	// online seat whose stream this is. Playback draws from its perspective
	// by default.
	LocalSeat uint8
	// MapName is the configuration's map, for the list screen.
	MapName string
	// Seats are the configuration's seats in slot order, for the list
	// screen.
	Seats []Seat
	// Started is when the battle started, Unix time in milliseconds, or zero
	// when unknown. It is host time and never reaches a tick.
	Started int64
}

// Seat is one seat as a list screen shows it.
type Seat struct {
	Name  string
	Side  string
	Color uint8
	Role  session.MatchRole
}

// clone deep-copies the header's slices.
func (h Header) clone() Header {
	h.Config = append([]byte(nil), h.Config...)
	h.Seats = append([]Seat(nil), h.Seats...)
	return h
}

// encodeHeader validates h and writes it in the version-1 header layout:
// kind u8; configuration size u32 and bytes; the identity — protocol u16,
// build, content and map digests, rule set name key, rule base u8,
// Community digest, mod id text(255), mod version text(255), mod archive
// digest, configuration digest; the initial checksum digest; local seat u8;
// map name text(1024); seat count u32 (at most 10) and per seat its name
// text(16), side text(255), colour u8 and role u8; started s64.
func encodeHeader(h Header) ([]byte, error) {
	if err := validateHeader(h); err != nil {
		return nil, err
	}
	var w netproto.Writer
	w.U8(uint8(h.Kind))
	w.U32(uint32(len(h.Config)))
	w.Raw(h.Config)
	id := &h.Identity
	w.U16(id.Protocol)
	w.Digest(id.Build)
	w.Digest(id.Content)
	w.Digest(id.Map)
	w.Key(id.Rules.Name)
	w.U8(id.Rules.Base)
	w.Digest(id.Rules.Community)
	w.Text(id.Mod.ID)
	w.Text(id.Mod.Version)
	w.Digest(id.Mod.Archive)
	w.Digest(id.Configuration)
	w.Digest(h.InitialChecksum)
	w.U8(h.LocalSeat)
	w.Text(h.MapName)
	w.U32(uint32(len(h.Seats)))
	for _, p := range h.Seats {
		w.Text(p.Name)
		w.Text(p.Side)
		w.U8(p.Color)
		w.U8(uint8(p.Role))
	}
	w.S64(h.Started)
	if w.Len() > MaxHeaderBytes {
		return nil, formatError("header", fmt.Sprintf("at most %d bytes", MaxHeaderBytes))
	}
	return w.Bytes(), nil
}

// validateHeader refuses every value decodeHeader would refuse, so a header
// a Writer accepts always reads back.
func validateHeader(h Header) error {
	text := func(path, s string, max int) error {
		if len(s) > max || !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 {
			return formatError(path, fmt.Sprintf("valid UTF-8 without NUL, at most %d bytes", max))
		}
		return nil
	}
	switch {
	case !h.Kind.valid():
		return formatError("header.kind", "1 skirmish, 2 survival, 3 online skirmish or 4 online survival")
	case len(h.Config) == 0 || len(h.Config) > MaxConfigBytes:
		return formatError("header.config", fmt.Sprintf("an encoded configuration of 1..%d bytes", MaxConfigBytes))
	case len(h.Identity.Rules.Name) == 0 || len(h.Identity.Rules.Name) > netproto.MaxKeyBytes:
		return formatError("header.identity.rules.name", fmt.Sprintf("a rule set name of 1..%d bytes", netproto.MaxKeyBytes))
	case h.LocalSeat > maxSeat:
		return formatError("header.localSeat", "seat 0..9")
	case len(h.Seats) > MaxSeats:
		return formatError("header.seats", fmt.Sprintf("at most %d seats", MaxSeats))
	}
	if strings.IndexByte(h.Identity.Rules.Name, 0) >= 0 {
		return formatError("header.identity.rules.name", "a key without NUL")
	}
	for _, t := range []struct {
		path, s string
		max     int
	}{
		{"header.identity.mod.id", h.Identity.Mod.ID, maxNameBytes},
		{"header.identity.mod.version", h.Identity.Mod.Version, maxNameBytes},
		{"header.mapName", h.MapName, maxMapNameBytes},
	} {
		if err := text(t.path, t.s, t.max); err != nil {
			return err
		}
	}
	for i, p := range h.Seats {
		path := fmt.Sprintf("header.seats[%d]", i)
		if err := text(path+".name", p.Name, maxNicknameBytes); err != nil {
			return err
		}
		if err := text(path+".side", p.Side, maxNameBytes); err != nil {
			return err
		}
		if p.Color > maxColor {
			return formatError(path+".color", "colour 0..9")
		}
		if p.Role < session.MatchRoleHuman || p.Role > session.MatchRoleSurvivalAttacker {
			return formatError(path+".role", "role 1 human, 2 computer, 3 watcher or 4 Survival attacker")
		}
	}
	return nil
}

// decodeHeader reads a version-1 header, refusing every bound before it
// allocates and any byte after the last field.
func decodeHeader(b []byte) (Header, error) {
	r := netproto.NewReader(b, func(path, expected string) error { return formatError("header "+path, expected) })
	var h Header
	h.Kind = Kind(r.U8())
	if r.Err() == nil && !h.Kind.valid() {
		r.FailAt(r.Offset()-1, "kind 1 skirmish, 2 survival, 3 online skirmish or 4 online survival")
	}
	n := r.U32()
	if r.Err() == nil && (n == 0 || n > MaxConfigBytes) {
		r.Fail(fmt.Sprintf("an encoded configuration of 1..%d bytes", MaxConfigBytes))
	}
	h.Config = append([]byte(nil), r.Raw(int(n))...)
	id := &h.Identity
	id.Protocol = r.U16()
	id.Build = r.Digest()
	id.Content = r.Digest()
	id.Map = r.Digest()
	id.Rules.Name = r.Key()
	id.Rules.Base = r.U8()
	id.Rules.Community = r.Digest()
	id.Mod.ID = r.Text(maxNameBytes)
	id.Mod.Version = r.Text(maxNameBytes)
	id.Mod.Archive = r.Digest()
	id.Configuration = r.Digest()
	h.InitialChecksum = r.Digest()
	h.LocalSeat = r.U8()
	if r.Err() == nil && h.LocalSeat > maxSeat {
		r.FailAt(r.Offset()-1, "seat 0..9")
	}
	h.MapName = r.Text(maxMapNameBytes)
	// A seat row is at least four bytes: two empty texts and two bytes.
	count := r.Count(MaxSeats, 4)
	if count > 0 {
		h.Seats = make([]Seat, count)
	}
	for i := range h.Seats {
		p := &h.Seats[i]
		p.Name = r.Text(maxNicknameBytes)
		p.Side = r.Text(maxNameBytes)
		p.Color = r.U8()
		if r.Err() == nil && p.Color > maxColor {
			r.FailAt(r.Offset()-1, "colour 0..9")
		}
		p.Role = session.MatchRole(r.U8())
		if r.Err() == nil && (p.Role < session.MatchRoleHuman || p.Role > session.MatchRoleSurvivalAttacker) {
			r.FailAt(r.Offset()-1, "role 1 human, 2 computer, 3 watcher or 4 Survival attacker")
		}
	}
	h.Started = r.S64()
	if err := r.End(); err != nil {
		return Header{}, err
	}
	return h, nil
}

// streamState is what the entries so far have established. A Writer and a
// Reader advance the same state through the same checks, so a file a Writer
// produces is always one a Reader accepts.
type streamState struct {
	kind Kind
	seat uint8
	// cursor is the last tick of the last recorded pump.
	cursor uint32
	// position is the last command's stream position.
	position uint64
	// checksum and command are the last checksum's and command's ticks.
	checksum uint32
	command  uint32
	ended    bool
}

// chunkStart is the part of the state a chunk's preamble records.
type chunkStart struct {
	cursor   uint32
	position uint64
	checksum uint32
	command  uint32
}

func (s *streamState) start() chunkStart {
	return chunkStart{cursor: s.cursor, position: s.position, checksum: s.checksum, command: s.command}
}

func (s *streamState) adopt(c chunkStart) {
	s.cursor, s.position, s.checksum, s.command = c.cursor, c.position, c.checksum, c.command
}

// The checks return what the entry was expected to be, or "" to admit it.

func (s *streamState) admitCommand(tick uint32, seat uint8, position uint64, size int) string {
	switch {
	case s.ended:
		return "no entry after the end entry"
	case seat > maxSeat:
		return "seat 0..9"
	case !s.kind.Online() && seat != s.seat:
		return fmt.Sprintf("a single-player command from the local seat %d", s.seat)
	case tick <= s.cursor:
		return fmt.Sprintf("a command tick after the recorded pumps' tick %d", s.cursor)
	case tick < s.command:
		return fmt.Sprintf("a command tick at or after the previous command's tick %d", s.command)
	case position <= s.position:
		return fmt.Sprintf("a stream position after %d", s.position)
	case size < 1 || size > netproto.MaxCommandBytes:
		return fmt.Sprintf("a command payload of 1..%d bytes", netproto.MaxCommandBytes)
	}
	return ""
}

func (s *streamState) recordCommand(tick uint32, position uint64) {
	s.command, s.position = tick, position
}

func (s *streamState) admitPumps(count, ticks uint32) string {
	switch {
	case s.ended:
		return "no entry after the end entry"
	case count == 0 || ticks == 0:
		return "at least one pump of at least one tick"
	case s.kind.Online() && ticks != 1:
		return "one-tick pumps: every granted tick is its own pump"
	case uint64(s.cursor)+uint64(count)*uint64(ticks) > math.MaxUint32:
		return "pumps ending at a tick within 32 bits"
	case s.command > s.cursor+ticks:
		return fmt.Sprintf("a pump reaching the last command's tick %d", s.command)
	}
	return ""
}

func (s *streamState) pumps(count, ticks uint32) { s.cursor += count * ticks }

func (s *streamState) admitChecksum(tick uint32) string {
	switch {
	case s.ended:
		return "no entry after the end entry"
	case tick == 0 || tick < s.cursor:
		return fmt.Sprintf("a checksum tick at or after the recorded pumps' tick %d, never 0", s.cursor)
	case tick <= s.checksum:
		return fmt.Sprintf("a checksum tick after the previous checksum's tick %d", s.checksum)
	}
	return ""
}

func (s *streamState) admitEnd(final uint32, reason EndReason) string {
	switch {
	case s.ended:
		return "one end entry"
	case final != s.cursor:
		return fmt.Sprintf("a final tick equal to the recorded pumps' tick %d", s.cursor)
	case !reason.valid():
		return "reason 1 finished, 2 left, 3 recording stopped or 4 room failed"
	}
	return ""
}

// Entry kinds of format version 1.
const (
	entryCommand  uint8 = 1
	entryPumps    uint8 = 2
	entryChecksum uint8 = 3
	entryEnd      uint8 = 4
)
