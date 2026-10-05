package step

import (
	"fmt"
	"math"
	"sort"
)

// Vec3 is a point or direction in model space.
type Vec3 struct{ X, Y, Z float64 }

func (a Vec3) sub(b Vec3) Vec3    { return Vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }
func (a Vec3) dot(b Vec3) float64 { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }
func (a Vec3) len() float64       { return math.Sqrt(a.dot(a)) }
func (a Vec3) scale(f float64) Vec3 {
	return Vec3{a.X * f, a.Y * f, a.Z * f}
}
func (a Vec3) norm() Vec3 {
	l := a.len()
	if l == 0 {
		return a
	}
	return a.scale(1 / l)
}
func (a Vec3) cross(b Vec3) Vec3 {
	return Vec3{a.Y*b.Z - a.Z*b.Y, a.Z*b.X - a.X*b.Z, a.X*b.Y - a.Y*b.X}
}

// Axis is a line in space: a point on it and a unit direction.
type Axis struct {
	Origin Vec3
	Dir    Vec3
}

// collinear reports whether two axes describe the same line, ignoring
// direction sign. Surfaces on one mandrel share an axis but frequently record
// it pointing opposite ways.
func (a Axis) collinear(b Axis, tol float64) bool {
	if math.Abs(math.Abs(a.Dir.dot(b.Dir))-1) > tol {
		return false
	}
	// Distance from b's origin to a's line.
	d := b.Origin.sub(a.Origin)
	perp := d.sub(a.Dir.scale(d.dot(a.Dir)))
	return perp.len() <= tol*100 // absolute, in model units
}

// Candidate is one axisymmetric surface found in the file, with everything the
// user needs to judge whether it belongs to the mandrel.
type Candidate struct {
	ID   int    // STEP entity id, so the UI can refer back to it
	Type string // CYLINDRICAL_SURFACE, CONICAL_SURFACE, ...
	Name string // the entity's name field, when the CAD wrote one

	Axis Axis

	// Extent along the chosen axis, in millimetres after unit conversion.
	AxialMin, AxialMax float64

	// Radius range over that extent.
	RadiusMin, RadiusMax float64

	// Coaxial is true when this surface shares the dominant axis.
	Coaxial bool

	// fromBSpline marks a surface recovered from a NURBS control net rather
	// than from an analytic entity. Its profile is already known at detection
	// time, in model units, so it skips segmentsFor.
	fromBSpline bool

	// faces counts how many STEP faces this candidate covers after merging
	// duplicate halves; mergedIDs records the ones folded in, so the review
	// panel can still name them.
	faces     int
	mergedIDs []int

	// rawProfile is the detected profile before unit scaling.
	rawProfile []Point
	rawWeights []float64
	rawDegree  int
	rawKnots   []float64

	// Rejected explains why a surface is not a mandrel candidate. Empty when
	// it is one. Surfaces are reported rather than silently dropped: a user
	// seeing "bore: coaxial but inside the outer surface" learns that the
	// right surface was found, which a silent filter cannot convey.
	Rejected string

	// Segments is the exact profile contribution, once extents are known.
	Segments []Seg
}

// Header holds the provenance a STEP file states about itself. These survive
// copying, where filesystem metadata does not.
type Header struct {
	Name              string
	Timestamp         string
	Author            string
	Organization      string
	PreprocessorVer   string
	OriginatingSystem string
	Schema            string
	Units             string
	ScaleToMM         float64
}

// Model is the interpreted content of a STEP file.
type Model struct {
	Header     Header
	Axis       Axis
	AxisLabel  string // "+X", "-Z", or "custom" when not aligned to a global axis
	Candidates []Candidate
}

// vec reads a CARTESIAN_POINT or DIRECTION's coordinate list.
func (f *File) vec(id int) (Vec3, bool) {
	e, ok := f.Get(id)
	if !ok {
		return Vec3{}, false
	}
	for _, p := range e.Params {
		if p.Kind == List && len(p.List) >= 2 {
			v := Vec3{X: p.List[0].Num, Y: p.List[1].Num}
			if len(p.List) > 2 {
				v.Z = p.List[2].Num
			}
			return v, true
		}
	}
	return Vec3{}, false
}

