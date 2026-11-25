package lib

import (
	"testing"
	"time"
)

func person(x1, y1, x2, y2, s float64) Person {
	return Person{Box: [4]float64{x1, y1, x2, y2}, Score: s}
}

// A person and a "person + pulpit" box on the same body: keep one.
func TestDedupe(t *testing.T) {
	got := dedupe([]Person{person(0.4, 0.2, 0.5, 0.7, 0.9), person(0.38, 0.18, 0.56, 0.98, 0.6), person(0.0, 0.2, 0.1, 0.7, 0.8)})
	if len(got) != 2 || got[0].Score != 0.9 || got[1].Score != 0.8 {
		t.Fatalf("dedupe = %+v", got)
	}
}

func run(tr *Tracker, frames int, ps ...Person) []*Track {
	var out []*Track
	at := time.Unix(0, 0)
	for i := 0; i < frames; i++ {
		at = at.Add(100 * time.Millisecond)
		out = tr.Update(ps, at)
	}
	return out
}

func TestIdentitiesHold(t *testing.T) {
	var tr Tracker
	ts := run(&tr, 5, person(0.0, 0.2, 0.1, 0.7, 0.9), person(0.4, 0.2, 0.5, 0.7, 0.9))
	if len(ts) != 2 {
		t.Fatalf("want 2 confirmed, got %d", len(ts))
	}
	ids := map[int]bool{ts[0].ID: true, ts[1].ID: true}
	// they move a little: same IDs
	ts = run(&tr, 3, person(0.01, 0.2, 0.11, 0.7, 0.9), person(0.42, 0.2, 0.52, 0.7, 0.9))
	for _, x := range ts {
		if !ids[x.ID] {
			t.Fatalf("identity changed: %d", x.ID)
		}
	}
}

func TestFrameShot(t *testing.T) {
	var tr Tracker
	zone := [4]float64{0, 0, 1, 0.8}
	// translator left, speaker right, someone walking in front (feet below the zone)
	ts := run(&tr, 5, person(0.05, 0.2, 0.15, 0.7, 0.9), person(0.6, 0.2, 0.72, 0.75, 0.9), person(0.4, 0.4, 0.5, 0.95, 0.9))
	f := FrameShot(ts, ModePair, ShotWide, zone, 0)
	if len(f.Subjects) != 2 {
		t.Fatalf("pair = %v", f.Subjects)
	}
	for _, id := range f.Subjects {
		for _, x := range ts {
			if x.ID == id && x.Box[3] > 0.8 {
				t.Fatalf("walker in front of the stage was framed")
			}
		}
	}
	if f.Aim[0] > 0.05 || f.Aim[2] < 0.72 {
		t.Fatalf("aim %v doesn't hold both", f.Aim)
	}
	g := FrameShot(ts, ModeGroup, ShotWide, zone, 0)
	if len(g.Subjects) != 2 {
		t.Fatalf("group = %v", g.Subjects)
	}
	if h := FrameShot(ts, ModeHold, ShotWide, zone, 0); !h.Holding || len(h.Subjects) != 0 {
		t.Fatalf("hold = %+v", h)
	}
	if l := FrameShot(nil, ModeFollow, ShotWaist, zone, 0); !l.Lost {
		t.Fatalf("nobody should be lost: %+v", l)
	}
	// follow a tapped person
	var right *Track
	for _, x := range ts {
		if x.Box[0] > 0.55 {
			right = x
		}
	}
	if fo := FrameShot(ts, ModeFollow, ShotClose, zone, right.ID); len(fo.Subjects) != 1 || fo.Subjects[0] != right.ID {
		t.Fatalf("follow = %v", fo.Subjects)
	}
}
