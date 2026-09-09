// Package wind - Path generation functions.
// This file contains the core algorithms for generating hoop and helical winding paths.
package wind

import (
	"fmt"
	"math"
)

// maxLayerPoints bounds how much geometry a single layer may generate. At
// 40 bytes per Point this is roughly 200 MB, well past any real wind but far
// short of exhausting memory. It exists so that implausible parameters fail
// with a message instead of hanging or OOM-ing the GUI.
const maxLayerPoints = 5_000_000

// MaxTotalPoints bounds the whole wind, not just one layer.
//
// Sizing came from the stated worst case (docs/REQUIREMENTS.md §9): an 8" x 4'
// tube at a 0.2" wall, wound with thin 3 mm x 0.1 mm E-glass roving. That is
// ~51 layers; the heaviest single layer is ~808k points, comfortably inside
// maxLayerPoints, but the TOTAL reaches ~41 million points -- about 1.6 GB of
// Point structs before the renderer's vertex buffer is even allocated.
//
// So the per-layer guard was correctly sized and the aggregate one was simply
// missing. 8 million points is roughly 320 MB of Points plus ~100 MB of
// vertices, which a browser tab can hold; past that the honest answer is that
// this design materialises every point up front and cannot go further without
// streaming or decimating. Failing with a message beats an OOM.
const MaxTotalPoints = 8_000_000

// gcd calculates the greatest common divisor of two integers.
// This is a helper function for the helical path calculation.
// Go's standard library has math/big for big integers, but for regular ints we implement it.
func gcd(a, b int) int {
	// Euclidean algorithm
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		return -a
	}
	return a
}

// mod360 returns a value equivalent to x % 360.0 in Python, i.e. always in [0, 360).
// This is important for matching the original helical path logic on arbitrary profiles.
func mod360(x float64) float64 {
	r := math.Mod(x, 360.0)
	if r < 0 {
		r += 360.0
	}
	return r
}

// creates a copy of a given point list with the angles shifted by some offset
func angleOffset(Points []Point, offset float64) []Point {
	r := make([]Point, len(Points))
	copy(r, Points)
	for i := range r {
		r[i].A += offset
	}
	return r
}

// takes into acount both the given offset, and the offset at the end of a point list
// useful when adding paths together, preserves desired absolute position
//
// The result is always a freshly allocated slice. Appending directly onto pts1
// would write into its backing array whenever it has spare capacity, which
// would silently corrupt aliases such as Layer.FWPath.
func offsetConcat(pts1 []Point, pts2 []Point, offsetBetween float64) []Point {
	if len(pts1) >= 1 {
		offsetBetween += pts1[len(pts1)-1].A
	}
	r := make([]Point, 0, len(pts1)+len(pts2))
	r = append(r, pts1...)
	r = append(r, angleOffset(pts2, offsetBetween)...)
	return r
}

// offsetConcat but repeating the operation n times
// useful for constructing inner repeats on helical, and all outer repeats
func offsetRepeat(pts []Point, n int, offset float64) []Point {
	if n <= 0 || len(pts) == 0 {
		return nil
	}

	// Start with a single copy of the base path.
	r := make([]Point, len(pts))
	copy(r, pts)

	for i := 1; i < n; i++ {
		r = offsetConcat(r, pts, offset)
	}
	return r
}

// pathAngleSpan returns the net angular advance across a path, in degrees.
// Paths are generated starting at A=0, so this is normally just the final
// angle, but taking the difference keeps it correct for any path.
func pathAngleSpan(pts []Point) float64 {
	if len(pts) < 2 {
		return 0
	}
	return pts[len(pts)-1].A - pts[0].A
}