// axisOf resolves AXIS2_PLACEMENT_3D (location, axis, ref_direction) or
// AXIS1_PLACEMENT (location, axis).
func (f *File) axisOf(id int) (Axis, bool) {
	e, ok := f.Get(id)
	if !ok || len(e.Params) < 2 {
		return Axis{}, false
	}
	origin, ok1 := f.vec(e.Params[1].Ref)
	if !ok1 {
		return Axis{}, false
	}
	dir := Vec3{0, 0, 1} // STEP's default when the axis is unset
	if len(e.Params) > 2 && e.Params[2].Kind == Ref {
		if d, ok := f.vec(e.Params[2].Ref); ok {
			dir = d
		}
	}
	return Axis{Origin: origin, Dir: dir.norm()}, true
}

// lengthScale finds the factor converting file units to millimetres.
//
// A file that does not state its units is read as millimetres, because that is
// what mechanical CAD overwhelmingly exports and guessing metres would be
// catastrophically wrong rather than merely wrong.
func (f *File) lengthScale() (float64, string) {
	for _, e := range f.Entities {
		isLength, isSI := false, false
		for _, t := range e.Types {
			if t == "LENGTH_UNIT" {
				isLength = true
			}
			if t == "SI_UNIT" {
				isSI = true
			}
		}
		if !isLength || !isSI {
			continue
		}
		prefix, unit := "", ""
		for _, p := range e.Params {
			if p.Kind != Enum {
				continue
			}
			switch p.Str {
			case "METRE":
				unit = "METRE"
			default:
				prefix = p.Str
			}
		}
		if unit != "METRE" {
			continue
		}
		switch prefix {
		case "MILLI":
			return 1, "mm"
		case "CENTI":
			return 10, "cm"
		case "":
			return 1000, "m"
		}
	}
	return 1, "mm (assumed)"
}

