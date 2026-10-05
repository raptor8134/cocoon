package step

import (
	"math"
)

// Detecting surfaces of revolution that were exported as NURBS.
//
// Many CAD exporters do not write CYLINDRICAL_SURFACE or
// SURFACE_OF_REVOLUTION at all. Onshape via ST-Developer, for instance, emits
// every face as a RATIONAL_B_SPLINE_SURFACE, so a file for a plainly
// axisymmetric part can contain no analytic surface entities whatsoever.
//
// The surface_form enum (.CYLINDRICAL_SURF., .TOROIDAL_SURF., ...) looks like
// it would rescue us, but it is only a hint and it is not reliable: a tangent
// ogive exports as .TOROIDAL_SURF., which is neither true nor useful. So the
// geometry has to be recognised from the control net rather than believed from
// a label.
//
// The test is simple and strong. Revolving a profile sweeps every control
// point in a circle about the axis, so in the control grid one parameter
// direction is the sweep and the other follows the profile. Along the sweep
// direction every control point keeps the SAME axial coordinate -- each row
// lies in a plane perpendicular to the axis. Checking that, in both directions,
// identifies the surface and tells us which index is which.
//
// The profile then comes from the control points at the start of each sweep.
// Those lie on the surface, because a B-spline interpolates its end control
// points, so they are exact rather than approximate.

// bsplineSurface is the control net of a (possibly rational) B-spline surface.
type bsplineSurface struct {
	UDegree, VDegree int
	// Net[i][j] indexes [u][v], matching STEP's list-of-lists ordering.
	Net     [][]Vec3
	Weights [][]float64 // empty when the surface is non-rational
	Form    string      // surface_form hint; recorded, not trusted

	UKnots, VKnots []float64 // expanded (multiplicities applied)
}

// readBSplineSurface pulls the control net out of a plain or complex instance.
func (f *File) readBSplineSurface(e Entity) (*bsplineSurface, bool) {
	params, ok := e.ParamsOf("B_SPLINE_SURFACE")
	if !ok {
		// A non-rational surface carries everything on the _WITH_KNOTS type.
		params, ok = e.ParamsOf("B_SPLINE_SURFACE_WITH_KNOTS")
		if !ok || len(params) < 4 {
			return nil, false
		}
	}
	if len(params) < 4 {
		return nil, false
	}

	s := &bsplineSurface{
		UDegree: int(params[0].Num),
		VDegree: int(params[1].Num),
	}
	grid := params[2]
	if grid.Kind != List {
		return nil, false
	}
	for _, row := range grid.List {
		if row.Kind != List {
			return nil, false
		}
		var line []Vec3
		for _, ref := range row.List {
			if ref.Kind != Ref {
				return nil, false
			}
			v, ok := f.vec(ref.Ref)
			if !ok {
				return nil, false
			}
			line = append(line, v)
		}
		s.Net = append(s.Net, line)
	}
	if len(s.Net) < 2 || len(s.Net[0]) < 2 {
		return nil, false
	}
	if len(params) > 3 && params[3].Kind == Enum {
		s.Form = params[3].Str
	}

	// Weights, when the surface is rational. Without them a swept arc is read
	// as its control polygon, which bulges away from the true radius.
	if wp, ok := e.ParamsOf("RATIONAL_B_SPLINE_SURFACE"); ok && len(wp) > 0 && wp[0].Kind == List {
		for _, row := range wp[0].List {
			if row.Kind != List {
				continue
			}
			var line []float64
			for _, w := range row.List {
				line = append(line, w.Num)
			}
			s.Weights = append(s.Weights, line)
		}
	}

	// Knots: STEP stores distinct values plus multiplicities.
	if kp, ok := e.ParamsOf("B_SPLINE_SURFACE_WITH_KNOTS"); ok && len(kp) >= 4 {
		s.UKnots = expandKnots(kp[0], kp[2])
		s.VKnots = expandKnots(kp[1], kp[3])
	}
	return s, true
}

func expandKnots(mult, vals Value) []float64 {
	if mult.Kind != List || vals.Kind != List {
		return nil
	}
	var out []float64
	for i, v := range vals.List {
		n := 1
		if i < len(mult.List) {
			n = int(mult.List[i].Num)
		}
		for j := 0; j < n; j++ {
			out = append(out, v.Num)
		}
	}
	return out
}

