package step

import (
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

// Curve segments a caller can ask to be written. This mirrors wind.Segment
// without importing it, so the step package stays free of domain types and can
// be tested on its own.
type (
	// Point in the profile plane: X along the revolution axis, R the radius.
	Point struct{ X, R float64 }

	// Seg is one profile segment to emit.
	Seg struct {
		Kind    string // "line", "arc", "spline", "polyline"
		Start   Point
		End     Point
		Center  Point
		Radius  float64
		Degree  int
		Control []Point
		Knots   []float64
		Weights []float64
		Points  []Point
	}
)

// WriteRevolution emits a STEP Part 21 file describing the profile revolved
// about the X axis.
//
// The point of this is recovery, not interoperability with a specific CAD
// package: a wind file that carries exact curves can regenerate the geometry
// it was built from, so losing the original model is inconvenient rather than
// terminal. What is written is the generating curve plus an axis -- the same
// pairing the importer reads back.
func WriteRevolution(w io.Writer, segs []Seg, name string, units string) error {
	if len(segs) == 0 {
		return fmt.Errorf("no profile segments to write")
	}
	e := &emitter{w: w, next: 1}

	stamp := time.Now().UTC().Format("2006-01-02T15:04:05")
	fmt.Fprintf(w, "ISO-10303-21;\nHEADER;\n")
	fmt.Fprintf(w, "FILE_DESCRIPTION(('mandrel profile exported by Cocoon'),'2;1');\n")
	fmt.Fprintf(w, "FILE_NAME('%s','%s',('Cocoon'),(''),'Cocoon','Cocoon','');\n",
		p21String(name), stamp)
	fmt.Fprintf(w, "FILE_SCHEMA(('AUTOMOTIVE_DESIGN { 1 0 10303 214 3 1 1 }'));\n")
	fmt.Fprintf(w, "ENDSEC;\nDATA;\n")

	// Revolution axis: +X through the origin, matching the winder convention
	// where the mandrel axis is the carriage direction.
	origin := e.emit("CARTESIAN_POINT('',(0.,0.,0.))")
	axisDir := e.emit("DIRECTION('',(1.,0.,0.))")
	axis := e.emit(fmt.Sprintf("AXIS1_PLACEMENT('',#%d,#%d)", origin, axisDir))

	for i, s := range segs {
		curve, err := e.curve(s)
		if err != nil {
			return fmt.Errorf("segment %d: %w", i, err)
		}
		e.emit(fmt.Sprintf("SURFACE_OF_REVOLUTION('seg%d',#%d,#%d)", i, curve, axis))
	}

	// Units. Written even though a bare profile hardly needs them, because a
	// file that does not state its units is a file someone will misread.
	unit := e.emit("( LENGTH_UNIT() NAMED_UNIT(*) SI_UNIT(.MILLI.,.METRE.) )")
	_ = unit
	_ = units

	fmt.Fprintf(w, "ENDSEC;\nEND-ISO-10303-21;\n")
	return e.err
}

type emitter struct {
	w    io.Writer
	next int
	err  error
}

func (e *emitter) emit(body string) int {
	id := e.next
	e.next++
	if e.err == nil {
		_, e.err = fmt.Fprintf(e.w, "#%d = %s;\n", id, body)
	}
	return id
}

func (e *emitter) point(p Point) int {
	// The profile plane maps to (axial, radial, 0): the revolve puts the
	// radial component back around the axis.
	return e.emit(fmt.Sprintf("CARTESIAN_POINT('',(%s,%s,0.))", p21Real(p.X), p21Real(p.R)))
}

func (e *emitter) curve(s Seg) (int, error) {
	switch s.Kind {
	case "line", "":
		// A LINE in STEP is a point plus a vector, and is unbounded; the
		// trimming is what gives it extent.
		a := e.point(s.Start)
		dx, dr := s.End.X-s.Start.X, s.End.R-s.Start.R
		length := math.Hypot(dx, dr)
		if length == 0 {
			return 0, fmt.Errorf("zero-length line segment")
		}
		dir := e.emit(fmt.Sprintf("DIRECTION('',(%s,%s,0.))", p21Real(dx/length), p21Real(dr/length)))
		vec := e.emit(fmt.Sprintf("VECTOR('',#%d,%s)", dir, p21Real(length)))
		line := e.emit(fmt.Sprintf("LINE('',#%d,#%d)", a, vec))
		b := e.point(s.End)
		return e.emit(fmt.Sprintf(
			"TRIMMED_CURVE('',#%d,(#%d),(#%d),.T.,.CARTESIAN.)", line, a, b)), nil

	case "arc":
		c := e.point(s.Center)
		normal := e.emit("DIRECTION('',(0.,0.,1.))")
		ref := e.emit("DIRECTION('',(1.,0.,0.))")
		pl := e.emit(fmt.Sprintf("AXIS2_PLACEMENT_3D('',#%d,#%d,#%d)", c, normal, ref))
		circ := e.emit(fmt.Sprintf("CIRCLE('',#%d,%s)", pl, p21Real(s.Radius)))
		a := e.point(s.Start)
		b := e.point(s.End)
		return e.emit(fmt.Sprintf(
			"TRIMMED_CURVE('',#%d,(#%d),(#%d),.T.,.CARTESIAN.)", circ, a, b)), nil

	case "spline":
		if s.Degree <= 0 || len(s.Control) == 0 {
			return 0, fmt.Errorf("spline segment missing degree or control points")
		}
		ids := make([]string, len(s.Control))
		for i, cp := range s.Control {
			ids[i] = fmt.Sprintf("#%d", e.point(cp))
		}
		// Collapse the knot vector into the distinct-knot + multiplicity form
		// STEP uses.
		knots, mult := compressKnots(s.Knots)
		ks := make([]string, len(knots))
		for i, k := range knots {
			ks[i] = p21Real(k)
		}
		ms := make([]string, len(mult))
		for i, m := range mult {
			ms[i] = fmt.Sprintf("%d", m)
		}
		return e.emit(fmt.Sprintf(
			"B_SPLINE_CURVE_WITH_KNOTS('',%d,(%s),.UNSPECIFIED.,.F.,.F.,(%s),(%s),.UNSPECIFIED.)",
			s.Degree, strings.Join(ids, ","), strings.Join(ms, ","), strings.Join(ks, ","))), nil

	case "polyline":
		if len(s.Points) < 2 {
			return 0, fmt.Errorf("polyline segment needs at least 2 points")
		}
		ids := make([]string, len(s.Points))
		for i, p := range s.Points {
			ids[i] = fmt.Sprintf("#%d", e.point(p))
		}
		return e.emit(fmt.Sprintf("POLYLINE('',(%s))", strings.Join(ids, ","))), nil
	}
	return 0, fmt.Errorf("unknown segment kind %q", s.Kind)
}

func compressKnots(knots []float64) ([]float64, []int) {
	var vals []float64
	var mult []int
	for _, k := range knots {
		if len(vals) > 0 && vals[len(vals)-1] == k {
			mult[len(mult)-1]++
			continue
		}
		vals = append(vals, k)
		mult = append(mult, 1)
	}
	return vals, mult
}

// p21Real formats a number the way Part 21 requires: a real always carries a
// decimal point, so "1" must be written "1.".
func p21Real(v float64) string {
	s := fmt.Sprintf("%g", v)
	if !strings.ContainsAny(s, ".eE") {
		s += "."
	}
	return s
}

// p21String escapes a value for a Part 21 single-quoted string.
func p21String(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