// ReadModel interprets a parsed file as a mandrel.
func ReadModel(f *File) (*Model, error) {
	scale, unitName := f.lengthScale()

	m := &Model{Header: Header{
		Schema:    f.Schema(),
		Units:     unitName,
		ScaleToMM: scale,
	}}
	if fn, ok := f.Header["FILE_NAME"]; ok {
		get := func(i int) string {
			if i < len(fn.Params) && fn.Params[i].Kind == Str {
				return fn.Params[i].Str
			}
			if i < len(fn.Params) && fn.Params[i].Kind == List && len(fn.Params[i].List) > 0 {
				return fn.Params[i].List[0].Str
			}
			return ""
		}
		m.Header.Name = get(0)
		m.Header.Timestamp = get(1)
		m.Header.Author = get(2)
		m.Header.Organization = get(3)
		m.Header.PreprocessorVer = get(4)
		m.Header.OriginatingSystem = get(5)
	}

	facePts := f.facePoints()
	var cands []Candidate

	for _, typ := range []string{
		"CYLINDRICAL_SURFACE", "CONICAL_SURFACE", "SPHERICAL_SURFACE",
		"TOROIDAL_SURFACE", "SURFACE_OF_REVOLUTION",
	} {
		for _, e := range f.OfType(typ) {
			c := Candidate{ID: e.ID, Type: typ}
			if len(e.Params) > 0 && e.Params[0].Kind == Str {
				c.Name = e.Params[0].Str
			}

			var ax Axis
			var ok bool
			if typ == "SURFACE_OF_REVOLUTION" {
				ax, ok = f.axisOf(e.Params[2].Ref)
			} else {
				ax, ok = f.axisOf(e.Params[1].Ref)
			}
			if !ok {
				c.Rejected = "could not resolve its axis"
				cands = append(cands, c)
				continue
			}
			c.Axis = ax
			cands = append(cands, c)
		}
	}

	// Surfaces exported as NURBS. Many CAD packages write every face this way,
	// so a file can contain no analytic surface entities at all and still be a
	// plainly axisymmetric part. See bspline.go.
	bsplines := f.bsplineCandidates()
	cands = append(cands, bsplines...)

	if len(cands) == 0 {
		return nil, fmt.Errorf("no axisymmetric surfaces found: this file may not contain a revolved solid")
	}

	// The mandrel axis is the one shared by the most surfaces. Consensus
	// rather than "the first one found", because a bore and a divot both have
	// axes too and the mandrel is whichever the bulk of the geometry agrees on.
	best, bestCount := Axis{}, 0
	for _, c := range cands {
		if c.Rejected != "" {
			continue
		}
		n := 0
		for _, o := range cands {
			if o.Rejected == "" && c.Axis.collinear(o.Axis, 1e-6) {
				n++
			}
		}
		if n > bestCount {
			best, bestCount = c.Axis, n
		}
	}
	m.Axis = best
	m.AxisLabel = axisLabel(best.Dir)

	for i := range cands {
		c := &cands[i]
		if c.Rejected != "" {
			continue
		}
		if !c.Axis.collinear(best, 1e-6) {
			c.Coaxial = false
			c.Rejected = "not coaxial with the mandrel axis"
			continue
		}
		c.Coaxial = true

		// Project this surface's face vertices onto the mandrel axis.
		pts := facePts[c.ID]
		if len(pts) == 0 {
			// No bounded face uses it. Hand-written fixtures and some exports
			// carry surfaces without topology; those cannot contribute an
			// extent, so they are reported rather than guessed at.
			c.Rejected = "no bounded face uses this surface, so its extent is unknown"
			continue
		}
		lo, hi := math.Inf(1), math.Inf(-1)
		origin := best.Origin.dot(best.Dir)
		for _, p := range pts {
			t := p.dot(best.Dir) - origin
			lo, hi = math.Min(lo, t), math.Max(hi, t)
		}
		c.AxialMin, c.AxialMax = lo*scale, hi*scale

		if c.fromBSpline {
			// Already carries its profile from the control net; just scale it
			// into millimetres and record the radius range.
			c.scaleSegments(scale, best)
			continue
		}

		segs, rmin, rmax, err := f.segmentsFor(*c, best, scale)
		if err != nil {
			c.Rejected = err.Error()
			continue
		}
		c.Segments, c.RadiusMin, c.RadiusMax = segs, rmin, rmax
	}

	// Internal features: a coaxial surface lying inside the outer envelope is
	// a bore, not the winding surface.
	//
	// This has to compare against the envelope of ALL the other surfaces, not
	// against any single one: a drive-shaft bore typically runs the full length
	// of the part, so no individual outer surface contains it -- the body
	// covers the first half and the nose the second. Testing pairwise misses
	// exactly the case the filter exists for.
	markInternalFeatures(cands)

	// A full revolve is often exported as two 180-degree halves, which produce
	// identical profiles. Selecting both would lay the same curve down twice
	// and double the mandrel outline, so fold duplicates into one entry that
	// records how many faces it covers.
	cands = mergeDuplicateProfiles(cands)

	// Put the part's own start at zero.
	//
	// The axis origin comes from whichever placement the file happened to
	// offer -- usually a circle at a section boundary -- so the raw axial
	// coordinates are arbitrary. Without this an import is at the mercy of
	// which circle appeared first in the file; the same part can come in at 0
	// or at -304 depending on nothing meaningful.
	normalizeAxialOrigin(cands)

	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Rejected == "" != (cands[j].Rejected == "") {
			return cands[i].Rejected == ""
		}
		return cands[i].AxialMin < cands[j].AxialMin
	})
	m.Candidates = cands
	return m, nil
}

func axisLabel(d Vec3) string {
	const tol = 1e-6
	switch {
	case math.Abs(d.X-1) < tol:
		return "+X"
	case math.Abs(d.X+1) < tol:
		return "-X"
	case math.Abs(d.Y-1) < tol:
		return "+Y"
	case math.Abs(d.Y+1) < tol:
		return "-Y"
	case math.Abs(d.Z-1) < tol:
		return "+Z"
	case math.Abs(d.Z+1) < tol:
		return "-Z"
	}
	return "custom"
}

