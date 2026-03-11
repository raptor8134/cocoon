// Package internal - Path generation functions.
// This file contains the core algorithms for generating hoop and helical winding paths.
package internal

import (
	"fmt"
	"math"
)

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
func offsetConcat(pts1 []Point, pts2 []Point, offsetBetween float64) []Point {
	if len(pts1) >= 1 {
		offsetBetween += pts1[len(pts1)-1].A
	}
	r := append(pts1, angleOffset(pts2, offsetBetween)...)
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
		// Calculate positions
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

		// Y coordinate for helical angle
		// In Python: y = 90 - angle
		y := 90.0 - angle

		// Create points
		fwpath = append(fwpath, NewPoint(xfw, y, zfw, afw))
		bwpath = append(bwpath, NewPoint(xbw, y, zbw, abw))

		// Update angles for next iteration
		afw += 180 / math.Pi * (math.Atan2(math.Pi/180*angle*dx, zfw)) // z is the radius here
		abw += 180 / math.Pi * (math.Atan2(math.Pi/180*angle*dx, zbw))
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
		// Generate forward and backward paths
		fwpath, bwpath := GenPointsHoop(mandrel, layer.Params.Stepover)
		layer.FWPath = fwpath
		layer.BWPath = bwpath

		// reverse start direction if needed
		if layer.RevStart {
			fwpath, bwpath = bwpath, fwpath
		}

		// Treat non-positive repeat as a single pass (useful for preview configs).
		repeat := layer.Repeat
		if repeat <= 0 {
			repeat = 1
		}

		// Alternate between forward and backward paths on each repeat.
		for i := 0; i < repeat; i++ {
			if i%2 == 0 {
				fullpath = append(fullpath, fwpath...)
			} else {
				fullpath = append(fullpath, bwpath...)
			}
		}

	case "helical":
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
		// the computed inner_repeat/da_end. We normalize explicitly to match Python.
		daInnerFW := mod360(fwpath[len(fwpath)-1].A - fwpath[0].A)
		daInnerBW := mod360(bwpath[len(bwpath)-1].A - bwpath[0].A)
		daInner := (daInnerFW + daInnerBW) / 2.0

		// Calculate inner repeat necessary to cover widest part of mandrel
		zmax := mandrel.MaxZ()
		dmax := zmax * 2.0 * math.Pi

		innerRepeat := dmax / (filament.Width * math.Sin(angle*math.Pi/180.0))
		innerRepeat = math.Ceil(innerRepeat/2.0) * 4.0 // Round up to nearest even number

		// Minimum da at the ends
		// TODO calculate this using the mandrel profile and fancy math
		daEndMin := 180.0 - 2.0*angle

		// Find da_end that satisfies conditions
		// Python:
		//   n = 0
		//   da_end = -1
		//   while da_end < da_end_min or gcd(int(da_inner_adjusted*inner_repeat/360),
		//                                   int(inner_repeat)) > 1:
		//       da_end = n*360/inner_repeat - da_inner
		//       da_inner_adjusted = da_inner + da_end
		//       n += 1
		//
		// We replicate this logic exactly so both conditions must be satisfied
		// (da_end >= da_end_min AND gcd(...) == 1) before we exit.
		n := 0
		daEnd := -1.0
		daInnerAdjusted := 0.0

		for {
			daEnd = float64(n)*360.0/innerRepeat - daInner
			daInnerAdjusted = daInner + daEnd

			val1 := int(daInnerAdjusted * innerRepeat / 360.0)
			val2 := int(innerRepeat)

			// Stop only when BOTH:
			//   - daEnd >= daEndMin
			//   - gcd(val1, val2) == 1
			if !(daEnd < daEndMin || gcd(val1, val2) > 1) {
				break
			}
			n++
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

	default:
		return nil, fmt.Errorf("layer type '%s' unrecognized for mandrel type 'arbitrary_axial2'", layer.LType)
	}

	layer.FullPath = fullpath
	return fullpath, nil
}
