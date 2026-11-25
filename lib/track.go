package lib

// Identities and framing. Detections come in per frame with no names;
// the tracker keeps each person's ID from frame to frame (match by overlap
// with where they were heading), so "the pastor" stays the same person.
// Framing turns the people a mode cares about into the shot it would aim
// for, in the camera's current picture (0..1; outside it means pan or zoom
// out).

import (
	"math"
	"sort"
	"time"
)

type Track struct {
	ID     int          `json:"id"`
	Box    [4]float64   `json:"box"`
	Points [][3]float64 `json:"-"`
	Score  float64      `json:"score"`
	vel    [4]float64   // per second
	hits   int
	miss   int
	seen   time.Time
}

func (t *Track) Confirmed() bool { return t.hits >= 3 }

// Tracker keeps identities for one camera.
type Tracker struct {
	next   int
	tracks []*Track
	last   time.Time
}

// Update matches this frame's people to the known tracks and returns the
// tracks seen in this frame (confirmed ones only).
func (tr *Tracker) Update(ps []Person, at time.Time) []*Track {
	ps = dedupe(ps)
	dt := 0.0
	if !tr.last.IsZero() {
		dt = at.Sub(tr.last).Seconds()
	}
	tr.last = at
	pred := make([][4]float64, len(tr.tracks))
	for i, t := range tr.tracks {
		for k := 0; k < 4; k++ {
			pred[i][k] = t.Box[k] + t.vel[k]*dt
		}
	}
	type pair struct {
		ti, pi int
		s      float64
	}
	var cand []pair
	for ti := range tr.tracks {
		for pi, p := range ps {
			s := iou(pred[ti], p.Box)
			if s < 0.15 { // small or fast: fall back to centre distance
				d := centerDist(pred[ti], p.Box)
				if d < 0.08 {
					s = 0.15 - d
				}
			}
			if s > 0.05 {
				cand = append(cand, pair{ti, pi, s})
			}
		}
	}
	sort.Slice(cand, func(i, j int) bool { return cand[i].s > cand[j].s })
	usedT := map[int]bool{}
	usedP := map[int]bool{}
	for _, c := range cand {
		if usedT[c.ti] || usedP[c.pi] {
			continue
		}
		usedT[c.ti], usedP[c.pi] = true, true
		t, p := tr.tracks[c.ti], ps[c.pi]
		for k := 0; k < 4; k++ {
			nb := 0.65*p.Box[k] + 0.35*pred[c.ti][k]
			if dt > 0 {
				t.vel[k] = 0.6*t.vel[k] + 0.4*(nb-t.Box[k])/dt
			}
			t.Box[k] = nb
		}
		t.Points, t.Score = p.Points, p.Score
		t.hits++
		t.miss = 0
		t.seen = at
	}
	keep := tr.tracks[:0]
	for ti, t := range tr.tracks {
		if !usedT[ti] {
			t.miss++
			t.vel = [4]float64{}
		}
		if t.miss <= 20 && (t.hits >= 3 || t.miss <= 2) {
			keep = append(keep, t)
		}
	}
	tr.tracks = keep
	for pi, p := range ps {
		if !usedP[pi] && p.Score >= 0.5 {
			tr.next++
			tr.tracks = append(tr.tracks, &Track{ID: tr.next, Box: p.Box, Points: p.Points, Score: p.Score, hits: 1, seen: at})
		}
	}
	var out []*Track
	for _, t := range tr.tracks {
		if t.Confirmed() && t.miss == 0 {
			out = append(out, t)
		}
	}
	return out
}

// dedupe drops a detection that mostly sits inside another one (a
// person and "person + pulpit" boxes on one body): the less sure of the
// two goes.
func dedupe(ps []Person) []Person {
	drop := make([]bool, len(ps))
	for i := range ps {
		for j := range ps {
			if i == j || drop[i] || drop[j] {
				continue
			}
			a, b := ps[i].Box, ps[j].Box // is b mostly inside a?
			ix := math.Max(0, math.Min(a[2], b[2])-math.Max(a[0], b[0]))
			iy := math.Max(0, math.Min(a[3], b[3])-math.Max(a[1], b[1]))
			if ab := area(b); ab > 0 && ix*iy/ab > 0.8 {
				if ps[i].Score < ps[j].Score {
					drop[i] = true
				} else {
					drop[j] = true
				}
			}
		}
	}
	out := ps[:0:0]
	for i, p := range ps {
		if !drop[i] {
			out = append(out, p)
		}
	}
	return out
}

// Find returns the visible track under a point, smallest first.
func Find(ts []*Track, x, y float64) *Track {
	var best *Track
	for _, t := range ts {
		if x >= t.Box[0] && x <= t.Box[2] && y >= t.Box[1] && y <= t.Box[3] {
			if best == nil || area(t.Box) < area(best.Box) {
				best = t
			}
		}
	}
	return best
}

func centerDist(a, b [4]float64) float64 {
	return math.Hypot((a[0]+a[2]-b[0]-b[2])/2, (a[1]+a[3]-b[1]-b[3])/2)
}

func area(b [4]float64) float64 { return (b[2] - b[0]) * (b[3] - b[1]) }

// ---------------------------------------------------------------- framing

const (
	ModeFollow = "follow"
	ModePair   = "pair"
	ModeGroup  = "group"
	ModeHold   = "hold"

	ShotClose = "close"
	ShotWaist = "waist"
	ShotWide  = "wide"
)

// Framing is what a mode would do with the people in view.
type Framing struct {
	Subjects []int      `json:"subjects"`       // track IDs in the shot
	Aim      [4]float64 `json:"aim"`            // the shot it would frame, in this picture
	Move     string     `json:"move"`           // in words: "pan right 12% · zoom in 1.6×"
	Holding  bool       `json:"holding"`        // close enough: no move
	Lost     bool       `json:"lost,omitempty"` // nobody to frame
}