// facePoints walks the topology to collect, for each surface, the VERTEX
// points of every face that uses it.
//
// STEP surfaces are unbounded -- a CYLINDRICAL_SURFACE describes an infinite
// cylinder -- so extent lives in the face boundary, not the surface. This
// traversal is what makes "where does the cylinder stop and the nose start"
// answerable.
//
// Only vertices count. Descending into the edge CURVES as well would sweep up
// B-spline control points, and those overshoot the geometry they describe: on
// a real ogive export the control hull reached 21 mm past the tip and 20 mm
// past the base, inflating a 12.7..304.8 mm surface to -21..325.
//
// Raw points are returned rather than a projected range because the mandrel
// axis is not known until every surface has been seen; projecting here would
// mean assuming an axis before choosing one.
func (f *File) facePoints() map[int][]Vec3 {
	out := map[int][]Vec3{}

	for _, face := range f.OfType("ADVANCED_FACE") {
		if len(face.Params) < 3 || face.Params[2].Kind != Ref {
			continue
		}
		surf := face.Params[2].Ref

		seen := map[int]bool{}
		var walk func(id, depth int)
		walk = func(id, depth int) {
			// Bounded because STEP references form cycles: an edge is shared
			// by two faces, each reachable from the other.
			if depth > 12 || seen[id] {
				return
			}
			seen[id] = true
			e, ok := f.Get(id)
			if !ok {
				return
			}

			switch e.Type {
			case "VERTEX_POINT":
				if len(e.Params) > 1 && e.Params[1].Kind == Ref {
					if v, ok := f.vec(e.Params[1].Ref); ok {
						out[surf] = append(out[surf], v)
					}
				}
				return

			case "EDGE_CURVE":
				// Params are (name, start_vertex, end_vertex, curve, sense).
				// Follow only the vertices; the curve's control points are not
				// on the surface boundary.
				for _, i := range []int{1, 2} {
					if i < len(e.Params) && e.Params[i].Kind == Ref {
						walk(e.Params[i].Ref, depth+1)
					}
				}
				return

			case "CARTESIAN_POINT":
				// Reached other than through a vertex; not a boundary point.
				return
			}

			for _, p := range e.Params {
				switch p.Kind {
				case Ref:
					walk(p.Ref, depth+1)
				case List:
					for _, q := range p.List {
						if q.Kind == Ref {
							walk(q.Ref, depth+1)
						}
					}
				}
			}
		}

		for _, p := range face.Params {
			if p.Kind == List {
				for _, q := range p.List {
					if q.Kind == Ref {
						walk(q.Ref, 0)
					}
				}
			}
		}
	}
	return out
}

// segmentsFor converts one surface into exact profile segments.
//
// The analytic surfaces give radius in closed form from their attributes, so
// only SURFACE_OF_REVOLUTION needs its generating curve read. That asymmetry
// is why handling the whole simple-surface family costs little: four of the
// five are a few lines each.
func (f *File) segmentsFor(c Candidate, axis Axis, scale float64) ([]Seg, float64, float64, error) {
	e, ok := f.Get(c.ID)
	if !ok {
		return nil, 0, 0, fmt.Errorf("entity vanished")
	}
	x0, x1 := c.AxialMin, c.AxialMax

	switch c.Type {
	case "CYLINDRICAL_SURFACE":
		r := e.Params[2].Num * scale
		return []Seg{{Kind: "line", Start: Point{x0, r}, End: Point{x1, r}}}, r, r, nil

	case "CONICAL_SURFACE":
		// A cone opens at semi_angle along ITS OWN axis, which is frequently
		// antiparallel to the mandrel axis -- that is how a narrowing nose is
		// encoded, since STEP requires a positive semi_angle. Ignoring the
		// sign makes every nose flare outwards instead of tapering.
		r0 := e.Params[2].Num * scale
		half := e.Params[3].Num
		own, _ := f.axisOf(e.Params[1].Ref)
		sign := 1.0
		if own.Dir.dot(axis.Dir) < 0 {
			sign = -1
		}
		base := (own.Origin.dot(axis.Dir) - axis.Origin.dot(axis.Dir)) * scale
		ra := r0 + sign*(x0-base)*math.Tan(half)
		rb := r0 + sign*(x1-base)*math.Tan(half)
		return []Seg{{Kind: "line", Start: Point{x0, ra}, End: Point{x1, rb}}},
			math.Min(ra, rb), math.Max(ra, rb), nil

	case "SPHERICAL_SURFACE":
		r := e.Params[2].Num * scale
		centre := (e0Origin(f, e.Params[1].Ref).dot(axis.Dir) - axis.Origin.dot(axis.Dir)) * scale
		ra := sphereRadiusAt(r, x0-centre)
		rb := sphereRadiusAt(r, x1-centre)
		return []Seg{{
			Kind: "arc", Radius: r,
			Center: Point{centre, 0},
			Start:  Point{x0, ra}, End: Point{x1, rb},
		}}, math.Min(ra, rb), math.Max(ra, rb), nil

	case "TOROIDAL_SURFACE":
		major := e.Params[2].Num * scale
		minor := e.Params[3].Num * scale
		centre := (e0Origin(f, e.Params[1].Ref).dot(axis.Dir) - axis.Origin.dot(axis.Dir)) * scale
		return []Seg{{
			Kind: "arc", Radius: minor,
			Center: Point{centre, major},
			Start:  Point{x0, major}, End: Point{x1, major},
		}}, major - minor, major + minor, nil

	case "SURFACE_OF_REVOLUTION":
		return f.curveSegments(e.Params[1].Ref, axis, scale)
	}
	return nil, 0, 0, fmt.Errorf("unsupported surface type %s", c.Type)
}

