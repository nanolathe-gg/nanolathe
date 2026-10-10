package main

// Recording local replays (docs/DESIGN_MULTIPLAYER.md §10). Every fresh
// single-player skirmish and Survival battle the game starts, and every
// online battle a seat plays, is recorded to the replay directory; a
// displayless run or a capture records only to the file --record-replay
// names. The session reports a single-player battle's commands, pumps and
// checksums on the simulation thread, and an online seat's recorder sees the
// relay stream its driver executes; both only append to the replay.Writer in
// memory. The file writes happen here, on the host: a flusher goroutine
// drains the Writer's sealed chunks to the file about once a minute, and the
// battle's end writes the rest and renames the file into place.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/lockstep"
	"github.com/nanolathe-gg/nanolathe/internal/netproto"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
	"github.com/nanolathe-gg/nanolathe/internal/replay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/version"
)

// replayFlushInterval is how often a recording's sealed chunks reach its
// file. In the browser each flush rewrites the whole stored file
// (web/fs.js), so it stays coarse.
const replayFlushInterval = time.Minute

// replayTarget is where a recording goes: the file --record-replay names, or
// the replay directory, where it is named, pruned and listed. The zero value
// records nothing.
type replayTarget struct {
	dir  string
	path string
}

func (t replayTarget) enabled() bool { return t.dir != "" || t.path != "" }

// replaySink is the Writer's output: a queue in memory that the recording's
// file writes drain. A chunk sealed on the simulation thread appends here and
// never waits for the disk.
type replaySink struct {
	mu  sync.Mutex
	buf []byte
}

func (s *replaySink) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.buf = append(s.buf, p...)
	s.mu.Unlock()
	return len(p), nil
}

// take returns the queued bytes, handing spare's storage to the queue.
func (s *replaySink) take(spare []byte) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.buf
	s.buf = spare[:0]
	return out
}

// replayRecording is one recording's file and Writer.
type replayRecording struct {
	w    *replay.Writer
	sink replaySink
	// part is the file being written and final where it goes when the
	// recording ends; discard removes a recording that ran no tick.
	part, final string
	discard     bool

	// io serializes the file's writes between the flusher and finish.
	io      sync.Mutex
	file    *os.File
	ioErr   error
	scratch []byte

	stop, done chan struct{}
	once       sync.Once
	// ending is how the battle stands now — the reason and ticks a finish
	// would record — for a recording finished at process exit
	// (finishLiveRecordings); nil records it as left.
	ending func() (replay.EndReason, uint32)
}

// liveRecordings are the recordings not yet finished. A battle the menus
// started is not torn down when the window closes, so the process finishes
// what is left when run returns, rather than leaving .part files.
var liveRecordings struct {
	sync.Mutex
	list []*replayRecording
}

func (r *replayRecording) setLive(live bool) {
	liveRecordings.Lock()
	defer liveRecordings.Unlock()
	i := slices.Index(liveRecordings.list, r)
	switch {
	case live && i < 0:
		liveRecordings.list = append(liveRecordings.list, r)
	case !live && i >= 0:
		liveRecordings.list = slices.Delete(liveRecordings.list, i, i+1)
	}
}

// finishLiveRecordings finishes every recording still running, as its
// battle stands.
func finishLiveRecordings() {
	liveRecordings.Lock()
	list := slices.Clone(liveRecordings.list)
	liveRecordings.Unlock()
	for _, r := range list {
		// A recording nothing reports on is kept: it may hold ticks.
		reason, ticks := replay.EndLeft, ^uint32(0)
		if r.ending != nil {
			reason, ticks = r.ending()
		}
		r.finishLogged(reason, ticks)
	}
}

