// Package wind - deposition tracking.
//
// This measures where fiber actually lands on the mandrel, which answers three
// questions the path alone cannot:
//
//   - How thick is the layup at each axial station? (bulges and dips)
//   - Is the surface fully covered, or are there bare stripes?
//   - What does the mandrel look like once this layer is on it?
//
// Method: volume conservation. A tow of width w and thickness t laid along a
// path of length ds deposits w*t*ds of material. Spread over the patch of
// mandrel surface it covers, that gives a local thickness. Nothing here
// models compaction, resin bleed, or tow spreading -- it is a geometric
// accounting of where the fiber goes.
package wind

import "math"

// Coverage accumulates deposition over a 2D grid of the mandrel surface:
// axial stations by angular sectors.
//
// The angular dimension is what distinguishes "covered" from "thick on
// average". A layer can deposit plenty of material at a station and still
// leave bare stripes between passes; averaging over theta hides exactly the
// defect you want to catch.
type Coverage struct {
	// NX axial stations spanning [XMin, XMax], NTheta angular sectors.
	NX, NTheta int
	XMin, XMax float64
	DX         float64 // axial width of one station, mm

	// Radius is the mandrel radius at each station's center.
	Radius []float64

	// Area[i*NTheta+j] is the fiber footprint area deposited in station i,
	// sector j, in mm^2.
	Area []float64

	// PathLength[i] is total tow path length crossing station i, in mm.
	// Useful for time and filament-usage estimates.
	PathLength []float64
}

// NewCoverage prepares an accumulator over the mandrel's axial extent.
//
// nx trades resolution against noise: too few stations and a narrow bulge
// averages away, too many and each station sees so little path that the
// result is dominated by discretization of the path itself.
func NewCoverage(m *Mandrel, nx, ntheta int) *Coverage {
	if nx < 2 {
		nx = 2
	}
	if ntheta < 1 {
		ntheta = 1
	}
	c := &Coverage{
		NX: nx, NTheta: ntheta,
		XMin: m.XMin, XMax: m.XMax,
		Radius:     make([]float64, nx),
		Area:       make([]float64, nx*ntheta),
		PathLength: make([]float64, nx),
	}
	c.DX = (m.XMax - m.XMin) / float64(nx)
	if c.DX <= 0 {
		c.DX = 1
	}
	for i := 0; i < nx; i++ {
		c.Radius[i] = m.Interp(c.XMin + (float64(i)+0.5)*c.DX)
	}
	return c
}

// station returns the axial bin index for x, clamped to the grid.
func (c *Coverage) station(x float64) int {
	i := int((x - c.XMin) / c.DX)
	if i < 0 {
		return 0
	}
	if i >= c.NX {
		return c.NX - 1
	}
	return i
}

// sector returns the angular bin index for an angle in degrees.
func (c *Coverage) sector(aDeg float64) int {
	a := math.Mod(aDeg, 360)
	if a < 0 {
		a += 360
	}
	j := int(a / 360 * float64(c.NTheta))
	if j >= c.NTheta {
		return c.NTheta - 1
	}
	return j
}