func sphereRadiusAt(r, d float64) float64 {
	if v := r*r - d*d; v > 0 {
		return math.Sqrt(v)
	}
	return 0
}

func e0Origin(f *File, placement int) Vec3 {
	if a, ok := f.axisOf(placement); ok {
		return a.Origin
	}
	return Vec3{}
}

// curveSegments reads a generating curve into profile segments.
func (f *File) curveSegments(id int, axis Axis, scale float64) ([]Seg, float64, float64, error) {
	e, ok := f.Get(id)
	if !ok {
		return nil, 0, 0, fmt.Errorf("generating curve #%d not found", id)
	}

	// In-plane coordinates: distance along the axis, and distance from it.
	toProfile := func(v Vec3) Point {
		d := v.sub(axis.Origin)
		ax := d.dot(axis.Dir)
		rad := d.sub(axis.Dir.scale(ax)).len()
		return Point{ax * scale, rad * scale}
	}

	switch e.Type {
	case "TRIMMED_CURVE":
		// The trim bounds matter for extent, but the underlying curve carries
		// the shape; recurse and let the face extents bound it.
		return f.curveSegments(e.Params[1].Ref, axis, scale)

	case "LINE":
		p, _ := f.vec(e.Params[1].Ref)
		vecEnt, ok := f.Get(e.Params[2].Ref)
		if !ok {
			return nil, 0, 0, fmt.Errorf("line #%d has no vector", id)
		}
		dir, _ := f.vec(vecEnt.Params[1].Ref)
		mag := vecEnt.Params[2].Num
		a := toProfile(p)
		b := toProfile(Vec3{p.X + dir.X*mag, p.Y + dir.Y*mag, p.Z + dir.Z*mag})
		return []Seg{{Kind: "line", Start: a, End: b}},
			math.Min(a.R, b.R), math.Max(a.R, b.R), nil

	case "CIRCLE":
		pl, _ := f.axisOf(e.Params[1].Ref)
		r := e.Params[2].Num * scale
		c := toProfile(pl.Origin)
		return []Seg{{
			Kind: "arc", Radius: r, Center: c,
			Start: Point{c.X - r, c.R}, End: Point{c.X + r, c.R},
		}}, math.Max(c.R-r, 0), c.R + r, nil

	case "POLYLINE":
		var pts []Point
		rmin, rmax := math.Inf(1), math.Inf(-1)
		for _, p := range e.Params {
			if p.Kind != List {
				continue
			}
			for _, q := range p.List {
				if q.Kind != Ref {
					continue
				}
				if v, ok := f.vec(q.Ref); ok {
					pt := toProfile(v)
					pts = append(pts, pt)
					rmin, rmax = math.Min(rmin, pt.R), math.Max(rmax, pt.R)
				}
			}
		}
		if len(pts) < 2 {
			return nil, 0, 0, fmt.Errorf("polyline #%d has fewer than 2 points", id)
		}
		return []Seg{{Kind: "polyline", Points: pts, Start: pts[0], End: pts[len(pts)-1]}},
			rmin, rmax, nil

	case "B_SPLINE_CURVE_WITH_KNOTS":
		seg := Seg{Kind: "spline", Degree: int(e.Params[1].Num)}
		rmin, rmax := math.Inf(1), math.Inf(-1)
		if e.Params[2].Kind == List {
			for _, q := range e.Params[2].List {
				if q.Kind != Ref {
					continue
				}
				if v, ok := f.vec(q.Ref); ok {
					pt := toProfile(v)
					seg.Control = append(seg.Control, pt)
					rmin, rmax = math.Min(rmin, pt.R), math.Max(rmax, pt.R)
				}
			}
		}
		// STEP stores distinct knots plus multiplicities; expand to the flat
		// vector de Boor wants.
		var mult []int
		var vals []float64
		for _, p := range e.Params {
			if p.Kind != List || len(p.List) == 0 {
				continue
			}
			if p.List[0].Kind == Int && mult == nil && len(seg.Control) > 0 {
				for _, q := range p.List {
					mult = append(mult, int(q.Num))
				}
				continue
			}
			if p.List[0].Kind == Real && vals == nil && mult != nil {
				for _, q := range p.List {
					vals = append(vals, q.Num)
				}
			}
		}
		for i, v := range vals {
			n := 1
			if i < len(mult) {
				n = mult[i]
			}
			for j := 0; j < n; j++ {
				seg.Knots = append(seg.Knots, v)
			}
		}
		if len(seg.Control) < 2 {
			return nil, 0, 0, fmt.Errorf("b-spline #%d has too few control points", id)
		}
		seg.Start, seg.End = seg.Control[0], seg.Control[len(seg.Control)-1]
		return []Seg{seg}, rmin, rmax, nil
	}
	return nil, 0, 0, fmt.Errorf("unsupported generating curve %s", e.Type)
}