// startReplayRecording creates the recording's file and writes the replay's
// header to it at once, so even a battle that crashes before the first flush
// leaves a listed, playable (empty) replay. In the replay directory it first
// prunes the oldest finished replays, keeping room for this one.
func startReplayRecording(target replayTarget, header replay.Header) (*replayRecording, error) {
	rec := &replayRecording{stop: make(chan struct{}), done: make(chan struct{})}
	var err error
	if target.path != "" {
		rec.final, rec.part = target.path, target.path+replayPartExt
		rec.file, err = os.OpenFile(rec.part, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return nil, fmt.Errorf("nanolathe: creating a replay failed: logical path %s, providers searched [--record-replay], expected a writable file: %w", rec.part, err)
		}
	} else {
		if err := pruneReplays(target.dir, replayKeep-1); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		started := time.UnixMilli(header.Started)
		if header.Started == 0 {
			started = time.Now()
		}
		rec.file, rec.final, err = createReplayPart(target.dir, replayFileStem(started, header.MapName, header.Kind))
		if err != nil {
			return nil, err
		}
		rec.part, rec.discard = rec.final+replayPartExt, true
	}
	if rec.w, err = replay.NewWriter(&rec.sink, header); err == nil {
		err = rec.drain(false)
	}
	if err != nil {
		_ = rec.file.Close()
		_ = os.Remove(rec.part)
		return nil, err
	}
	markReplayActive(rec.part, true)
	rec.setLive(true)
	go rec.flushLoop()
	return rec, nil
}

// flushLoop seals and writes the recording about once a minute until finish
// or abort stops it.
func (r *replayRecording) flushLoop() {
	defer close(r.done)
	ticker := time.NewTicker(replayFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			_ = r.flush()
		}
	}
}

// flush seals what the Writer holds into a chunk and writes it to the file.
func (r *replayRecording) flush() error {
	if err := r.w.Flush(); err != nil {
		return err
	}
	return r.drain(true)
}

// drain writes the bytes the Writer has sealed to the file, and syncs it,
// which is also what stores it in the browser. The first file error is kept:
// the recording then stays in memory and ends incomplete.
func (r *replayRecording) drain(sync bool) error {
	r.io.Lock()
	defer r.io.Unlock()
	if r.ioErr != nil {
		return r.ioErr
	}
	b := r.sink.take(r.scratch)
	if len(b) > 0 {
		if _, err := r.file.Write(b); err != nil {
			r.ioErr = fmt.Errorf("nanolathe: writing a replay failed: logical path %s, providers searched [replays], expected a writable file: %w", r.part, err)
		}
	}
	r.scratch = b
	if sync && r.ioErr == nil {
		if err := r.file.Sync(); err != nil {
			r.ioErr = fmt.Errorf("nanolathe: storing a replay failed: logical path %s, providers searched [replays], expected a writable file: %w", r.part, err)
		}
	}
	return r.ioErr
}

// finish ends the recording at the battle's end: the end entry with reason
// (the Writer writes "recording stopped" instead when the battle could no
// longer be recorded exactly), the last chunk, and the rename into place. A
// recording in the replay directory that ran no tick is removed instead:
// there is nothing to watch. A file that could not be completed keeps its
// .part name and lists as incomplete. It returns the replay's path, empty
// when nothing was kept. Only the first call acts.
func (r *replayRecording) finish(reason replay.EndReason, ticks uint32) (string, error) {
	if r == nil {
		return "", nil
	}
	path := ""
	var err error
	r.once.Do(func() {
		close(r.stop)
		<-r.done
		r.setLive(false)
		defer markReplayActive(r.part, false)
		if r.discard && ticks == 0 {
			_ = r.file.Close()
			_ = os.Remove(r.part)
			return
		}
		closeErr := r.w.Close(reason)
		writeErr := r.drain(true)
		fileErr := r.file.Close()
		path = r.part
		if err = errors.Join(closeErr, writeErr, fileErr); err != nil {
			return
		}
		if err = os.Rename(r.part, r.final); err != nil {
			err = fmt.Errorf("nanolathe: finishing a replay failed: logical path %s, providers searched [replays], expected a writable directory: %w", r.final, err)
			return
		}
		path = r.final
	})
	return path, err
}

// finishLogged is finish for a battle's end: it reports a recording that
// stopped early or could not be written, and never fails the battle.
func (r *replayRecording) finishLogged(reason replay.EndReason, ticks uint32) string {
	if r == nil {
		return ""
	}
	path, err := r.finish(reason, ticks)
	if stopped := r.w.Stopped(); stopped != nil && path != "" {
		fmt.Fprintf(os.Stderr, "nanolathe: the replay %s stops early: %v\n", path, stopped)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: the replay %s could not be completed: %v\n", path, err)
	}
	return path
}