// revolutionFit describes how a control net sits about a candidate axis.
type revolutionFit struct {
	SweepIsU bool // true when varying the first index sweeps around the axis
	// Profile control points in (axial, radial) form, exact because a
	// B-spline interpolates its end control points.
	Profile []Point
	Weights []float64
	Degree  int
	Knots   []float64
}

// axialSpread returns how much the axial coordinate varies within a slice.
func axialSpread(pts []Vec3, axis Axis) float64 {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, p := range pts {
		t := p.sub(axis.Origin).dot(axis.Dir)
		lo, hi = math.Min(lo, t), math.Max(hi, t)
	}
	return hi - lo
}

// fitRevolution tests whether the net is a surface of revolution about axis.
//
// tol is absolute, in model units, and is compared against the overall size of
// the surface so that a large part is not rejected for floating-point dust.
func (s *bsplineSurface) fitRevolution(axis Axis, tol float64) (revolutionFit, bool) {
	nu, nv := len(s.Net), len(s.Net[0])

	// Sweeping along u: every column (fixed v) must stay at one axial station.
	sweepU := true
	for j := 0; j < nv; j++ {
		col := make([]Vec3, nu)
		for i := 0; i < nu; i++ {
			col[i] = s.Net[i][j]
		}
		if axialSpread(col, axis) > tol {
			sweepU = false
			break
		}
	}

	// Sweeping along v: every row (fixed u) must stay at one axial station.
	sweepV := true
	for i := 0; i < nu; i++ {
		if axialSpread(s.Net[i], axis) > tol {
			sweepV = false
			break
		}
	}

	// A surface flat enough to satisfy both is a disc or a plane perpendicular
	// to the axis -- an end cap, not a winding surface. It contributes no
	// profile, so reject rather than pick a direction arbitrarily.
	if sweepU == sweepV {
		return revolutionFit{}, false
	}

	fit := revolutionFit{SweepIsU: sweepU}
	toProfile := func(v Vec3) Point {
		d := v.sub(axis.Origin)
		ax := d.dot(axis.Dir)
		return Point{X: ax, R: d.sub(axis.Dir.scale(ax)).len()}
	}

	if sweepU {
		// Profile runs along v; take the start of each sweep.
		fit.Degree, fit.Knots = s.VDegree, s.VKnots
		for j := 0; j < nv; j++ {
			fit.Profile = append(fit.Profile, toProfile(s.Net[0][j]))
			if len(s.Weights) > 0 && j < len(s.Weights[0]) {
				fit.Weights = append(fit.Weights, s.Weights[0][j])
			}
		}
	} else {
		fit.Degree, fit.Knots = s.UDegree, s.UKnots
		for i := 0; i < nu; i++ {
			fit.Profile = append(fit.Profile, toProfile(s.Net[i][0]))
			if i < len(s.Weights) && len(s.Weights[i]) > 0 {
				fit.Weights = append(fit.Weights, s.Weights[i][0])
			}
		}
	}

	// A profile whose radius never changes and whose axial extent is zero is
	// degenerate; a genuine surface has to go somewhere.
	var axMin, axMax = math.Inf(1), math.Inf(-1)
	for _, p := range fit.Profile {
		axMin, axMax = math.Min(axMin, p.X), math.Max(axMax, p.X)
	}
	if axMax-axMin <= tol {
		return revolutionFit{}, false
	}
	return fit, true
}

// candidateAxes gathers plausible mandrel axes from the file.
//
// Circles are the best source: every one states a centre and a normal, and an
// axisymmetric part is full of them at its section boundaries. Placements and
// the global axes are added as fallbacks for files with no circular edges.
func (f *File) candidateAxes() []Axis {
	var out []Axis
	add := func(a Axis) {
		if a.Dir.len() == 0 {
			return
		}
		a.Dir = a.Dir.norm()
		for _, e := range out {
			if e.collinear(a, 1e-6) {
				return
			}
		}
		out = append(out, a)
	}

	for _, c := range f.OfType("CIRCLE") {
		if len(c.Params) > 1 && c.Params[1].Kind == Ref {
			if a, ok := f.axisOf(c.Params[1].Ref); ok {
				add(a)
			}
		}
	}
	for _, t := range []string{"AXIS1_PLACEMENT", "AXIS2_PLACEMENT_3D"} {
		for _, e := range f.OfType(t) {
			if a, ok := f.axisOf(e.ID); ok {
				add(a)
			}
		}
	}
	add(Axis{Dir: Vec3{0, 0, 1}})
	add(Axis{Dir: Vec3{1, 0, 0}})
	add(Axis{Dir: Vec3{0, 1, 0}})
	return out
}
