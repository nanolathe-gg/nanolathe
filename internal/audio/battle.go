package audio

// SetAcknowledgementClock binds the Nanolathe host's 30 Hz update identity.
// Only the queue pop is limited; committed events are delivered on every
// presentation. Cooldowns still use the committed global tick [03 §8.3].
// This host policy is documented in DESIGN_PRESENTATION_CLIENT C18.
func (a *Service) SetAcknowledgementClock(now func() uint64) {
	if a != nil {
		a.ackClock = now
		a.hasAckOpportunity = false
	}
}

func (a *Service) admitAcknowledgement() bool {
	if a.ackClock == nil {
		return true
	}
	now := a.ackClock()
	if a.hasAckOpportunity && now == a.ackOpportunity {
		return false
	}
	a.ackOpportunity, a.hasAckOpportunity = now, true
	return true
}

type battleIntensity struct {
	buckets     [30]int32
	current     int
	evals       int32
	stamp       uint32
	active      bool
	initialized bool
	lastWant    int32
}

// StartBattleMusic resets the battle-only intensity ring and requests Building.
// The existing media pump handles device availability [03 R-AUD-01 §5].
func (a *Service) StartBattleMusic() {
	if a == nil || a.Music == nil {
		return
	}
	i := &a.intensity
	if !i.initialized {
		i.lastWant, i.initialized = -1, true
	}
	i.buckets, i.current, i.active = [30]int32{}, 0, true
	a.Music.SetDesired(0)
	// SetDesired can already start physical media or register a transition.
	// Do not inject another tick into that work. A modeled playing status
	// without a player still needs the deferred desktop-device handoff.
	c := a.Music
	a.musicStartPending = c.fadeTimer < 0 && c.delayTimer < 0 &&
		(c.player == nil || !c.player.IsPlaying())
}

// UpdateBattleMusic evaluates once when more than 30 scaled units elapsed.
// A long host stall advances only one bucket; it does not invent intervening
// evaluations. Ended is the host's committed battle-over mapping; teardown
// handles exit by requesting silence [03 R-AUD-01 §5].
func (a *Service) UpdateBattleMusic(localUnits int, ended bool) {
	if a == nil || !a.intensity.active || a.Music == nil || a.Music.clock == nil || ended {
		return
	}
	now := a.Music.clock()
	i := &a.intensity
	if now <= i.stamp+30 {
		return
	}
	i.evals++
	want := i.lastWant
	desired := a.Music.DesiredCategory()
	if i.evals > 10 {
		var sum30, sum5 int32
		for age := range i.buckets {
			points := i.buckets[(i.current+len(i.buckets)-1-age)%len(i.buckets)]
			sum30 += points
			if age < 5 {
				sum5 += points
			}
		}
		if desired == 0 && (sum30 > 50 || sum5 > 30) && uint16(localUnits) > 30 {
			want = 1
		} else if desired == 1 && sum30 < 10 && sum5 == 0 && i.evals > 60 {
			want = 0
		}
	}
	if want != i.lastWant {
		a.Music.SetDesired(want)
		i.lastWant = want
		i.evals = 0
	}
	i.current = (i.current + 1) % len(i.buckets)
	i.buckets[i.current] = 0
	i.stamp = a.Music.clock()
}

// EndBattleMusic transfers the live controller to the shell's continuing
// music pump. In Custom mode category 4 completes its ordinary fade before
// stopping; closing the service here would cut that fade short [03 R-AUD-01 §4].
//
// Every other mode's category request only records the category and returns,
// so nothing would reach the tick until the playing track ended. Battle exit
// and the front-end return request silence, which the tick applies in any mode
// [03 R-AUD-01 §4 tick step 2][03 R-AUD-01 §5], so run it here.
func (a *Service) EndBattleMusic() {
	if a == nil {
		return
	}
	a.intensity.active = false
	a.musicStartPending = false
	a.StopStream()
	a.StopVoices()
	if c := a.Music; c != nil {
		c.SetDesired(4)
		if c.playMode != ModeCategoryShuffle {
			c.tickFromMedia()
		}
	}
}