// abort removes a recording whose battle never ran.
func (r *replayRecording) abort() {
	if r == nil {
		return
	}
	r.discard = true
	_, _ = r.finish(replay.EndLeft, 0)
	if r.part != "" {
		_ = os.Remove(r.part)
	}
}

// replaySkip is a battle the game does not record, and why. A quiet skip is
// a battle no one expects a replay of (a campaign mission).
type replaySkip struct {
	reason string
	quiet  bool
}

func (s *replaySkip) Error() string {
	return "nanolathe: replay not recorded: logical path <battle>, providers searched [replay recording], expected a recordable battle: " + s.reason
}

// logReplaySkip reports why a battle is not recorded, unless the skip is
// quiet. Recording never fails a battle the player started.
func logReplaySkip(err error) {
	var skip *replaySkip
	if errors.As(err, &skip) && skip.quiet {
		return
	}
	fmt.Fprintln(os.Stderr, err)
}

// replayContentRefusal is why content cannot be described to a playback, or
// nil: a playback composes the recorded battle from the installed base game
// or an installed mod it can identify, so extra --root or --mod-config
// content, and a mod without an archive digest, are not recorded.
func replayContentRefusal(cs *contentSet) error {
	switch {
	case cs == nil || cs.fs == nil:
		return &replaySkip{reason: "no mounted content"}
	case cs.manualRoots || cs.configPath != "":
		return &replaySkip{reason: "content stacked with --root or --mod-config cannot be identified by a playback"}
	case cs.mod != nil && matchModOf(cs.mod).Archive == [32]byte{}:
		return &replaySkip{reason: "the mod " + modSelectorOf(cs.mod.ID, cs.mod.Version) + " has no archive digest to identify it by"}
	}
	return nil
}

// singlePlayerReplayRefusal is why a fresh battle request is not recorded, or
// nil: campaign missions, watched battles and battles without a human player
// have no single-player replay.
func singlePlayerReplayRefusal(request headless.FreshBattleRequest, cs *contentSet) error {
	switch request.Kind {
	case headless.ScenarioDirectOTA, headless.ScenarioSkirmish, headless.ScenarioSurvival:
	default:
		return &replaySkip{reason: "a campaign mission is not recorded", quiet: true}
	}
	switch {
	case request.Watching:
		return &replaySkip{reason: "a watched battle is not recorded"}
	case request.AutomatedPlayers:
		return &replaySkip{reason: "a battle without a human player is not recorded"}
	}
	return replayContentRefusal(cs)
}

// startSinglePlayerReplay records sess, composed from request and not yet
// ticked, to target: the configuration MatchConfigForFreshBattle describes the
// battle with goes in the header, and the session's recorder is attached
// before the first tick. A battle that cannot be recorded returns a
// *replaySkip.
func startSinglePlayerReplay(target replayTarget, request headless.FreshBattleRequest, cs *contentSet, sess *session.Session) (*replayRecording, error) {
	if err := singlePlayerReplayRefusal(request, cs); err != nil {
		return nil, err
	}
	if request.Catalog == nil && request.Restrictions.IsZero() && sess != nil && sess.Catalog != nil {
		// Without restrictions the configuration reads only the map header
		// from the catalog, and the battle's own catalog has the same maps,
		// so the battle start compiles no second catalog.
		request.Catalog = sess.Catalog
	}
	config, err := headless.MatchConfigForFreshBattle(request, session.MatchRoomInputs{Mod: matchModOf(cs.mod), ContentProfile: cs.profile})
	if err != nil {
		return nil, &replaySkip{reason: err.Error()}
	}
	header, err := replay.SinglePlayerHeader(sess, config)
	if err != nil {
		return nil, &replaySkip{reason: err.Error()}
	}
	header.Started = time.Now().UnixMilli()
	rec, err := startReplayRecording(target, header)
	if err != nil {
		return nil, err
	}
	if _, err := replay.NewSessionRecorder(rec.w, sess); err != nil {
		rec.abort()
		return nil, &replaySkip{reason: err.Error()}
	}
	return rec, nil
}