// GenPointsHoop generates points for a hoop (circumferential) winding pattern.
// A hoop pattern winds around the mandrel at a constant angle (90 degrees).
// Parameters:
//   - mandrel: The mandrel profile
//   - stepover: Distance between each hoop pass in mm
//
// Returns two paths: forward (fwpath) and backward (bwpath).
// In Go, we return multiple values using parentheses, unlike Python's tuple unpacking.
func GenPointsHoop(mandrel *Mandrel, stepover float64) ([]Point, []Point) {
	// Calculate number of steps
	// In Go, int() truncates (like Python's int()), but we want floor division
	// math.Floor and int conversion work together for this
	nsteps := int(math.Floor(mandrel.Length / stepover))

	// Number of angular segments per hoop revolution.
	// More segments gives smoother rendering and motion.
	segs := 72
	if segs < 8 {
		segs = 8
	}

	ptsPerStep := segs + 1 // include last point at 360 to close the hoop
	fwpath := make([]Point, 0, nsteps*ptsPerStep)
	bwpath := make([]Point, 0, nsteps*ptsPerStep)

	// Generate points
	// Go's for loop is more explicit than Python's range()
	// for init; condition; post { } is like Python's for i in range(n)
	for n := 0; n < nsteps; n++ {
		// Calculate positions in a 0..Length local frame.
		xfw := float64(n) * stepover // float64(n) converts int to float64
		xbw := mandrel.Length - xfw

		// Interpolate to get radius at these positions
		zfw := mandrel.Interp(xfw)
		zbw := mandrel.Interp(xbw)

		// Create a full hoop (circle) at each x position.
		abase := float64(n) * 360.0
		for k := 0; k <= segs; k++ {
			a := abase + float64(k)*360.0/float64(segs)
			fwpath = append(fwpath, NewPoint(xfw, 0, zfw, a))
			bwpath = append(bwpath, NewPoint(xbw, 0, zbw, a))
		}
	}

	return fwpath, bwpath
}

// GenPointsHelical generates points for a helical winding pattern.
// A helical pattern winds at an angle relative to the mandrel axis.
// Parameters:
//   - mandrel: The mandrel profile
//   - angle: Helical angle in degrees (measured from axis)
//
// Returns forward and backward paths.
func GenPointsHelical(mandrel *Mandrel, angle float64) ([]Point, []Point) {
	// Resolution: 5mm steps
	res := 5.0
	nsteps := int(math.Ceil(mandrel.Length / res))
	dx := mandrel.Length / float64(nsteps)
	nsteps += 1 // keeps us from having to special case the last point

	// Pre-allocate slices
	fwpath := make([]Point, 0, nsteps)
	bwpath := make([]Point, 0, nsteps)

	// Initialize angle accumulators
	afw := 0.0
	abw := 0.0

	// Generate points
	for n := 0; n < nsteps; n++ {
		xfw := float64(n) * dx
		xbw := mandrel.Length - xfw

		// Get radius at these positions
		zfw := mandrel.Interp(xfw)
		zbw := mandrel.Interp(xbw)

		// Y is the payout eye angle for this pass, not a position.
		// In Python: y = 90 - angle
		//
		// The eye leans the other way on the return pass: the fiber approaches
		// from the opposite side when the carriage travels -X, so the backward
		// path takes -y. Only the eye flips -- A keeps increasing on both
		// passes, because the mandrel spins continuously in one direction.
		y := 90.0 - angle

		// Create points
		fwpath = append(fwpath, NewPoint(xfw, y, zfw, afw))
		bwpath = append(bwpath, NewPoint(xbw, -y, zbw, abw))

		// Update angles for next iteration.
		//
		// For a tow at `angle` from the mandrel axis advancing dx axially, the
		// circumferential arc it sweeps is dx*tan(angle), so the spindle
		// advance is dx*tan(angle)/r. Two bugs previously lived on this line:
		//
		//  1. the angle in RADIANS was used where tan(angle) belongs, so 45
		//     degrees actually laid 37.9 and 60 laid 45.9;
		//  2. the result was wrapped in atan, which treats the step as a chord
		//     through the solid. The tow lies ON the surface, so r*dTheta =
		//     dx*tan(angle) exactly, with no arctangent.
		//
		// z is the radius here.
		tanA := math.Tan(angle * math.Pi / 180)
		afw += 180 / math.Pi * (tanA * dx / zfw)
		abw += 180 / math.Pi * (tanA * dx / zbw)
	}

	return fwpath, bwpath
}