// radiusAt evaluates a candidate's profile radius at an axial position,
// reporting false when the position lies outside its extent.
func (c Candidate) radiusAt(x float64) (float64, bool) {
	if x < c.AxialMin-1e-9 || x > c.AxialMax+1e-9 {
		return 0, false
	}
	for _, s := range c.Segments {
		lo, hi := s.Start.X, s.End.X
		if lo > hi {
			lo, hi = hi, lo
		}
		if x < lo-1e-9 || x > hi+1e-9 {
			continue
		}
		if hi-lo < 1e-12 {
			return s.Start.R, true
		}
		// Linear in the segment's own span is sufficient here: this is used
		// for inside/outside classification, not for the final geometry.
		t := (x - s.Start.X) / (s.End.X - s.Start.X)
		return s.Start.R + t*(s.End.R-s.Start.R), true
	}
	return 0, false
}

// markInternalFeatures rejects coaxial surfaces that sit inside the envelope
// formed by the others.
func markInternalFeatures(cands []Candidate) {
	const samples = 64

	live := func(i int) bool {
		return cands[i].Rejected == "" && len(cands[i].Segments) > 0
	}

	for i := range cands {
		if !live(i) {
			continue
		}
		c := cands[i]
		span := c.AxialMax - c.AxialMin
		if span <= 0 {
			continue
		}

		covered, inside := 0, 0
		for k := 0; k <= samples; k++ {
			x := c.AxialMin + span*float64(k)/samples
			mine, ok := c.radiusAt(x)
			if !ok {
				continue
			}
			outer, found := 0.0, false
			for j := range cands {
				if i == j || !live(j) {
					continue
				}
				if r, ok := cands[j].radiusAt(x); ok && r > outer {
					outer, found = r, true
				}
			}
			if !found {
				continue
			}
			covered++
			if mine < outer-1e-9 {
				inside++
			}
		}

		// Require the surface to be covered over most of its length and to be
		// inside wherever it is covered. A surface that pokes outside the
		// envelope anywhere is part of the winding surface, not a feature.
		if covered > samples/2 && inside == covered {
			cands[i].Rejected = "inside the outer envelope: an internal feature such as a bore"
		}
	}
}