// replayTarget is where this shell's battles record: its replay directory.
func (g *gameShell) replayTarget() replayTarget {
	if g == nil {
		return replayTarget{}
	}
	return replayTarget{dir: g.opts.ReplayDir}
}

// recordFreshBattle starts recording a fresh battle the window composed, or
// logs why it does not. Recording never fails the battle.
func recordFreshBattle(target replayTarget, request headless.FreshBattleRequest, cs *contentSet, sess *session.Session) *replayRecording {
	if !target.enabled() {
		return nil
	}
	rec, err := startSinglePlayerReplay(target, request, cs, sess)
	if err != nil {
		logReplaySkip(err)
		return nil
	}
	return rec
}

// adoptReplayRecording makes rec this single-player battle's recording,
// finished when the battle is torn down.
func (b *battleSession) adoptReplayRecording(rec *replayRecording) {
	b.replay = rec
	if rec != nil {
		rec.ending = func() (replay.EndReason, uint32) {
			// At process exit a simulation batch may still be running.
			b.stopSimulation(b.cl)
			return b.replayEnding()
		}
	}
}

// replayEnding is how a single-player battle's recording ends now: finished
// when the battle reached its result, left otherwise, at its last tick.
func (b *battleSession) replayEnding() (replay.EndReason, uint32) {
	reason, ticks := replay.EndLeft, uint32(0)
	if b.sess != nil && b.sess.Clock != nil {
		ticks = b.sess.Clock.GlobalTick
		if cur, ok := b.currentSnapshot(); b.sess.State == session.StatePostBattle || ok && cur.Result.Ended {
			reason = replay.EndFinished
		}
	}
	return reason, ticks
}

// finishReplayRecording ends a single-player battle's recording at teardown,
// after the simulation goroutine has been joined.
func (b *battleSession) finishReplayRecording() {
	rec := b.replay
	b.replay = nil
	if rec != nil {
		rec.finishLogged(b.replayEnding())
	}
}

// onlineReplayClient is the lockstep.Client an online battle's driver reads
// through while this seat records the relay stream it executes
// (replay.OnlineRecorder). Closing it — the driver closes its client when the
// battle ends, is left or fails — finishes the recording: finished at the
// shared end, room failed after a transport error, left otherwise.
type onlineReplayClient struct {
	*replay.OnlineRecorder
	rec          *replayRecording
	openingReady func() error

	mu     sync.Mutex
	acked  uint32
	ended  bool
	failed bool
}

// OpeningReady forwards the presentation barrier without recording an entry.
// A failed marker is a transport failure just like a failed acknowledgment.
func (c *onlineReplayClient) OpeningReady() error {
	if c.openingReady == nil {
		return nil
	}
	err := c.openingReady()
	if err != nil {
		c.mu.Lock()
		c.failed = true
		c.mu.Unlock()
	}
	return err
}

// ReadGrant notes a transport failure; the relay's explicit completion is
// io.EOF.
func (c *onlineReplayClient) ReadGrant() (relay.LocalGrant, error) {
	g, err := c.OnlineRecorder.ReadGrant()
	if err != nil && !errors.Is(err, io.EOF) {
		c.mu.Lock()
		c.failed = true
		c.mu.Unlock()
	}
	return g, err
}

// Acknowledge records the acknowledged tick and notes the shared end.
func (c *onlineReplayClient) Acknowledge(tick uint32, checksum [32]byte, ended, final bool) error {
	err := c.OnlineRecorder.Acknowledge(tick, checksum, ended, final)
	c.mu.Lock()
	c.acked, c.ended = tick, c.ended || ended
	c.failed = c.failed || err != nil
	c.mu.Unlock()
	return err
}

// Close finishes the recording, then closes the relay client.
func (c *onlineReplayClient) Close() error {
	c.rec.finishLogged(c.ending())
	return c.OnlineRecorder.Close()
}

// ending is how the stream ends now: finished at the shared end, room failed
// after a transport error, left otherwise, at the last acknowledged tick.
func (c *onlineReplayClient) ending() (replay.EndReason, uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.ended:
		return replay.EndFinished, c.acked
	case c.failed:
		return replay.EndRoomFailed, c.acked
	}
	return replay.EndLeft, c.acked
}

