package step

import (
	"math"
	"os"
	"testing"
)

func model(t *testing.T) *Model {
	t.Helper()
	f, err := os.Open("testdata/bounded.step")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	doc, err := Parse(f)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	m, err := ReadModel(doc)
	if err != nil {
		t.Fatalf("ReadModel: %v", err)
	}
	return m
}

func find(m *Model, id int) *Candidate {
	for i := range m.Candidates {
		if m.Candidates[i].ID == id {
			return &m.Candidates[i]
		}
	}
	return nil
}

func TestHeaderProvenance(t *testing.T) {
	h := model(t).Header
	// These are what let a user recognise a file later, and they survive
	// copying where filesystem metadata does not.
	if h.OriginatingSystem != "Onshape" {
		t.Errorf("OriginatingSystem = %q, want Onshape", h.OriginatingSystem)
	}
	if h.Timestamp != "2026-09-14T10:22:31" {
		t.Errorf("Timestamp = %q", h.Timestamp)
	}
	if h.Units != "mm" || h.ScaleToMM != 1 {
		t.Errorf("units = %q scale = %v, want mm x1", h.Units, h.ScaleToMM)
	}
}

func TestAxisByConsensus(t *testing.T) {
	m := model(t)
	// Three surfaces share +X and one does not, so consensus must pick +X.
	// Picking "the first axis seen" would be a coin toss.
	if m.AxisLabel != "+X" {
		t.Errorf("AxisLabel = %q, want +X", m.AxisLabel)
	}
}

func TestExtentsFromTopology(t *testing.T) {
	m := model(t)
	// STEP surfaces are unbounded; these numbers can only come from walking
	// the face boundaries.
	for _, c := range []struct {
		id     int
		lo, hi float64
	}{
		{10, 0, 200},   // body
		{32, 200, 280}, // nose
	} {
		got := find(m, c.id)
		if got == nil {
			t.Fatalf("#%d missing", c.id)
		}
		if math.Abs(got.AxialMin-c.lo) > 1e-6 || math.Abs(got.AxialMax-c.hi) > 1e-6 {
			t.Errorf("#%d extent = %.1f..%.1f, want %.1f..%.1f",
				c.id, got.AxialMin, got.AxialMax, c.lo, c.hi)
		}
	}
}

// A narrowing cone is encoded with the placement at the small end and the axis
// pointing back toward the large end, because STEP requires a positive
// semi_angle. Ignoring that direction makes every nose flare outwards.
func TestConeTapersNotFlares(t *testing.T) {
	nose := find(model(t), 32)
	if nose == nil {
		t.Fatal("nose surface missing")
	}
	if nose.Rejected != "" {
		t.Fatalf("nose rejected: %s", nose.Rejected)
	}
	if math.Abs(nose.RadiusMin-15) > 0.05 || math.Abs(nose.RadiusMax-50) > 0.05 {
		t.Errorf("nose radius = %.2f..%.2f, want 15..50", nose.RadiusMin, nose.RadiusMax)
	}
	// And it must narrow along +X, not widen.
	seg := nose.Segments[0]
	if seg.End.R >= seg.Start.R {
		t.Errorf("nose widens along +X: %.1f -> %.1f", seg.Start.R, seg.End.R)
	}
}

func TestBoreRejectedByEnvelope(t *testing.T) {
	bore := find(model(t), 50)
	if bore == nil {
		t.Fatal("bore surface missing")
	}
	if bore.Rejected == "" {
		t.Fatal("bore was accepted; it is an internal feature")
	}
	// It is genuinely coaxial -- only the envelope test can reject it, and the
	// bore spans body and nose together so no single surface contains it.
	if !bore.Coaxial {
		t.Error("bore should be recognised as coaxial before being rejected on radius")
	}
}

func TestDivotRejectedByAxis(t *testing.T) {
	divot := find(model(t), 74)
	if divot == nil {
		t.Fatal("divot surface missing")
	}
	if divot.Coaxial {
		t.Error("divot axis is radial; it must not be coaxial")
	}
	if divot.Rejected == "" {
		t.Error("divot was accepted")
	}
}

func TestAcceptedSurfacesFormTheProfile(t *testing.T) {
	m := model(t)
	var accepted int
	for _, c := range m.Candidates {
		if c.Rejected == "" {
			accepted++
			if len(c.Segments) == 0 {
				t.Errorf("#%d accepted but produced no segments", c.ID)
			}
		}
	}
	if accepted != 2 {
		t.Errorf("accepted %d surfaces, want 2 (body and nose)", accepted)
	}
}