// Layer2Path generates the full path for a layer based on its type.
// This is the main function that converts layer configuration into a point path.
// Parameters:
//   - mandrel: The mandrel profile
//   - filament: Filament properties (needed for width calculations)
//   - layer: The layer configuration
//
// Returns the complete path as a slice of Points.
func Layer2Path(mandrel *Mandrel, filament Filament, layer *Layer) ([]Point, error) {
	var fullpath []Point // nil slice (empty)

	switch layer.LType {
	case "hoop":
		// Reject values that would make the step count non-finite. Without this
		// a stepover of 0 yields int(+Inf) steps and an unbounded allocation.
		stepover := layer.Params.Stepover
		if math.IsNaN(stepover) || stepover <= 0 {
			return nil, fmt.Errorf("hoop layer: stepover must be greater than 0, got %g", stepover)
		}
		if stepover > mandrel.Length {
			return nil, fmt.Errorf("hoop layer: stepover %g mm exceeds mandrel length %g mm, so no passes would be generated", stepover, mandrel.Length)
		}
		// A very small stepover is finite but generates one full 73-point hoop
		// per step; bound it the same way the helical case is bounded.
		if est := (mandrel.Length / stepover) * 73; est > maxLayerPoints {
			return nil, fmt.Errorf(
				"hoop layer: stepover %g mm over a %g mm mandrel would generate about %.3g points (limit %d)",
				stepover, mandrel.Length, est, maxLayerPoints)
		}

		// Generate forward and backward paths
		fwpath, bwpath := GenPointsHoop(mandrel, stepover)
		layer.FWPath = fwpath
		layer.BWPath = bwpath

		// reverse start direction if needed
		if layer.RevStart {
			fwpath, bwpath = bwpath, fwpath
		}

		// nrepeat == 0 disables the layer: it stays in the file, keeps its
		// parameters, and contributes nothing to the path or the G-code.
		// This is a mute switch, not a delete.
		repeat := layer.Repeat
		if repeat <= 0 {
			layer.Disabled = true
			layer.DAInner = 0
			layer.DAOuter = 0
			layer.FullPath = nil
			return nil, nil
		}

		// Alternate between forward and backward paths on each repeat.
		//
		// These must be chained with offsetConcat, not plain append: both
		// fwpath and bwpath start at A=0, so appending them raw made the angle
		// jump back to zero at every pass boundary. That is a commanded
		// unwind of thousands of degrees, and it also tripped the old
		// "A == 0 means rapid" rule in Layers2Gcode, emitting a G0 mid-path.
		for i := 0; i < repeat; i++ {
			pass := fwpath
			if i%2 == 1 {
				pass = bwpath
			}
			fullpath = offsetConcat(fullpath, pass, 0.0)
		}

		// One one-way pass of angular advance; see the Layer.DAInner docs.
		layer.DAInner = pathAngleSpan(fwpath)

	case "helical":
		// nrepeat == 0 disables the layer; check before doing any work.
		if layer.Repeat <= 0 {
			layer.Disabled = true
			layer.DAInner = 0
			layer.DAOuter = 0
			layer.FullPath = nil
			return nil, nil
		}

		// sin(angle) appears in the inner-repeat denominator below, so an angle
		// of 0 or 180 makes innerRepeat non-finite and the search loop that
		// follows can never satisfy its exit condition.
		if a := layer.Params.Angle; math.IsNaN(a) || a <= 0 || a >= 180 {
			return nil, fmt.Errorf("helical layer: angle must be between 0 and 180 degrees (exclusive), got %g", a)
		}

		// Generate base forward and backward paths
		fwpath, bwpath := GenPointsHelical(mandrel, layer.Params.Angle)
		layer.FWPath = fwpath
		layer.BWPath = bwpath

		// reverse start direction if needed
		if layer.RevStart {
			fwpath, bwpath = bwpath, fwpath
		}

		angle := layer.Params.Angle

		// Calculate inner angle differences
		// In Python: da_inner_fw = (l.fwpath[-1]-l.fwpath[0]).A %360
		// Go: we need to access slice elements explicitly
		if len(fwpath) == 0 || len(bwpath) == 0 {
			return nil, fmt.Errorf("helical paths are empty")
		}

		// Python uses the % operator for modulo, which always returns a value in [0, 360)
		// even when the left-hand side is negative. Go's math.Mod, by contrast, can
		// return a negative result. On non‑cylindrical profiles where the net angle
		// change over a pass can wrap, this difference changes da_inner and thus
		// the computed inner_repeat/da_end, so normalize explicitly rather than
		// relying on the accumulated value staying in range.
		daInnerFW := mod360(fwpath[len(fwpath)-1].A - fwpath[0].A)
		daInnerBW := mod360(bwpath[len(bwpath)-1].A - bwpath[0].A)
		daInner := (daInnerFW + daInnerBW) / 2.0

		// Calculate inner repeat necessary to cover widest part of mandrel
		zmax := mandrel.MaxZ()
		dmax := zmax * 2.0 * math.Pi

		innerRepeat := dmax / (filament.Width * math.Sin(angle*math.Pi/180.0))
		innerRepeat = math.Ceil(innerRepeat/2.0) * 4.0 // Round up to nearest even number

		// A non-finite or non-positive innerRepeat (filament width <= 0, or a
		// degenerate mandrel) would make the search below non-terminating and
		// the offsetRepeat call after it allocate without bound.
		if math.IsNaN(innerRepeat) || math.IsInf(innerRepeat, 0) || innerRepeat < 1 {
			return nil, fmt.Errorf("helical layer: computed inner repeat %g is not usable; check filament width (%g mm) and mandrel radius (%g mm)", innerRepeat, filament.Width, zmax)
		}

		// innerRepeat scales as 1/filamentWidth, so a small-but-legal width
		// stays finite while exploding into billions of circuits. That both
		// makes the search loop below effectively non-terminating and makes
		// the offsetRepeat calls allocate far past available memory, so bound
		// the total geometry rather than just its individual factors.
		repeatCount := layer.Repeat
		if repeatCount <= 0 {
			repeatCount = 1
		}
		estPoints := innerRepeat * float64(len(fwpath)+len(bwpath)) * float64(repeatCount)
		if estPoints > maxLayerPoints {
			return nil, fmt.Errorf(
				"helical layer: this configuration would generate about %.3g points (limit %d); filament width %g mm is very small relative to the mandrel circumference %.1f mm, requiring %.0f circuits",
				estPoints, maxLayerPoints, filament.Width, dmax, innerRepeat)
		}

		// Minimum da at the ends
		// TODO calculate this using the mandrel profile and fancy math
		daEndMin := 180.0 - 2.0*angle

		// Find da_end that satisfies conditions
		// Original algorithm, kept as a derivation of the search below:
		//   n = 0
		//   da_end = -1
		//   while da_end < da_end_min or gcd(int(da_inner_adjusted*inner_repeat/360),
		//                                   int(inner_repeat)) > 1:
		//       da_end = n*360/inner_repeat - da_inner
		//       da_inner_adjusted = da_inner + da_end
		//       n += 1
		//
		// Both conditions must hold before exiting: da_end >= da_end_min AND
		// gcd(...) == 1, so successive circuits do not retrace each other.
		// The smallest satisfying n is bounded by roughly
		// (daEndMin + daInner) * innerRepeat / 360 plus a short coprimality
		// search, so this cap is generous. It exists so that an unforeseen
		// degenerate input surfaces as an error instead of hanging the caller.
		maxIter := int(10*innerRepeat) + 1000

		n := 0
		daEnd := -1.0
		daInnerAdjusted := 0.0
		found := false

		for n = 0; n < maxIter; n++ {
			daEnd = float64(n)*360.0/innerRepeat - daInner
			daInnerAdjusted = daInner + daEnd

			val1 := int(daInnerAdjusted * innerRepeat / 360.0)
			val2 := int(innerRepeat)

			// Stop only when BOTH:
			//   - daEnd >= daEndMin
			//   - gcd(val1, val2) == 1
			if !(daEnd < daEndMin || gcd(val1, val2) > 1) {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("helical layer: no end angle satisfying the winding constraints found within %d iterations (angle %g, inner repeat %g)", maxIter, angle, innerRepeat)
		}

		// Warning check (Python raises Warning, Go doesn't have warnings, so we'll log it)
		if daEnd > math.Max(90.0, 2.0*daEndMin) {
			// In production, you might want to log this
			// For now, we'll just continue
		}

		// Add da_end points to paths
		partpath := offsetConcat(fwpath, bwpath, daEnd)
		innerpath := offsetRepeat(partpath, int(innerRepeat), daEnd)
		fullpath = offsetRepeat(innerpath, layer.Repeat, 0.0)

		// Angular advance of one inner circuit; see the Layer.DAInner docs.
		layer.DAInner = daInner

	default:
		return nil, fmt.Errorf("layer type '%s' unrecognized for mandrel type 'arbitrary_axial2'", layer.LType)
	}

	// DAOuter is the layer's total angular advance, which Layers2Gcode adds to
	// its running aStart so the *next* layer begins at the angle this one
	// ended on. It is what lets the spindle be driven in ABSOLUTE mode.
	// Leaving it at zero (as it was) made every layer restart from A=0.
	layer.DAOuter = pathAngleSpan(fullpath)

	layer.FullPath = fullpath
	return fullpath, nil
}
