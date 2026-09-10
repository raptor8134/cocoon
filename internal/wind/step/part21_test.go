package step

import (
	"os"
	"strings"
	"testing"
)

func load(t *testing.T) *File {
	t.Helper()
	f, err := os.Open("testdata/mandrel.step")
	if err != nil {
		t.Fatalf("open testdata: %v", err)
	}
	defer f.Close()
	doc, err := Parse(f)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func TestParseHeader(t *testing.T) {
	doc := load(t)
	if got := doc.Schema(); !strings.Contains(got, "214") {
		t.Errorf("Schema() = %q, want it to mention AP214", got)
	}
	if _, ok := doc.Header["FILE_NAME"]; !ok {
		t.Error("FILE_NAME missing from header")
	}
}

func TestSurfaceAttributes(t *testing.T) {
	doc := load(t)

	// Radius and placement must survive parsing exactly; these are the numbers
	// the profile is built from, so a silent coercion here would produce a
	// plausible but wrong mandrel.
	cases := []struct {
		id         int
		typ        string
		wantRadius float64
	}{
		{20, "CYLINDRICAL_SURFACE", 50},
		{40, "CYLINDRICAL_SURFACE", 8},
		{54, "CYLINDRICAL_SURFACE", 3},
		{32, "CONICAL_SURFACE", 50},
	}
	for _, c := range cases {
		e, ok := doc.Get(c.id)
		if !ok {
			t.Fatalf("#%d not found", c.id)
		}
		if e.Type != c.typ {
			t.Errorf("#%d type = %q, want %q", c.id, e.Type, c.typ)
		}
		if got := e.Params[2].Num; got != c.wantRadius {
			t.Errorf("#%d radius = %v, want %v", c.id, got, c.wantRadius)
		}
	}
}

// The two filters that make edge cases free: a coaxial-but-smaller bore is
// excluded by taking the outermost radius, and a radially-oriented divot is
// excluded by requiring the axis to be collinear with the mandrel's.
func TestEdgeCaseDiscrimination(t *testing.T) {
	doc := load(t)

	axisOf := func(id int) []float64 {
		e, _ := doc.Get(id)
		pl, _ := doc.Get(e.Params[1].Ref)
		dir, _ := doc.Get(pl.Params[2].Ref)
		for _, p := range dir.Params {
			if p.Kind == List {
				out := make([]float64, len(p.List))
				for i, v := range p.List {
					out[i] = v.Num
				}
				return out
			}
		}
		return nil
	}

	body, bore, divot := axisOf(20), axisOf(40), axisOf(54)

	if body[2] != 1 {
		t.Errorf("body axis = %v, want +Z", body)
	}
	// The bore is coaxial with the body -- it cannot be rejected on axis, only
	// on radius. That is exactly why the outermost rule is needed.
	if bore[2] != 1 {
		t.Errorf("bore axis = %v, want +Z (coaxial with body)", bore)
	}
	if b, _ := doc.Get(40); b.Params[2].Num >= 50 {
		t.Error("bore radius should be smaller than the body's")
	}
	// The divot is radial, so the coaxiality filter alone rejects it.
	if divot[0] != 1 {
		t.Errorf("divot axis = %v, want radial (+X)", divot)
	}
}

func TestComplexInstance(t *testing.T) {
	doc := load(t)
	e, ok := doc.Get(70)
	if !ok {
		t.Fatal("#70 not found")
	}
	want := []string{"LENGTH_UNIT", "NAMED_UNIT", "SI_UNIT"}
	if len(e.Types) != len(want) {
		t.Fatalf("Types = %v, want %v", e.Types, want)
	}
	for i, w := range want {
		if e.Types[i] != w {
			t.Errorf("Types[%d] = %q, want %q", i, e.Types[i], w)
		}
	}
}

func TestValueGrammar(t *testing.T) {
	src := `ISO-10303-21;
HEADER;
ENDSEC;
DATA;
#1 = THING('it''s quoted', 42, -1.5E-3, .TRUE., $, *, (1,2,3), #7, NAMED(2.));
ENDSEC;
END-ISO-10303-21;`
	doc, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	e, _ := doc.Get(1)
	if len(e.Params) != 9 {
		t.Fatalf("got %d params, want 9", len(e.Params))
	}
	checks := []struct {
		i    int
		kind Kind
		test func(Value) bool
	}{
		{0, Str, func(v Value) bool { return v.Str == "it's quoted" }}, // doubled quote unescapes
		{1, Int, func(v Value) bool { return v.Num == 42 }},
		{2, Real, func(v Value) bool { return v.Num == -1.5e-3 }},
		{3, Enum, func(v Value) bool { return v.Str == "TRUE" }},
		{4, Unset, func(v Value) bool { return true }},
		{5, Derived, func(v Value) bool { return true }},
		{6, List, func(v Value) bool { return len(v.List) == 3 && v.List[2].Num == 3 }},
		{7, Ref, func(v Value) bool { return v.Ref == 7 }},
		{8, Typed, func(v Value) bool { return v.Str == "NAMED" && v.List[0].Num == 2 }},
	}
	for _, c := range checks {
		got := e.Params[c.i]
		if got.Kind != c.kind {
			t.Errorf("param %d kind = %v, want %v", c.i, got.Kind, c.kind)
			continue
		}
		if !c.test(got) {
			t.Errorf("param %d value wrong: %+v", c.i, got)
		}
	}
}

func TestRejectsNonStep(t *testing.T) {
	if _, err := Parse(strings.NewReader("this is not a step file")); err == nil {
		t.Error("expected an error for non-STEP input")
	}
}

// A profile written out must read back with the geometry intact. This is the
// property that matters for recovery: if the original CAD file is lost, the
// wind file has to be able to regenerate it.
func TestWriteReadRoundTrip(t *testing.T) {
	segs := []Seg{
		{Kind: "line", Start: Point{0, 50}, End: Point{200, 50}},
		{Kind: "arc", Start: Point{200, 50}, End: Point{260, 15.5},
			Center: Point{200, 15.5}, Radius: 34.5},
		{Kind: "spline", Degree: 3,
			Control: []Point{{260, 15.5}, {280, 12}, {300, 9}, {320, 8}},
			Knots:   []float64{0, 0, 0, 0, 1, 1, 1, 1}},
	}

	var buf strings.Builder
	if err := WriteRevolution(&buf, segs, "mandrel.step", "mm"); err != nil {
		t.Fatalf("write: %v", err)
	}

	doc, err := Parse(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("re-parse of our own output failed: %v", err)
	}

	// One revolution per segment, all sharing the single axis.
	revs := doc.OfType("SURFACE_OF_REVOLUTION")
	if len(revs) != len(segs) {
		t.Fatalf("got %d surfaces of revolution, want %d", len(revs), len(segs))
	}
	axis := revs[0].Params[2].Ref
	for _, r := range revs[1:] {
		if r.Params[2].Ref != axis {
			t.Errorf("segments do not share one axis: #%d vs #%d", r.Params[2].Ref, axis)
		}
	}

	// The curve kinds must survive, not be flattened to points.
	for _, want := range []string{"LINE", "CIRCLE", "B_SPLINE_CURVE_WITH_KNOTS"} {
		if len(doc.OfType(want)) == 0 {
			t.Errorf("no %s in output: geometry was not preserved exactly", want)
		}
	}

	// The spline's knot vector must come back in STEP's compressed form.
	bs := doc.OfType("B_SPLINE_CURVE_WITH_KNOTS")[0]
	if got := int(bs.Params[1].Num); got != 3 {
		t.Errorf("spline degree = %d, want 3", got)
	}
	mult := bs.Params[6]
	if mult.Kind != List || len(mult.List) != 2 {
		t.Fatalf("knot multiplicities = %+v, want 2 entries", mult)
	}
	for i, m := range mult.List {
		if m.Num != 4 {
			t.Errorf("multiplicity[%d] = %v, want 4", i, m.Num)
		}
	}

	// Reals must carry a decimal point or the file is not valid Part 21.
	if strings.Contains(buf.String(), "(0,0,0)") {
		t.Error("integers emitted where Part 21 requires reals")
	}
}
