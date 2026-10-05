package step

import (
	"fmt"
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

// --- NURBS-exported files --------------------------------------------------

func nurbsModel(t *testing.T) *Model {
	t.Helper()
	f, err := os.Open("testdata/nurbs_export.step")
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

// The file contains NO analytic surface entities. A reader that looks up
// CYLINDRICAL_SURFACE and friends finds nothing and reports an unusable file,
// which is exactly what happened to a real Onshape export.
func TestNoAnalyticSurfacesStillImports(t *testing.T) {
	f, _ := os.Open("testdata/nurbs_export.step")
	defer f.Close()
	doc, _ := Parse(f)
	for _, typ := range []string{
		"CYLINDRICAL_SURFACE", "CONICAL_SURFACE", "SPHERICAL_SURFACE",
		"TOROIDAL_SURFACE", "SURFACE_OF_REVOLUTION",
	} {
		if n := len(doc.OfType(typ)); n != 0 {
			t.Fatalf("fixture should contain no %s, found %d", typ, n)
		}
	}
	m, err := ReadModel(doc)
	if err != nil {
		t.Fatalf("ReadModel on an all-NURBS file: %v", err)
	}
	if len(m.Candidates) == 0 {
		t.Fatal("no candidates recovered from NURBS surfaces")
	}
}

func TestNurbsUnitsAndAxis(t *testing.T) {
	m := nurbsModel(t)
	// Bare METRE with no prefix. Reading this as millimetres would scale the
	// whole part down by 1000 and produce a mandrel 0.3 mm long.
	if m.Header.ScaleToMM != 1000 {
		t.Errorf("ScaleToMM = %v, want 1000 for bare METRE", m.Header.ScaleToMM)
	}
	if m.AxisLabel != "+Z" {
		t.Errorf("AxisLabel = %q, want +Z", m.AxisLabel)
	}
	if m.Header.OriginatingSystem == "" {
		t.Error("originating system not recovered")
	}
}

func TestNurbsSurfacesClassified(t *testing.T) {
	m := nurbsModel(t)

	var body, ogive, tip, bore *Candidate
	for i := range m.Candidates {
		c := &m.Candidates[i]
		switch {
		case c.Rejected != "":
			bore = c
		case c.RadiusMax > 40 && c.AxialMax-c.AxialMin < 50:
			body = c
		case c.RadiusMax > 40:
			ogive = c
		default:
			tip = c
		}
	}
	if body == nil || ogive == nil || tip == nil || bore == nil {
		t.Fatalf("expected body, ogive, tip and bore; got %d candidates", len(m.Candidates))
	}
	// The blunted tip: a winder cannot wind a point.
	if math.Abs(tip.RadiusMax-9.525) > 0.01 {
		t.Errorf("tip radius = %.3f, want 9.525", tip.RadiusMax)
	}

	// Extents come from VERTEX points only. Following the edge CURVES as well
	// would pull in B-spline control points, which overshoot: on the real
	// export that turned 12.7..304.8 into -21..325.
	if math.Abs(ogive.AxialMin-12.7) > 0.01 || math.Abs(ogive.AxialMax-304.8) > 0.01 {
		t.Errorf("ogive extent = %.2f..%.2f, want 12.70..304.80",
			ogive.AxialMin, ogive.AxialMax)
	}
	if math.Abs(ogive.RadiusMax-47.625) > 0.01 || math.Abs(ogive.RadiusMin-9.525) > 0.01 {
		t.Errorf("ogive radius = %.3f..%.3f, want 9.525..47.625",
			ogive.RadiusMin, ogive.RadiusMax)
	}
	if bore.Rejected == "" {
		t.Error("the coaxial bore should be rejected as an internal feature")
	}
}

// The exporter tags the ogive .TOROIDAL_SURF., which is not true. Detection
// must come from the control net, so the label can only ever be a description.
func TestSurfaceFormHintIsNotTrusted(t *testing.T) {
	m := nurbsModel(t)
	for _, c := range m.Candidates {
		if c.Rejected == "" && c.RadiusMax > 40 && c.AxialMax-c.AxialMin > 200 {
			if len(c.Segments) == 0 {
				t.Fatal("the surface tagged TOROIDAL_SURF produced no profile")
			}
			seg := c.Segments[0]
			if seg.Kind != "spline" || len(seg.Control) != 4 {
				t.Errorf("ogive segment = %s with %d control points, want a 4-point spline",
					seg.Kind, len(seg.Control))
			}
			// Tangency: the first two control points share a radius, so the
			// curve leaves the body parallel to the axis.
			if math.Abs(seg.Control[0].R-seg.Control[1].R) > 1e-9 {
				t.Errorf("ogive is not tangent to the body: P0.r=%.6f P1.r=%.6f",
					seg.Control[0].R, seg.Control[1].R)
			}
			// Rational: weights must survive, or the arc reads as its control
			// polygon and bulges.
			if len(seg.Weights) != 4 {
				t.Errorf("got %d weights, want 4", len(seg.Weights))
			}
			return
		}
	}
	t.Fatal("ogive candidate not found")
}

// A full revolve exported as two halves yields identical profiles; selecting
// both would lay the outline down twice.
func TestDuplicateHalvesMerge(t *testing.T) {
	m := nurbsModel(t)
	seen := map[string]int{}
	for _, c := range m.Candidates {
		if c.Rejected != "" {
			continue
		}
		key := fmt.Sprintf("%.3f-%.3f-%.3f", c.AxialMin, c.AxialMax, c.RadiusMax)
		seen[key]++
	}
	for k, n := range seen {
		if n > 1 {
			t.Errorf("profile %s appears %d times; duplicate halves should merge", k, n)
		}
	}
}
