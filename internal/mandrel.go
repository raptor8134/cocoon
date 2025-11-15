// Package internal - Mandrel and interpolation functions.
// This file handles mandrel profile data and linear interpolation.
package internal

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Mandrel represents a mandrel profile for winding.
// The mandrel is defined by a series of (X, Z) points that form a profile.
// X is the axial position, Z is the radius at that position.
type Mandrel struct {
	MType   string    // Mandrel type identifier
	XPoints []float64 // X coordinates (axial positions)
	ZPoints []float64 // Z coordinates (radii at each X position)
	XMax    float64   // Maximum X value
	XMin    float64   // Minimum X value
	ZMax    float64   // Maximum Z value
	ZMin    float64   // Minimum Z value
	Length  float64   // Total length (XMax - XMin)
}

// NewMandrelFromPoints creates a Mandrel from a slice of (x, z) coordinate pairs.
// In Go, we use functions like this as constructors since we can't have default parameters.
// The input is a slice of slices: [][]float64 where each inner slice is [x, z].
func NewMandrelFromPoints(points [][]float64) (*Mandrel, error) {
	// Validate input
	if len(points) == 0 {
		return nil, fmt.Errorf("mandrel must have at least one point")
	}

	// Extract X and Z coordinates
	// In Go, we need to pre-allocate slices with make() when we know the size.
	xPoints := make([]float64, len(points))
	zPoints := make([]float64, len(points))

	for i, p := range points {
		if len(p) != 2 {
			return nil, fmt.Errorf("point %d must have exactly 2 coordinates (x, z), got %d", i, len(p))
		}
		xPoints[i] = p[0]
		zPoints[i] = p[1]
	}

	// Sort points by X coordinate to ensure proper interpolation
	// We need to keep X and Z points aligned, so we create a slice of indices
	indices := make([]int, len(points))
	for i := range indices {
		indices[i] = i
	}

	// Sort indices based on X values
	sort.Slice(indices, func(i, j int) bool {
		return xPoints[indices[i]] < xPoints[indices[j]]
	})

	// Reorder both slices based on sorted indices
	sortedX := make([]float64, len(points))
	sortedZ := make([]float64, len(points))
	for i, idx := range indices {
		sortedX[i] = xPoints[idx]
		sortedZ[i] = zPoints[idx]
	}

	// Calculate bounds
	xMax, xMin := sortedX[0], sortedX[0]
	zMax, zMin := sortedZ[0], sortedZ[0]

	// Go's range gives index and value, but we only need the value here
	for _, x := range sortedX {
		if x > xMax {
			xMax = x
		}
		if x < xMin {
			xMin = x
		}
	}

	for _, z := range sortedZ {
		if z > zMax {
			zMax = z
		}
		if z < zMin {
			zMin = z
		}
	}

	return &Mandrel{
		MType:   "AA2",
		XPoints: sortedX,
		ZPoints: sortedZ,
		XMax:    xMax,
		XMin:    xMin,
		ZMax:    zMax,
		ZMin:    zMin,
		Length:  xMax - xMin,
	}, nil
}

// NewMandrelFromCSV creates a Mandrel from a CSV file.
// This replaces the Python version that used os.path.join and csv.reader.
func NewMandrelFromCSV(filename string) (*Mandrel, error) {
	// filepath.Join is like os.path.join in Python - it handles path separators correctly
	path := filepath.Join("profiles", filename)

	// Open the file
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open mandrel file %s: %w", path, err)
	}
	defer file.Close() // defer ensures the file is closed when the function returns

	// Create CSV reader
	reader := csv.NewReader(file)

	// Read all rows
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV: %w", err)
	}

	// Parse rows into float64 pairs
	points := make([][]float64, 0, len(rows)) // Pre-allocate with capacity
	for i, row := range rows {
		if len(row) < 2 {
			return nil, fmt.Errorf("row %d must have at least 2 columns", i)
		}

		var x, z float64
		// fmt.Sscanf is like Python's unpacking, but we need to handle errors explicitly
		_, err := fmt.Sscanf(row[0], "%f", &x)
		if err != nil {
			return nil, fmt.Errorf("row %d, column 0: invalid number: %w", i, err)
		}

		_, err = fmt.Sscanf(row[1], "%f", &z)
		if err != nil {
			return nil, fmt.Errorf("row %d, column 1: invalid number: %w", i, err)
		}

		points = append(points, []float64{x, z})
	}

	return NewMandrelFromPoints(points)
}

// Interp performs linear interpolation to find the Z (radius) value at a given X position.
// This replaces numpy's np.interp() function.
// The algorithm:
// 1. Clamp X to the valid range [XMin, XMax]
// 2. Find the two points that bracket X
// 3. Linearly interpolate between them
func (m *Mandrel) Interp(x float64) float64 {
	// Clamp x to valid range (replaces np.clip)
	if x < m.XMin {
		x = m.XMin
	}
	if x > m.XMax {
		x = m.XMax
	}

	// Find the two points that bracket x
	// We'll use binary search for efficiency, but linear search is simpler
	// For small mandrels, linear search is fine
	n := len(m.XPoints)

	// Handle edge cases
	if x <= m.XPoints[0] {
		return m.ZPoints[0]
	}
	if x >= m.XPoints[n-1] {
		return m.ZPoints[n-1]
	}

	// Find the index where XPoints[i] <= x < XPoints[i+1]
	// We could use sort.Search, but for clarity we'll do linear search
	var i int
	for i = 0; i < n-1; i++ {
		if m.XPoints[i] <= x && x < m.XPoints[i+1] {
			break
		}
	}

	// Linear interpolation: z = z0 + (z1 - z0) * (x - x0) / (x1 - x0)
	x0, x1 := m.XPoints[i], m.XPoints[i+1]
	z0, z1 := m.ZPoints[i], m.ZPoints[i+1]

	// Avoid division by zero (shouldn't happen if points are sorted, but be safe)
	if x1 == x0 {
		return z0
	}

	z := z0 + (z1-z0)*(x-x0)/(x1-x0)
	return z
}

// MaxZ returns the maximum Z (radius) value in the mandrel.
// This is a helper method used in helical calculations.
func (m *Mandrel) MaxZ() float64 {
	return m.ZMax
}