// inZone: is the person standing in the stage zone? Judged by where
// their feet (the bottom of the box) are, so someone walking in front of
// the stage, whose body overlaps it, doesn't count.
func inZone(b, z [4]float64) bool {
	cx, fy := (b[0]+b[2])/2, b[3]
	return cx >= z[0] && cx <= z[2] && fy >= z[1] && fy <= z[3]
}

// Frame works out the shot for a mode. target is the tapped person (0 =
// none). The picture is 16:9, so a 16:9 shot is as wide as it is tall in
// these 0..1 units.
func FrameShot(ts []*Track, mode, shot string, zone [4]float64, target int) Framing {
	if mode == ModeHold {
		return Framing{Holding: true, Move: "holding (Hold)"}
	}
	var in []*Track
	for _, t := range ts {
		if inZone(t.Box, zone) {
			in = append(in, t)
		}
	}
	sort.Slice(in, func(i, j int) bool { return area(in[i].Box) > area(in[j].Box) })
	var pick []*Track
	var tgt *Track
	for _, t := range in {
		if t.ID == target {
			tgt = t
		}
	}
	switch mode {
	case ModeFollow:
		if tgt != nil {
			pick = []*Track{tgt}
		} else if target == 0 && len(in) > 0 {
			pick = in[:1]
		}
	case ModePair:
		if tgt != nil {
			pick = []*Track{tgt}
			var near *Track
			for _, t := range in {
				if t != tgt && (near == nil || centerDist(t.Box, tgt.Box) < centerDist(near.Box, tgt.Box)) {
					near = t
				}
			}
			if near != nil {
				pick = append(pick, near)
			}
		} else if len(in) > 0 {
			pick = in[:min(2, len(in))]
		}
	case ModeGroup:
		pick = in
	}
	if len(pick) == 0 {
		return Framing{Lost: true, Holding: true, Move: "nobody to frame: holding"}
	}
	var u [4]float64
	for i, t := range pick {
		top, bottom := t.Box[1], shotBottom(t, shot)
		b := [4]float64{t.Box[0], top, t.Box[2], bottom}
		if i == 0 {
			u = b
		} else {
			u = [4]float64{math.Min(u[0], b[0]), math.Min(u[1], b[1]), math.Max(u[2], b[2]), math.Max(u[3], b[3])}
		}
	}
	w, h := u[2]-u[0], u[3]-u[1]
	size := math.Max(h/0.82, w*1.2) // headroom above, a little below; room at the sides
	cx := (u[0] + u[2]) / 2
	top := u[1] - 0.09*size
	aim := [4]float64{cx - size/2, top, cx + size/2, top + size}
	f := Framing{Aim: aim}
	for _, t := range pick {
		f.Subjects = append(f.Subjects, t.ID)
	}
	dx := (aim[0]+aim[2])/2 - 0.5
	dy := (aim[1]+aim[3])/2 - 0.5
	zoom := 1 / size
	var parts []string
	if math.Abs(dx) > 0.06 {
		parts = append(parts, dir(dx, "pan right", "pan left"))
	}
	if math.Abs(dy) > 0.06 {
		parts = append(parts, dir(dy, "tilt down", "tilt up"))
	}
	if zoom > 1.15 || zoom < 0.87 {
		if zoom > 1 {
			parts = append(parts, "zoom in "+ftoa(zoom)+"×")
		} else {
			parts = append(parts, "zoom out "+ftoa(1/zoom)+"×")
		}
	}
	if len(parts) == 0 {
		f.Holding = true
		f.Move = "in frame: holding"
	} else {
		f.Move = join(parts)
	}
	return f
}

// shotBottom is where the shot's lower edge falls on a person: chest for
// Close, waist for Waist, feet for Wide. Body points when the detector has
// them, else proportions of the box.
func shotBottom(t *Track, shot string) float64 {
	h := t.Box[3] - t.Box[1]
	pt := func(i int) (float64, bool) {
		if len(t.Points) > i && t.Points[i][2] >= 0.3 {
			return t.Points[i][1], true
		}
		return 0, false
	}
	avg := func(a, b int) (float64, bool) {
		ya, oka := pt(a)
		yb, okb := pt(b)
		switch {
		case oka && okb:
			return (ya + yb) / 2, true
		case oka:
			return ya, true
		case okb:
			return yb, true
		}
		return 0, false
	}
	switch shot {
	case ShotClose:
		if sh, ok := avg(kpLShoulder, kpRShoulder); ok {
			if hip, ok2 := avg(kpLHip, kpRHip); ok2 {
				return math.Min(t.Box[3], sh+(hip-sh)*0.45)
			}
			return math.Min(t.Box[3], sh+h*0.12)
		}
		return t.Box[1] + h*0.38
	case ShotWaist:
		if hip, ok := avg(kpLHip, kpRHip); ok {
			return math.Min(t.Box[3], hip+h*0.04)
		}
		return t.Box[1] + h*0.6
	}
	// Wide: the whole visible body. When the feet show, a little floor
	// under them; when something hides them (the front wall of the
	// stage), a strip of the wall, so the shot doesn't end on the cut.
	if _, ok := avg(kpLAnkle, kpRAnkle); ok {
		return t.Box[3] + h*0.03
	}
	return t.Box[3] + h*0.12
}

func dir(v float64, pos, neg string) string {
	p := int(math.Round(math.Abs(v) * 100))
	if v > 0 {
		return pos + " " + itoa(p) + "%"
	}
	return neg + " " + itoa(p) + "%"
}