// scaleSegments converts a detected NURBS profile into millimetre segments.
//
// It only scales. fitRevolution already measured the profile relative to the
// axis origin, so subtracting it again here would double the offset -- which
// it did, putting segments at twice the part's distance from the origin while
// the extents stayed correct.
func (c *Candidate) scaleSegments(scale float64, axis Axis) {
	pts := make([]Point, len(c.rawProfile))
	rmin, rmax := math.Inf(1), math.Inf(-1)
	axMin, axMax := math.Inf(1), math.Inf(-1)
	for i, p := range c.rawProfile {
		pts[i] = Point{X: p.X * scale, R: p.R * scale}
		rmin, rmax = math.Min(rmin, pts[i].R), math.Max(rmax, pts[i].R)
		axMin, axMax = math.Min(axMin, pts[i].X), math.Max(axMax, pts[i].X)
	}
	c.RadiusMin, c.RadiusMax = rmin, rmax

	// Face topology gives the authoritative extent; fall back to the control
	// hull when a face carried none.
	if c.AxialMax <= c.AxialMin {
		c.AxialMin, c.AxialMax = axMin, axMax
	}

	seg := Seg{
		Kind:    "spline",
		Degree:  c.rawDegree,
		Control: pts,
		Weights: c.rawWeights,
		Knots:   c.rawKnots,
		Start:   pts[0],
		End:     pts[len(pts)-1],
	}
	// A degree-1 profile is a straight line; emitting it as one keeps a
	// cylinder or cone exactly straight rather than relying on spline
	// evaluation to reproduce it.
	if c.rawDegree <= 1 && len(pts) == 2 {
		seg = Seg{Kind: "line", Start: pts[0], End: pts[1]}
	}
	c.Segments = []Seg{seg}
}

// bsplineCandidates finds every NURBS surface that is a surface of revolution.
func (f *File) bsplineCandidates() []Candidate {
	axes := f.candidateAxes()
	var out []Candidate

	// Faces tell us which surfaces are actually used by the solid, and give a
	// surface its name. A surface entity nothing references is not part of the
	// shape.
	usedBy := map[int]string{}
	for _, face := range f.OfType("ADVANCED_FACE") {
		if len(face.Params) >= 3 && face.Params[2].Kind == Ref {
			name := ""
			if face.Params[0].Kind == Str {
				name = face.Params[0].Str
			}
			usedBy[face.Params[2].Ref] = name
		}
	}

	for id, name := range usedBy {
		e, ok := f.Get(id)
		if !ok {
			continue
		}
		if !e.Has("B_SPLINE_SURFACE") && !e.Has("B_SPLINE_SURFACE_WITH_KNOTS") {
			continue // an analytic surface, handled elsewhere
		}
		surf, ok := f.readBSplineSurface(e)
		if !ok {
			continue
		}

		// Tolerance scaled to the surface's own size: an absolute epsilon is
		// either too tight for a metre-scale part or too loose for a
		// millimetre-scale one.
		extent := surf.extent()
		tol := math.Max(extent*1e-6, 1e-12)

		matched := false
		for _, ax := range axes {
			fit, ok := surf.fitRevolution(ax, tol)
			if !ok {
				continue
			}
			label := name
			if label == "" {
				label = describeForm(surf.Form)
			}
			out = append(out, Candidate{
				ID:          id,
				Type:        "B_SPLINE_SURFACE",
				Name:        label,
				Axis:        ax,
				fromBSpline: true,
				rawProfile:  fit.Profile,
				rawWeights:  fit.Weights,
				rawDegree:   fit.Degree,
				rawKnots:    fit.Knots,
			})
			matched = true
			break
		}
		if !matched {
			label := name
			if label == "" {
				label = describeForm(surf.Form)
			}
			out = append(out, Candidate{
				ID:       id,
				Type:     "B_SPLINE_SURFACE",
				Name:     label,
				Rejected: "not a surface of revolution about any candidate axis",
			})
		}
	}
	return out
}

