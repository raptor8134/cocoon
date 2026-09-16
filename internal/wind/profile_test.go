package wind

import (
	"math"
	"testing"
)

func TestLineSegmentExact(t *testing.T) {
	s := Segment{Kind: SegLine, Start: Pt{0, 50}, End: Pt{100, 20}}
	for _, tc := range []struct{ t, x, r float64 }{
		{0, 0, 50}, {0.5, 50, 35}, {1, 100, 20},
	} {
		got := s.EvalAt(tc.t)
		if math.Abs(got.X-tc.x) > 1e-12 || math.Abs(got.R-tc.r) > 1e-12 {
			t.Errorf("EvalAt(%v) = %+v, want {%v %v}", tc.t, got, tc.x, tc.r)
		}
	}
	// A straight segment must not be subdivided: two points describe it
	// exactly, and sampling more would be wasted work in the hot path.
	if pts := s.sample(0.01); len(pts) != 2 {
		t.Errorf("line sampled to %d points, want 2", len(pts))
	}
}

func TestArcStaysOnRadius(t *testing.T) {
	// Quarter arc from (0,10) to (10,0) about the origin.
	s := Segment{
		Kind: SegArc, Center: Pt{0, 0}, Radius: 10,
		Start: Pt{10, 0}, End: Pt{0, 10}, CCW: true,
	}
	for i := 0; i <= 20; i++ {
		p := s.EvalAt(float64(i) / 20)
		if d := math.Hypot(p.X, p.R); math.Abs(d-10) > 1e-9 {
			t.Fatalf("t=%.2f is %.6f from centre, want 10", float64(i)/20, d)
		}
	}
}

// Sampling density must follow curvature, not a fixed count: that is what lets
// a nearly-straight spline cost almost nothing while a tight nose radius gets
// the points it needs.
func TestSamplingAdaptsToTolerance(t *testing.T) {
	s := Segment{
		Kind: SegArc, Center: Pt{0, 0}, Radius: 100,
		Start: Pt{100, 0}, End: Pt{0, 100}, CCW: true,
	}
	coarse := len(s.sample(1.0))
	fine := len(s.sample(0.001))
	if fine <= coarse {
		t.Errorf("tighter tolerance gave %d points, coarser gave %d; want more", fine, coarse)
	}
	// And the result must actually meet the tolerance it was given.
	if e := s.maxChordError(fine - 1); e > 0.001*1.5 {
		t.Errorf("chord error %.6f exceeds requested tolerance 0.001", e)
	}
}

// De Boor on a clamped cubic must interpolate its end control points exactly.
// CAD exports use repeated end knots constantly, so getting this wrong would
// misplace the start and end of every imported nose curve.
func TestSplineClampedEndpoints(t *testing.T) {
	s := Segment{
		Kind: SegSpline, Degree: 3,
		Control: []Pt{{0, 50}, {30, 48}, {70, 30}, {100, 10}},
		Knots:   []float64{0, 0, 0, 0, 1, 1, 1, 1},
	}
	start, end := s.EvalAt(0), s.EvalAt(1)
	if math.Abs(start.X-0) > 1e-9 || math.Abs(start.R-50) > 1e-9 {
		t.Errorf("spline start = %+v, want {0 50}", start)
	}
	if math.Abs(end.X-100) > 1e-9 || math.Abs(end.R-10) > 1e-9 {
		t.Errorf("spline end = %+v, want {100 10}", end)
	}
	// A Bezier (single span, clamped) must stay inside its control hull.
	for i := 0; i <= 10; i++ {
		p := s.EvalAt(float64(i) / 10)
		if p.X < -1e-9 || p.X > 100+1e-9 || p.R < 10-1e-9 || p.R > 50+1e-9 {
			t.Errorf("t=%.1f left the control hull: %+v", float64(i)/10, p)
		}
	}
}

func TestSplineDegradesOnMalformedInput(t *testing.T) {
	// Too few knots for the stated degree: must fall back to the chord rather
	// than producing NaNs that would propagate into the whole path.
	s := Segment{
		Kind: SegSpline, Degree: 3,
		Control: []Pt{{0, 50}, {100, 10}},
		Knots:   []float64{0, 1},
		Start:   Pt{0, 50}, End: Pt{100, 10},
	}
	p := s.EvalAt(0.5)
	if math.IsNaN(p.X) || math.IsNaN(p.R) {
		t.Fatalf("malformed spline produced NaN: %+v", p)
	}
	if math.Abs(p.X-50) > 1e-9 || math.Abs(p.R-30) > 1e-9 {
		t.Errorf("fallback = %+v, want the chord midpoint {50 30}", p)
	}
}

func TestProfileToMandrel(t *testing.T) {
	p := Profile{Segments: []Segment{
		{Kind: SegLine, Start: Pt{0, 50}, End: Pt{200, 50}},
		{Kind: SegArc, Center: Pt{200, 15}, Radius: 35,
			Start: Pt{200, 50}, End: Pt{235, 15}, CCW: false},
	}}
	m, err := p.ToMandrel(0.05)
	if err != nil {
		t.Fatalf("ToMandrel: %v", err)
	}
	if got := m.Interp(100); math.Abs(got-50) > 1e-6 {
		t.Errorf("radius at x=100 = %v, want 50 (cylindrical section)", got)
	}
	if _, ok := m.Profile(); !ok {
		t.Error("mandrel did not retain its exact profile")
	}
	// The joint between segments must not be duplicated.
	for i := 1; i < len(m.XPoints); i++ {
		if m.XPoints[i] == m.XPoints[i-1] {
			t.Errorf("duplicate x at index %d (%v)", i, m.XPoints[i])
			break
		}
	}
}

// The profile mandrel type must survive the JSON round trip that the UI and
// the saved wind file both go through.
func TestProfileMandrelFromJSON(t *testing.T) {
	cfg := `{
	  filament: { width: 20 },
	  mandrel: {
	    type: "profile",
	    tolerance: 0.05,
	    profile: {
	      segments: [
	        { kind: "line", start: {x: 0, r: 50}, end: {x: 200, r: 50} },
	        { kind: "line", start: {x: 200, r: 50}, end: {x: 280, r: 15} }
	      ],
	      source: { filename: "nosecone.step", sha256: "abc123", originating_system: "Onshape" }
	    }
	  },
	  layers: [ { type: "hoop", stepover: 5, nrepeat: 1 } ]
	}`
	w, err := ParseWindFromJSONBytes([]byte(cfg))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := w.Mandrel.Interp(100); math.Abs(got-50) > 1e-6 {
		t.Errorf("radius at x=100 = %v, want 50", got)
	}
	if got := w.Mandrel.Interp(240); math.Abs(got-32.5) > 0.2 {
		t.Errorf("radius at x=240 = %v, want ~32.5 (mid-taper)", got)
	}
	prof, ok := w.Mandrel.Profile()
	if !ok {
		t.Fatal("mandrel did not retain its exact profile")
	}
	if len(prof.Segments) != 2 {
		t.Errorf("got %d segments, want 2", len(prof.Segments))
	}
	// Provenance must survive: it is the whole point of recording it.
	if prof.Source == nil || prof.Source.SHA256 != "abc123" ||
		prof.Source.OriginatingSystem != "Onshape" {
		t.Errorf("source provenance lost: %+v", prof.Source)
	}
}
