package camera

import "testing"

func TestMinimapLetterbox(t *testing.T) { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// wide: mapW > mapH
	m := LayoutMinimap(640, 480)
	if m.W != 126 {
		t.Fatalf("wide W want 126 got %d", m.W)
	}
	wantH := int32(int64(480) * 126 / int64(640)) // floor [07 §10]
	if m.H != wantH {
		t.Fatalf("wide H want %d got %d", wantH, m.H)
	}
	if m.PadX != 0 {
		t.Fatalf("wide PadX want 0 got %d", m.PadX)
	}
	wantPadY := (126 - wantH) / 2
	if m.PadY != wantPadY {
		t.Fatalf("wide PadY want %d got %d", wantPadY, m.PadY)
	}
	if m.Right() != m.PadX+m.W-1 || m.Bottom() != m.PadY+m.H-1 {
		t.Fatalf("wide inclusive right/bottom got %d %d", m.Right(), m.Bottom())
	}
	// tall: mapW < mapH
	m = LayoutMinimap(480, 640)
	wantW := int32(int64(480) * 126 / int64(640))
	if m.W != wantW || m.H != 126 {
		t.Fatalf("tall W/H want %d/126 got %d/%d", wantW, m.W, m.H)
	}
	wantPadX := (126 - wantW) / 2
	if m.PadX != wantPadX || m.PadY != 0 {
		t.Fatalf("tall Pad want %d/0 got %d/%d", wantPadX, m.PadX, m.PadY)
	}
	// square: mapW == mapH
	m = LayoutMinimap(512, 512)
	if m.W != 126 || m.H != 126 || m.PadX != 0 || m.PadY != 0 {
		t.Fatalf("square want 126x126 0,0 got %dx%d %d,%d", m.W, m.H, m.PadX, m.PadY)
	}
	// letterbox bars are fill inference 0 black [03 §3.6] TODO(question)
	if minimapLetterboxFill != 0 {
		t.Fatalf("letterbox fill inference want 0 black got %d", minimapLetterboxFill)
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question) ALP quadrant order is supported inference, not exercised in lens package.
}

func TestHitTestInclusive(t *testing.T) { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	m := LayoutMinimap(640, 480) // wide: PadX 0, PadY (126-H)/2
	// inside inclusive rect
	if !m.HitTest(m.PadX, m.PadY) {
		t.Fatalf("HitTest top-left inclusive want true")
	}
	if !m.HitTest(m.Right(), m.Bottom()) {
		t.Fatalf("HitTest bottom-right inclusive want true got Right %d Bottom %d", m.Right(), m.Bottom())
	}
	if !m.HitTest(m.PadX+m.W/2, m.PadY+m.H/2) {
		t.Fatalf("HitTest center want true")
	}
	// outside by one
	if m.HitTest(m.PadX-1, m.PadY) {
		t.Fatalf("HitTest left-1 want false")
	}
	if m.HitTest(m.Right()+1, m.PadY) {
		t.Fatalf("HitTest right+1 want false")
	}
	if m.HitTest(m.PadX, m.PadY-1) {
		t.Fatalf("HitTest top-1 want false")
	}
	if m.HitTest(m.PadX, m.Bottom()+1) {
		t.Fatalf("HitTest bottom+1 want false")
	}
	// tall map variant: PadX non-zero
	m2 := LayoutMinimap(480, 640)
	if !m2.HitTest(m2.PadX, m2.PadY) || !m2.HitTest(m2.Right(), m2.Bottom()) {
		t.Fatalf("HitTest tall inclusive failed")
	}
	if m2.HitTest(m2.PadX-1, m2.PadY) || m2.HitTest(m2.Right()+1, m2.Bottom()) {
		t.Fatalf("HitTest tall outside want false")
	}
	// 126x126 square has full canvas hit
	m3 := LayoutMinimap(512, 512)
	if !m3.HitTest(0, 0) || !m3.HitTest(125, 125) || m3.HitTest(126, 0) || m3.HitTest(0, 126) {
		t.Fatalf("HitTest square 126 canvas failed: Pad %d,%d W%d H%d", m3.PadX, m3.PadY, m3.W, m3.H)
	}
}