// extent is the diagonal of the control net's bounding box.
func (s *bsplineSurface) extent() float64 {
	lo := Vec3{math.Inf(1), math.Inf(1), math.Inf(1)}
	hi := Vec3{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	for _, row := range s.Net {
		for _, p := range row {
			lo = Vec3{math.Min(lo.X, p.X), math.Min(lo.Y, p.Y), math.Min(lo.Z, p.Z)}
			hi = Vec3{math.Max(hi.X, p.X), math.Max(hi.Y, p.Y), math.Max(hi.Z, p.Z)}
		}
	}
	return hi.sub(lo).len()
}

// describeForm turns the surface_form hint into a label. The hint is shown as
// a description only -- it is not used to decide anything, because exporters
// write it loosely (a tangent ogive arrives tagged .TOROIDAL_SURF.).
func describeForm(form string) string {
	switch form {
	case "CYLINDRICAL_SURF":
		return "cylindrical face"
	case "CONICAL_SURF":
		return "conical face"
	case "TOROIDAL_SURF":
		return "curved face"
	case "SPHERICAL_SURF":
		return "spherical face"
	case "PLANE_SURF":
		return "planar face"
	}
	return "spline face"
}

// MergedIDs lists the duplicate faces folded into this candidate.
func (c Candidate) MergedIDs() []int { return c.mergedIDs }

// Faces is how many faces this candidate represents. A full revolve exported
// as halves yields one candidate covering several faces.
func (c Candidate) FaceCount() int {
	if c.faces < 1 {
		return 1
	}
	return c.faces
}

// sameProfile reports whether two candidates describe the same outline.
func sameProfile(a, b Candidate, tol float64) bool {
	if len(a.Segments) != len(b.Segments) {
		return false
	}
	near := func(x, y float64) bool { return math.Abs(x-y) <= tol }
	if !near(a.AxialMin, b.AxialMin) || !near(a.AxialMax, b.AxialMax) ||
		!near(a.RadiusMin, b.RadiusMin) || !near(a.RadiusMax, b.RadiusMax) {
		return false
	}
	for i := range a.Segments {
		x, y := a.Segments[i], b.Segments[i]
		if x.Kind != y.Kind || len(x.Control) != len(y.Control) {
			return false
		}
		if !near(x.Start.X, y.Start.X) || !near(x.Start.R, y.Start.R) ||
			!near(x.End.X, y.End.X) || !near(x.End.R, y.End.R) {
			return false
		}
		for k := range x.Control {
			if !near(x.Control[k].X, y.Control[k].X) || !near(x.Control[k].R, y.Control[k].R) {
				return false
			}
		}
	}
	return true
}

func mergeDuplicateProfiles(cands []Candidate) []Candidate {
	const tol = 1e-6
	var out []Candidate
	for _, c := range cands {
		merged := false
		if c.Rejected == "" && len(c.Segments) > 0 {
			for i := range out {
				if out[i].Rejected == "" && sameProfile(out[i], c, tol) {
					out[i].faces = out[i].FaceCount() + 1
					out[i].mergedIDs = append(out[i].mergedIDs, c.ID)
					merged = true
					break
				}
			}
		}
		if !merged {
			out = append(out, c)
		}
	}
	return out
}

// normalizeAxialOrigin shifts every candidate so the accepted geometry starts
// at axial zero, which is also where a carriage naturally starts.
func normalizeAxialOrigin(cands []Candidate) {
	shift := math.Inf(1)
	for _, c := range cands {
		if c.Rejected == "" && len(c.Segments) > 0 && c.AxialMin < shift {
			shift = c.AxialMin
		}
	}
	if math.IsInf(shift, 1) || shift == 0 {
		return
	}
	for i := range cands {
		c := &cands[i]
		c.AxialMin -= shift
		c.AxialMax -= shift
		for j := range c.Segments {
			s := &c.Segments[j]
			s.Start.X -= shift
			s.End.X -= shift
			s.Center.X -= shift
			for k := range s.Control {
				s.Control[k].X -= shift
			}
			for k := range s.Points {
				s.Points[k].X -= shift
			}
		}
	}
}
