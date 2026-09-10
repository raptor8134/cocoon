// Package wind - exact mandrel profiles.
//
// A profile is stored as a sequence of CURVE SEGMENTS in the (axial, radial)
// plane, not as sampled points. That choice is about information, not size:
//
//   - It is exact. Sampling a 3:1 ogive to fifty points and interpolating is
//     an approximation of geometry that was originally defined exactly.
//   - It is resolution-independent. Path generation samples it at whatever
//     density it needs; the stored form does not have to guess in advance.
//   - It round-trips. A profile held as curves plus an axis can be written
//     back out as a STEP surface of revolution, so a wind file remains a
//     complete record of the geometry even if the original CAD file is lost.
//
// The same representation covers all three sources: hand-entered points
// (polyline segments), imported STEP curves (whatever the file used), and
// parametric rocketry shapes (an arc, or a spline fitted once).
//
// Mandrel remains the sampled working form used by path generation, which
// wants a flat array and a binary search rather than curve evaluation per
// point. Sampling is a derived cache, rebuilt from the profile on load.
package wind

import (
	"fmt"
	"math"
)

// SegmentKind identifies a profile segment's geometry.
type SegmentKind string

const (
	// SegLine is a straight taper (or a constant radius when r0 == r1).
	// Cylinders and cones both reduce to this.
	SegLine SegmentKind = "line"

	// SegArc is a circular arc, as produced by spherical and toroidal
	// surfaces and by tangent-ogive noses.
	SegArc SegmentKind = "arc"

	// SegSpline is a NURBS curve, the general case from CAD.
	SegSpline SegmentKind = "spline"

	// SegPolyline is a sampled run of points, used for hand-entered profiles
	// and CSV imports where no better description exists.
	SegPolyline SegmentKind = "polyline"
)

// Pt is a point in the profile plane: X along the mandrel axis, R the radius.
type Pt struct {
	X float64 `json:"x"`
	R float64 `json:"r"`
}

// Segment is one piece of a profile. Only the fields relevant to Kind are set.
type Segment struct {
	Kind SegmentKind `json:"kind"`

	// Line, and the endpoints of every other kind.
	Start Pt `json:"start"`
	End   Pt `json:"end"`

	// Arc: centre and radius in the profile plane. Sweep runs from Start to
	// End the short way unless Large is set.
	Center Pt      `json:"center,omitempty"`
	Radius float64 `json:"radius,omitempty"`
	Large  bool    `json:"large,omitempty"`
	CCW    bool    `json:"ccw,omitempty"`

	// Spline: a NURBS curve. Weights may be nil for a non-rational curve.
	Degree  int       `json:"degree,omitempty"`
	Control []Pt      `json:"control,omitempty"`
	Knots   []float64 `json:"knots,omitempty"`
	Weights []float64 `json:"weights,omitempty"`

	// Polyline points, including both endpoints.
	Points []Pt `json:"points,omitempty"`
}

// Profile is an exact mandrel outline plus where it came from.
type Profile struct {
	Segments []Segment `json:"segments"`

	// Source records provenance. Kept because the geometry outlives the file
	// it came from, and knowing which file (and whether it has since changed)
	// is the difference between trusting a wind and re-deriving it.
	Source *ProfileSource `json:"source,omitempty"`
}

