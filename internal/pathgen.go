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

	// Pre-allocate slices with known capacity for efficiency
	// make([]Type, length, capacity) - capacity is optional but helps performance
	fwpath := make([]Point, 0, nsteps)
	bwpath := make([]Point, 0, nsteps)

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

		// Calculate angle (360 degrees per step)
		a := float64(n) * 360.0

		// Create points and append to slices
		// append() is like Python's list.append(), but it returns a new slice
		// (Go slices can grow, but the underlying array might be reallocated)
		fwpath = append(fwpath, NewPoint(xfw, 0, zfw, a))
		bwpath = append(bwpath, NewPoint(xbw, 0, zbw, a))
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
	// Resolution: 3mm steps
	res := 3.0
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
		afw += math.Atan2(dx, zfw) * 180.0 / math.Pi
		abw += math.Atan2(dx, zbw) * 180.0 / math.Pi
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
		temp_path := make([]Point, len(fwpath))
		for i := 0; i < layer.Repeat; i++ {
			// Alternate between forward and backward
			if i%2 == 0 {
				copy(temp_path, fwpath)
			} else {
				copy(temp_path, bwpath)
			}
			// angle translation to match the last point of the fullpath
			if len(fullpath) > 0 && len(temp_path) > 0 {
				lastA := fullpath[len(fullpath)-1].A
				for i := range temp_path {
					temp_path[i].A += lastA
				}
				fmt.Println("fullpath =", temp_path)
			}
			fullpath = append(fullpath, temp_path...)
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
		innerRepeat = math.Ceil(innerRepeat/2.0) * 2.0 // Round up to nearest even number

		// Minimum da at the ends
		// TODO calculate this using the mandrel profile and fancy math
		daEndMin := 180.0 - 2.0*angle
		fmt.Println("daEndMin =", daEndMin)

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
		// In Python: da_point_fw = deepcopy(l.fwpath[-1])
		// Go doesn't have deepcopy, but structs are value types, so assignment copies.
		// IMPORTANT: Python appends to l.fwpath / l.bwpath, and the local "paths"
		// tuple still sees those updates. To match that behaviour we append to the
		// local fwpath/bwpath slices and then store them back onto the layer.
		daPointFW := fwpath[len(fwpath)-1]
		daPointBW := bwpath[len(bwpath)-1]

		daPointFW.A += daEnd
		daPointBW.A += daEnd

		fwpath = append(fwpath, daPointFW)
		bwpath = append(bwpath, daPointBW)
		layer.FWPath = fwpath
		layer.BWPath = bwpath

		// Build full path with inner repeats
		aCurr := 0.0
		paths := [][]Point{layer.FWPath, layer.BWPath}
		startIdx := 1 // backward
		if !layer.RevStart {
			startIdx = 0 // forward
		}

		for i := 0; i < int(innerRepeat)/2; i++ {
			pathIdx := (startIdx + i) % 2
			path := paths[pathIdx]

			// Add points with adjusted angle
			for _, p := range path {
				newPoint := p
				newPoint.A += aCurr
				fullpath = append(fullpath, newPoint)
			}

			if len(fullpath) > 0 {
				aCurr = fullpath[len(fullpath)-1].A
			}
		}

		// Add outer repeats
		fullpathBase := make([]Point, len(fullpath))
		copy(fullpathBase, fullpath) // Copy the base path

		for n := 0; n < layer.Repeat-1; n++ {
			baseAngle := fullpathBase[len(fullpathBase)-1].A * float64(n)
			for _, p := range fullpathBase {
				newPoint := p
				newPoint.A += baseAngle
				fullpath = append(fullpath, newPoint)
			}
		}

	default:
		return nil, fmt.Errorf("layer type '%s' unrecognized for mandrel type 'arbitrary_axial2'", layer.LType)
	}

	layer.FullPath = fullpath
	return fullpath, nil
}
