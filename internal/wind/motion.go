// Package wind - motion timing and feedrate.
//
// The generator works in physical terms -- how fast the tow should be laid on
// the mandrel surface -- and converts to a feedrate only when emitting. That
// separation exists because there is no portable way to express a combined
// linear+rotary speed: under normal feed mode (G94) a controller must reconcile
// millimetres with degrees, and each one picks a different arbitrary rule.
//
// G93 inverse-time sidesteps it. In G93 the F word means "this move takes 1/F
// minutes", so the controller is told a duration and never has to combine
// units. That also makes varying the feedrate for slip control trivial: it is
// just a different duration per move.
package wind

import (
	"fmt"
	"math"
)

// MoveMetrics describes one move in physical terms, independent of any
// controller's notion of feedrate.
type MoveMetrics struct {
	// SurfaceLength is how far the tow travels across the mandrel surface, mm.
	// This is the quantity that should move at the configured speed: it is the
	// rate at which fiber is actually laid.
	SurfaceLength float64

	// Seconds is how long the move should take at the configured surface speed.
	Seconds float64
}

// MoveBetween computes the metrics for the move from p0 to p1.
//
// Surface travel combines the axial component with the circumferential arc at
// the local radius. The payout eye's rotation is deliberately excluded: it
// orients the tow rather than laying it, and including it would let a large eye
// swing slow the whole move down.
func MoveBetween(p0, p1 Point, surfaceSpeed float64) MoveMetrics {
	dx := p1.X - p0.X
	rMid := (p0.Z + p1.Z) / 2
	dArc := (p1.A - p0.A) * math.Pi / 180 * rMid
	dr := p1.Z - p0.Z

	// Radial motion is real travel of the delivery head, so it counts.
	ds := math.Sqrt(dx*dx + dArc*dArc + dr*dr)

	m := MoveMetrics{SurfaceLength: ds}
	if surfaceSpeed > 0 {
		m.Seconds = ds / surfaceSpeed
	}
	return m
}

// WindMetrics aggregates timing and material use over a whole program.
type WindMetrics struct {
	// TowLength is total fiber laid, mm.
	TowLength float64

	// Seconds is the total estimated run time.
	Seconds float64

	// Moves is the number of motion commands.
	Moves int

	// MinSeconds is the shortest single move, useful for spotting moves so
	// brief that acceleration will dominate and the commanded time will not
	// be met.
	MinSeconds float64
}

// TowVolume returns the volume of fiber laid, mm^3.
func (m WindMetrics) TowVolume(f Filament) float64 {
	return m.TowLength * f.Width * f.Thickness
}

// TowMass returns the mass of fiber laid in grams, given a density in g/cm^3.
// E-glass is about 2.55; carbon around 1.75. Returns 0 when density is unset.
func (m WindMetrics) TowMass(f Filament, densityGPerCm3 float64) float64 {
	if densityGPerCm3 <= 0 {
		return 0
	}
	return m.TowVolume(f) / 1000.0 * densityGPerCm3 // mm^3 -> cm^3 -> g
}

// FormatDuration renders a run time the way a slicer does.
func FormatDuration(seconds float64) string {
	if seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return "unknown"
	}
	total := int(math.Round(seconds))
	h, m, s := total/3600, (total%3600)/60, total%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