// ProfileSource identifies the file a profile was imported from.
//
// Hash is authoritative: filenames get renamed and timestamps get rewritten by
// copying, but content that hashes the same is the same geometry. The other
// fields are for human recognition.
type ProfileSource struct {
	Filename string `json:"filename,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Modified string `json:"modified,omitempty"` // file's own timestamp, RFC3339

	// FromSTEP records what the STEP header said about its origin, which
	// survives copying better than any filesystem metadata.
	Schema            string `json:"schema,omitempty"`
	OriginatingSystem string `json:"originating_system,omitempty"`
	Units             string `json:"units,omitempty"`

	// Imported is when Cocoon read it.
	Imported string `json:"imported,omitempty"`

	// Axis and Surfaces record the interpretation that produced the profile,
	// so a later reader can tell how the geometry was chosen -- not just what
	// it came out as.
	Axis     string   `json:"axis,omitempty"`
	Surfaces []string `json:"surfaces,omitempty"`
}

// EvalAt returns the radius at parameter t in [0,1] along a segment.
func (s Segment) EvalAt(t float64) Pt {
	switch s.Kind {
	case SegLine:
		return Pt{
			X: s.Start.X + t*(s.End.X-s.Start.X),
			R: s.Start.R + t*(s.End.R-s.Start.R),
		}
	case SegArc:
		a0 := math.Atan2(s.Start.R-s.Center.R, s.Start.X-s.Center.X)
		a1 := math.Atan2(s.End.R-s.Center.R, s.End.X-s.Center.X)
		sweep := a1 - a0
		// Normalise into the intended direction and extent.
		if s.CCW {
			for sweep < 0 {
				sweep += 2 * math.Pi
			}
		} else {
			for sweep > 0 {
				sweep -= 2 * math.Pi
			}
		}
		if s.Large == (math.Abs(sweep) < math.Pi) {
			if sweep > 0 {
				sweep -= 2 * math.Pi
			} else {
				sweep += 2 * math.Pi
			}
		}
		a := a0 + t*sweep
		return Pt{
			X: s.Center.X + s.Radius*math.Cos(a),
			R: s.Center.R + s.Radius*math.Sin(a),
		}
	case SegPolyline:
		if len(s.Points) == 0 {
			return s.Start
		}
		if len(s.Points) == 1 {
			return s.Points[0]
		}
		// Parameterise uniformly by index; adequate because polyline points
		// come from sources that were already sampled.
		f := t * float64(len(s.Points)-1)
		i := int(f)
		if i >= len(s.Points)-1 {
			return s.Points[len(s.Points)-1]
		}
		u := f - float64(i)
		p0, p1 := s.Points[i], s.Points[i+1]
		return Pt{X: p0.X + u*(p1.X-p0.X), R: p0.R + u*(p1.R-p0.R)}
	case SegSpline:
		return s.evalSpline(t)
	}
	return s.Start
}

// evalSpline evaluates a (possibly rational) B-spline via de Boor's algorithm.
//
// De Boor rather than direct basis-function evaluation because it is
// numerically stable and does not require computing the basis functions
// explicitly -- which matters here, since CAD exports routinely use repeated
// knots at the ends and de Boor handles those without special cases.
func (s Segment) evalSpline(t float64) Pt {
	p, n := s.Degree, len(s.Control)
	if p <= 0 || n == 0 || len(s.Knots) < n+p+1 {
		// Not a usable spline; fall back to the chord so a malformed segment
		// degrades to something drawable rather than producing NaNs.
		return Pt{
			X: s.Start.X + t*(s.End.X-s.Start.X),
			R: s.Start.R + t*(s.End.R-s.Start.R),
		}
	}

	// Map t in [0,1] onto the curve's valid parameter span.
	lo, hi := s.Knots[p], s.Knots[n]
	u := lo + t*(hi-lo)
	if u < lo {
		u = lo
	}
	if u > hi {
		u = hi
	}

	// Knot span containing u.
	k := n - 1
	for i := p; i < n; i++ {
		if u < s.Knots[i+1] {
			k = i
			break
		}
	}

	// Work in homogeneous coordinates so rational and non-rational curves
	// share one code path.
	type h struct{ x, r, w float64 }
	d := make([]h, p+1)
	for j := 0; j <= p; j++ {
		idx := k - p + j
		w := 1.0
		if s.Weights != nil && idx < len(s.Weights) {
			w = s.Weights[idx]
		}
		c := s.Control[idx]
		d[j] = h{c.X * w, c.R * w, w}
	}

	for r := 1; r <= p; r++ {
		for j := p; j >= r; j-- {
			i := k - p + j
			den := s.Knots[i+p-r+1] - s.Knots[i]
			a := 0.0
			if den != 0 {
				a = (u - s.Knots[i]) / den
			}
			d[j] = h{
				x: (1-a)*d[j-1].x + a*d[j].x,
				r: (1-a)*d[j-1].r + a*d[j].r,
				w: (1-a)*d[j-1].w + a*d[j].w,
			}
		}
	}
	if d[p].w == 0 {
		return s.Start
	}
	return Pt{X: d[p].x / d[p].w, R: d[p].r / d[p].w}
}

// Sample converts a profile into the point form path generation uses.
//
// Straight segments need two points; curved ones are subdivided until the
// chord deviates from the curve by less than tol. Sampling by deviation rather
// than by a fixed count means a nearly-straight spline costs almost nothing
// while a tight nose radius gets the density it needs.
func (p Profile) Sample(tol float64) []Pt {
	if tol <= 0 {
		tol = 0.01 // mm
	}
	var out []Pt
	for _, seg := range p.Segments {
		pts := seg.sample(tol)
		// Drop the duplicated joint between consecutive segments.
		if len(out) > 0 && len(pts) > 0 && nearly(out[len(out)-1], pts[0]) {
			pts = pts[1:]
		}
		out = append(out, pts...)
	}
	return out
}

func nearly(a, b Pt) bool {
	return math.Abs(a.X-b.X) < 1e-9 && math.Abs(a.R-b.R) < 1e-9
}

func (s Segment) sample(tol float64) []Pt {
	if s.Kind == SegLine {
		return []Pt{s.Start, s.End}
	}
	if s.Kind == SegPolyline {
		if len(s.Points) > 0 {
			return append([]Pt(nil), s.Points...)
		}
		return []Pt{s.Start, s.End}
	}

	// Adaptive: start coarse and double until the midpoint error is inside
	// tolerance. Bounded so a pathological curve cannot spin here.
	n := 8
	for ; n <= 4096; n *= 2 {
		if s.maxChordError(n) <= tol {
			break
		}
	}
	out := make([]Pt, 0, n+1)
	for i := 0; i <= n; i++ {
		out = append(out, s.EvalAt(float64(i)/float64(n)))
	}
	return out
}

// maxChordError measures how far the curve strays from its chords at n
// subdivisions, by checking the midpoint of each chord.
func (s Segment) maxChordError(n int) float64 {
	worst := 0.0
	for i := 0; i < n; i++ {
		t0 := float64(i) / float64(n)
		t1 := float64(i+1) / float64(n)
		a, b := s.EvalAt(t0), s.EvalAt(t1)
		mid := s.EvalAt((t0 + t1) / 2)
		chordMid := Pt{X: (a.X + b.X) / 2, R: (a.R + b.R) / 2}
		if e := math.Hypot(mid.X-chordMid.X, mid.R-chordMid.R); e > worst {
			worst = e
		}
	}
	return worst
}

// ToMandrel samples the profile and builds the working mandrel geometry.
func (p Profile) ToMandrel(tol float64) (*Mandrel, error) {
	pts := p.Sample(tol)
	if len(pts) < 2 {
		return nil, fmt.Errorf("profile has fewer than 2 points after sampling")
	}
	rows := make([][]float64, len(pts))
	for i, pt := range pts {
		rows[i] = []float64{pt.X, pt.R}
	}
	m, err := NewMandrelFromPoints(rows)
	if err != nil {
		return nil, err
	}
	m.profile = &p
	return m, nil
}