func TestWorldToRadarRadarToWorldRoundTrip(t *testing.T) { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	cases := []struct {
		mapW, mapH   int32
		playW, playH int32
	}{
		{640, 480, PlayRight(640), PlayBottom(480)}, // raw pix 640,480 -> play 608,352 [03 §3.4]
		{1024, 1024, 992, 896},                      // square
		{480, 640, 448, 512},
		{800, 600, 768, 472},
	}
	for _, c := range cases {
		m := LayoutMinimap(c.mapW, c.mapH)
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// use playW/playH as divisors per minimap §4, §8.
		pts := []struct{ wx, wz int32 }{
			{0, 0},
			{c.playW - 1, c.playH - 1},
			{c.playW / 2, c.playH / 2},
			{c.playW / 4, c.playH / 3},
			{1, 1},
			{100, 200},
		}
		for _, p := range pts {
			rx, ry := m.WorldToRadar(p.wx, p.wz, c.playW, c.playH)
			// radar canvas must be inside inclusive rect when world inside play
			if !m.HitTest(rx, ry) {
				t.Fatalf("world (%d,%d) -> radar (%d,%d) outside rect %+v play %dx%d map %dx%d", p.wx, p.wz, rx, ry, m, c.playW, c.playH, c.mapW, c.mapH)
			}
			wx2, wz2 := m.RadarToWorld(rx, ry, c.playW, c.playH)
			dx := wx2 - p.wx
			if dx < 0 {
				dx = -dx
			}
			dz := wz2 - p.wz
			if dz < 0 {
				dz = -dz
			}
			// TRUNC both ways: world->radar trunc then radar->world trunc.
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// For lens where Play>>126, ±1 world pixel is not achievable; bound scales.
			allowX := c.playW/m.W + 1
			if m.W == 0 {
				allowX = 1
			}
			allowZ := c.playH/m.H + 1
			if m.H == 0 {
				allowZ = 1
			}
			if dx > allowX || dz > allowZ {
				t.Fatalf("world->radar->world trunc error >allow (%d,%d): map %dx%d play %dx%d world (%d,%d) -> radar (%d,%d) -> world (%d,%d) delta %d,%d", allowX, allowZ, c.mapW, c.mapH, c.playW, c.playH, p.wx, p.wz, rx, ry, wx2, wz2, dx, dz)
			}
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			rx2, ry2 := m.WorldToRadar(wx2, wz2, c.playW, c.playH)
			drx := rx2 - rx
			if drx < 0 {
				drx = -drx
			}
			dry := ry2 - ry
			if dry < 0 {
				dry = -dry
			}
			if drx > 1 || dry > 1 {
				t.Fatalf("radar->world->radar trunc error >1: radar (%d,%d) -> world (%d,%d) -> radar (%d,%d) delta %d,%d", rx, ry, wx2, wz2, rx2, ry2, drx, dry)
			}
			// also test ToWorldPlay alias matches RadarToWorld
			wx3, wz3 := m.ToWorldPlay(rx, ry, c.playW, c.playH)
			if wx3 != wx2 || wz3 != wz2 {
				t.Fatalf("ToWorldPlay mismatch RadarToWorld: %d,%d vs %d,%d", wx3, wz3, wx2, wz2)
			}
			// height-aware variant with Y=0 matches plain
			rx3, ry3 := m.WorldToRadarWithY(p.wx, 0, p.wz, c.playW, c.playH)
			if rx3 != rx || ry3 != ry {
				t.Fatalf("WorldToRadarWithY Y=0 mismatch: %d,%d vs %d,%d", rx3, ry3, rx, ry)
			}
		}
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		m2 := m
		wx, wy, wz := int32(200), int32(40), int32(300)
		rx, ry := m2.WorldToRadarWithY(wx, wy, wz, c.playW, c.playH)
		// ry should be (wz - wy>>1) scaled
		wantRy := m2.PadY + int32(int64(wz-(wy>>1))*int64(m2.H)/int64(c.playH))
		wantRx := m2.PadX + int32(int64(wx)*int64(m2.W)/int64(c.playW))
		if rx != wantRx || ry != wantRy {
			t.Fatalf("WorldToRadarWithY shear want %d,%d got %d,%d", wantRx, wantRy, rx, ry)
		}
	}
}

func TestToCameraPlayClamp(t *testing.T) { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	playW, playH := PlayRight(640), PlayBottom(480) // 608,352
	m := LayoutMinimap(640, 480)
	viewW, viewH := int32(128), int32(128)
	// center click should center camera
	rx := m.PadX + m.W/2
	ry := m.PadY + m.H/2
	cx, cz := m.ToCameraPlay(rx, ry, playW, playH, viewW, viewH)
	wx, wz := m.ToWorldPlay(rx, ry, playW, playH)
	wantCx := clampAxis(wx-viewW/2, playW, viewW)
	wantCz := clampAxis(wz-viewH/2, playH, viewH)
	if cx != wantCx || cz != wantCz {
		t.Fatalf("ToCameraPlay center want %d,%d got %d,%d", wantCx, wantCz, cx, cz)
	}
	// corner 0,0 click with view larger than play clamps per clampAxis order [07 §10]
	cx2, cz2 := m.ToCameraPlay(m.PadX, m.PadY, playW, playH, 800, 600)
	if cx2 != clampAxis(-400, playW, 800) || cz2 != clampAxis(-300, playH, 600) {
		t.Fatalf("ToCameraPlay clamp failed got %d,%d", cx2, cz2)
	}
}

func TestPlayHelpers(t *testing.T) { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if PlayRight(640) != 608 || PlayBottom(480) != 352 {
		t.Fatalf("PlayRight/Bottom want 608/352 got %d/%d", PlayRight(640), PlayBottom(480))
	}
	w, h := PlaySize(1024, 768)
	if w != 992 || h != 640 {
		t.Fatalf("PlaySize want 992/640 got %d/%d", w, h)
	}
	// cells*16 path
	pw, ph := PlaySizeFromCells(64, 64) // 64*16=1024
	if pw != 992 || ph != 896 {         // 1024-32, 1024-128
		t.Fatalf("PlaySizeFromCells want 992/896 got %d/%d", pw, ph)
	}
}

func TestMinimapLetterboxFillTODO(t *testing.T) { // TODO(question) 0 black [03 §3.6]
	// Letterbox bars beyond RadarW×RadarH retain heap bytes — inference 0 black pending capture.
	// This test locks the current inference so review knows it is deliberate.
	if MinimapLongSide != 126 {
		t.Fatalf("MinimapLongSide want 126 got %d", MinimapLongSide)
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// lens conversions are integer TRUNC only.
}