// onlineReporter is a relay client that reports its traffic and the relay's
// progress, which the online overlay type-asserts (online_net.go).
type onlineReporter interface {
	onlineTrafficReporter
	onlineProgressReporter
}

// reportingOnlineReplayClient keeps those reports visible through the
// recorder when the wrapped client has them.
type reportingOnlineReplayClient struct {
	*onlineReplayClient
	reporter onlineReporter
}

func (c reportingOnlineReplayClient) Traffic() relay.LocalTraffic { return c.reporter.Traffic() }
func (c reportingOnlineReplayClient) Progress() (relay.HostedMatchProgress, bool) {
	return c.reporter.Progress()
}

// startOnlineReplay wraps conn so this seat records the online battle sess,
// composed for config and prepared, with the identity it joined with. The
// returned client keeps conn's traffic and progress reports.
func startOnlineReplay(target replayTarget, cs *contentSet, sess *session.Session, config session.EffectiveMatchConfig, identity netproto.Identity, conn lockstep.Client) (lockstep.Client, error) {
	if conn == nil {
		return nil, &replaySkip{reason: "no relay client"}
	}
	if err := replayContentRefusal(cs); err != nil {
		return nil, err
	}
	header, err := replay.OnlineHeader(sess, config, identity)
	if err != nil {
		return nil, &replaySkip{reason: err.Error()}
	}
	header.Started = time.Now().UnixMilli()
	rec, err := startReplayRecording(target, header)
	if err != nil {
		return nil, err
	}
	recorder, err := replay.NewOnlineRecorder(rec.w, conn)
	if err != nil {
		rec.abort()
		return nil, &replaySkip{reason: err.Error()}
	}
	client := &onlineReplayClient{OnlineRecorder: recorder, rec: rec}
	if opening, ok := conn.(interface{ OpeningReady() error }); ok {
		client.openingReady = opening.OpeningReady
	}
	rec.ending = client.ending
	if reporter, ok := conn.(onlineReporter); ok {
		return reportingOnlineReplayClient{onlineReplayClient: client, reporter: reporter}, nil
	}
	return client, nil
}

// recordOnlineReplay is the online lobby's recording hook: it wraps conn, the
// relay client of the battle p prepared, so this seat records the battle to
// the replay directory, and returns conn itself when the battle is not
// recorded. The identity is the one the seat joined with, recomputed from
// the prepared inputs and checked against the digest the lobby reported.
// The online battle start calls it before it builds the driver.
func (g *gameShell) recordOnlineReplay(p *onlinePrepared, conn lockstep.Client) lockstep.Client {
	target := g.replayTarget()
	if !target.enabled() || p == nil || p.sess == nil || conn == nil {
		return conn
	}
	build, _ := version.CurrentBuildManifest()
	identity, err := (session.MatchJoin{Build: session.MatchBuild{Running: build}, Inputs: p.inputs, Config: p.config, Mod: matchModOf(g.cs.mod)}).Identity()
	if err == nil && onlineIdentityDigest(identity) != p.identity {
		err = &replaySkip{reason: "the battle's identity is not the one this seat joined with"}
	}
	var client lockstep.Client
	if err == nil {
		client, err = startOnlineReplay(target, g.cs, p.sess, p.config, identity, conn)
	}
	if err != nil {
		logReplaySkip(err)
		return conn
	}
	return client
}

// recordHostedReplay is the command-line hosted play test's recording hook
// (battle_hosted.go): the configuration is the fixed play-test one the
// battle was composed from, which the recorder checks against identity.
func (b *battleSession) recordHostedReplay(o Options, identity netproto.Identity, conn lockstep.Client) lockstep.Client {
	target := b.shell.replayTarget()
	if !target.enabled() || b.sess == nil || b.cs == nil {
		return conn
	}
	schema, err := session.OnlineMapSchema(b.cs.fs, b.sess.Catalog, o.Map, 2)
	var config session.EffectiveMatchConfig
	if err == nil {
		config, err = localMultiplayerConfig(o, b.cs, schema)
	}
	var client lockstep.Client
	if err == nil {
		client, err = startOnlineReplay(target, b.cs, b.sess, config, identity, conn)
	}
	if err != nil {
		logReplaySkip(err)
		return conn
	}
	return client
}