// AddPath accumulates one layer's deposition.
//
// The tow is a BAND, not a line, and spreading it correctly is what makes the
// resulting thickness physical. A tow of width w running at angle a from the
// mandrel axis presents its width perpendicular to its own direction, so the
// band covers:
//
//	axially:          w * sin(a)     (a hoop pass is w wide along the axis;
//	                                  an axial pass has no axial width)
//	circumferentially: w * cos(a)    (and the reverse)
//
// Depositing a segment's whole footprint at a single station instead -- as a
// naive implementation does -- concentrates a 20 mm tow into one 0.5 mm bin
// and reports tens of millimetres of thickness on a part that has under one.
// It also reports bare sectors everywhere, because the band never lands in
// the bins it actually covers.
func (c *Coverage) AddPath(path []Point, f Filament) {
	if len(path) < 2 || f.Width <= 0 {
		return
	}

	for k := 1; k < len(path); k++ {
		p0, p1 := path[k-1], path[k]

		dx := p1.X - p0.X
		rMid := (p0.Z + p1.Z) / 2
		if rMid <= 0 {
			continue
		}
		dArc := (p1.A - p0.A) * math.Pi / 180 * rMid
		ds := math.Hypot(dx, dArc)
		if ds <= 0 {
			continue
		}

		// Local winding angle from the mandrel axis.
		sinA := math.Abs(dArc) / ds
		cosA := math.Abs(dx) / ds

		// Half-extents of the band around the path centreline.
		halfAxial := f.Width * sinA / 2
		halfArc := f.Width * cosA / 2
		halfDeg := halfArc / rMid * 180 / math.Pi

		// Substep so no substep skips a bin along the path.
		nAxial := math.Abs(dx) / c.DX
		nAng := math.Abs(p1.A-p0.A) / (360 / float64(c.NTheta))
		n := int(math.Ceil(math.Max(nAxial, nAng)))
		if n < 1 {
			n = 1
		}

		// Total material this substep lays down, spread over the band.
		segArea := f.Width * ds / float64(n)
		segLen := ds / float64(n)

		for st := 0; st < n; st++ {
			t := (float64(st) + 0.5) / float64(n)
			x := p0.X + t*dx
			a := p0.A + t*(p1.A-p0.A)

			iLo := c.station(x - halfAxial)
			iHi := c.station(x + halfAxial)
			nBinX := iHi - iLo + 1

			// Angular span in sectors. A full wrap saturates at NTheta.
			nBinT := int(math.Ceil(2*halfDeg/(360/float64(c.NTheta)))) + 1
			if nBinT > c.NTheta {
				nBinT = c.NTheta
			}

			share := segArea / float64(nBinX*nBinT)
			lenShare := segLen / float64(nBinX)

			jStart := c.sector(a - halfDeg)
			for bi := 0; bi < nBinX; bi++ {
				i := iLo + bi
				c.PathLength[i] += lenShare
				row := i * c.NTheta
				for bj := 0; bj < nBinT; bj++ {
					j := (jStart + bj) % c.NTheta
					c.Area[row+j] += share
				}
			}
		}
	}
}

// StationReport summarises deposition at one axial station.
type StationReport struct {
	X         float64 // station center, mm
	Radius    float64 // mandrel radius here, mm
	Thickness float64 // mean deposited thickness, mm
	Fraction  float64 // mean areal coverage; 1.0 == exactly one tow thickness
	MinFrac   float64 // worst sector; 0 means a bare stripe
	Gap       bool    // true when some sector received nothing
}

// Report reduces the grid to a per-station summary.
//
// Thickness is derived by volume conservation: footprint area times tow
// thickness, spread over the station's surface area. Fraction is the same
// quantity before multiplying by thickness, so 0.5 means half the surface got
// one tow's worth and 2.0 means two tows deep on average.
func (c *Coverage) Report(f Filament) []StationReport {
	out := make([]StationReport, c.NX)
	for i := 0; i < c.NX; i++ {
		r := c.Radius[i]
		// Surface area of one sector of this station.
		sectorArea := 2 * math.Pi * r * c.DX / float64(c.NTheta)

		total := 0.0
		minFrac := math.Inf(1)
		gap := false
		for j := 0; j < c.NTheta; j++ {
			a := c.Area[i*c.NTheta+j]
			total += a
			frac := 0.0
			if sectorArea > 0 {
				frac = a / sectorArea
			}
			if frac < minFrac {
				minFrac = frac
			}
			if a == 0 {
				gap = true
			}
		}
		if math.IsInf(minFrac, 1) {
			minFrac = 0
		}

		stationArea := 2 * math.Pi * r * c.DX
		frac := 0.0
		if stationArea > 0 {
			frac = total / stationArea
		}
		out[i] = StationReport{
			X:         c.XMin + (float64(i)+0.5)*c.DX,
			Radius:    r,
			Thickness: frac * f.Thickness,
			Fraction:  frac,
			MinFrac:   minFrac,
			Gap:       gap,
		}
	}
	return out
}

// FiberLength returns the total tow path length recorded, in mm.
func (c *Coverage) FiberLength() float64 {
	total := 0.0
	for _, l := range c.PathLength {
		total += l
	}
	return total
}

// GrownMandrel returns a copy of m with each station's radius increased by the
// deposited thickness, i.e. the surface the NEXT layer would be wound onto.
//
// Not yet wired into generation: feeding this back would change every
// multi-layer path, so it is opt-in pending a decision on whether layer
// buildup should affect subsequent layers' geometry.
func (c *Coverage) GrownMandrel(m *Mandrel, f Filament) (*Mandrel, error) {
	rep := c.Report(f)
	pts := make([][]float64, len(rep))
	for i, s := range rep {
		pts[i] = []float64{s.X, s.Radius + s.Thickness}
	}
	return NewMandrelFromPoints(pts)
}
