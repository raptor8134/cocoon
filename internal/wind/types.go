// Package wind contains the core data structures and types for gcode generation.
// This file defines the fundamental types: Point, PointRect, Layer, and Wind.
package wind

import (
	"fmt"
	"math"
)

// PointRect represents a point in rectangular (Cartesian) coordinates.
// In Go, structs are defined with the 'type' keyword followed by the name and 'struct'.
// Unlike Python dataclasses, Go structs are explicit about their fields.
type PointRect struct {
	X float64 // X coordinate in mm
	Y float64 // Y coordinate in mm
	Z float64 // Z coordinate in mm
}

// Add performs vector addition on two PointRect values.
// In Go, methods are defined with a receiver (the type they operate on).
// The receiver 'p' is like 'self' in Python, but it's explicit.
// Go doesn't have operator overloading, so we use methods instead of __add__.
func (p PointRect) Add(p2 PointRect) PointRect {
	return PointRect{
		X: p.X + p2.X,
		Y: p.Y + p2.Y,
		Z: p.Z + p2.Z,
	}
}

// Sub performs vector subtraction.
func (p PointRect) Sub(p2 PointRect) PointRect {
	return PointRect{
		X: p.X - p2.X,
		Y: p.Y - p2.Y,
		Z: p.Z - p2.Z,
	}
}

// String implements the Stringer interface, which is like __repr__ in Python.
// When you print a PointRect, Go will automatically call this method.
// The 'fmt' package uses this for formatting.
func (p PointRect) String() string {
	// fmt.Sprintf is like Python's f-string formatting
	return fmt.Sprintf("PointRect(X=%.2f  Y=%.2f  Z=%.2f)", p.X, p.Y, p.Z)
}

// Point represents a point in cylindrical coordinates with feedrate.
// This is the main data structure for gcode generation.
// In Go, float64 is the standard floating-point type (like float in Python).
type Point struct {
	X float64 // Axial position (mm)
	Y float64 // Y coordinate (mm) - used for helical angles
	Z float64 // Radial position (mm) - the radius
	A float64 // Angular position (degrees)
	F float64 // Feedrate multiplier (1.0 = 100%)
}

// NewPoint creates a new Point with default feedrate of 1.0.
// This is a constructor function - Go doesn't have default parameter values like Python,
// so we use functions to provide defaults.
func NewPoint(x, y, z, a float64) Point {
	return Point{
		X: x,
		Y: y,
		Z: z,
		A: a,
		F: 1.0, // Default feedrate
	}
}

// ToRect converts cylindrical coordinates to rectangular (Cartesian) coordinates.
// This is used for rendering and visualization.
// math.Sin and math.Cos work in radians, so we convert degrees to radians first.
func (p Point) ToRect() PointRect {
	// Keep trig arguments bounded for numerical stability.
	// The winding generator intentionally lets A grow without bound across
	// repeats, but sin/cos of huge angles loses precision and causes visible
	// rendering artifacts. Normalizing here preserves the correct wrapped
	// position on the mandrel surface.
	a := math.Mod(p.A, 360.0)
	if a < 0 {
		a += 360.0
	}
	// math.Pi / 180 converts degrees to radians
	rad := a * math.Pi / 180.0
	return PointRect{
		X: p.X,
		Y: p.Z * math.Sin(rad),
		Z: p.Z * math.Cos(rad),
	}
}

// Add performs vector addition on two Points.
// Note: Feedrate is reset to 1.0 after addition (matching Python behavior).
func (p Point) Add(p2 Point) Point {
	return Point{
		X: p.X + p2.X,
		Y: p.Y + p2.Y,
		Z: p.Z + p2.Z,
		A: p.A + p2.A,
		F: 1.0, // Reset feedrate
	}
}

// Sub performs vector subtraction.
func (p Point) Sub(p2 Point) Point {
	return Point{
		X: p.X - p2.X,
		Y: p.Y - p2.Y,
		Z: p.Z - p2.Z,
		A: p.A - p2.A,
		F: 1.0, // Reset feedrate
	}
}

// String implements the Stringer interface for Point.
func (p Point) String() string {
	return fmt.Sprintf("Point(X=%.2f  Y=%.2f  Z=%.2f  A=%.2f  F=%.2f)",
		p.X, p.Y, p.Z, p.A, p.F)
}

// LayerParams holds type-specific parameters for a layer.
// In Python, this was a SimpleNamespace (a dynamic object).
// In Go, we use a struct for type safety. We'll use a map[string]interface{} for flexibility
// when we need to handle different parameter types.
type LayerParams struct {
	// For "hoop" layers:
	Stepover float64 // Stepover distance in mm

	// For "helical" layers:
	Angle float64 // Helical angle in degrees

	// We could add more fields here as needed, or use a map for dynamic params
}

// Layer represents a single layer of winding.
// In Go, slices ([]Point) are like Python lists but with type safety.
// The zero value of a slice is nil (like None in Python), not an empty list.
type Layer struct {
	// User-configurable parameters
	LType  string      // Layer type: "hoop" or "helical"
	Repeat int         // Number of times to repeat this layer
	Params LayerParams // Type-specific parameters

	// Disabled is set when the layer contributes nothing (nrepeat == 0).
	// The layer is kept in the configuration with its parameters intact so it
	// can be switched back on; it is simply skipped during generation.
	Disabled bool

	// Internal tracking variables
	DAInner  float64 // Inner angle difference
	DAOuter  float64 // Outer angle difference
	AbsRot   bool    // Use absolute rotation (for future bolted COPV)
	RevStart bool    // Reverse start direction

	// Paths
	FWPath   []Point // Forward path (one-way -to+ points)
	BWPath   []Point // Backward path (one-way +to- points)
	FullPath []Point // Full path with inner repeat
}

// SpindleDirection selects which way the mandrel turns during a wind.
//
// This is a MACHINE property, not a wind property: whether "forward" winds the
// filament up over the top or down under the bottom depends on which side of
// the carriage the payout eye sits on, which varies between machines. Baking
// one handedness into the generator would silently produce mirrored parts on a
// machine built the other way round.
//
// It also interacts with the axis convention. Reversing the carriage axis
// direction flips the handedness of rotation about it, so a left-handed spin
// becomes a positive right-handed one without the mandrel physically turning
// differently. Keeping direction configurable means that choice can be made
// per machine rather than forced by the coordinate system.
type SpindleDirection string

const (
	// SpindleForward emits increasing spindle angles (the historical behaviour).
	SpindleForward SpindleDirection = "forward"
	// SpindleReverse negates them, for a machine whose eye is on the other side.
	SpindleReverse SpindleDirection = "reverse"
)

// AxisLetters maps Cocoon's four functional axes onto G-code words.
//
// The generator never writes letters directly; it emits functional quantities
// and these decide how they are spelled. That is what lets one wind target
// machines that disagree about lettering -- and filament winders genuinely do
// disagree, since there is no standard for them.
//
// Defaults follow the lathe convention (ISO 841): Z parallel to the spindle
// axis, X the radial cross-slide, and rotations A/B/C about X/Y/Z, so the
// spindle is C and the payout eye (rotating about the radial axis) is A.
//
// Note on direction: with the spindle turning forward as +C, +Z points
// OPPOSITE the direction the carriage advances. That falls out of the
// right-hand rule and is not something the letters can fix -- see
// docs/REQUIREMENTS.md.
type AxisLetters struct {
	Carriage string // along the mandrel axis; lathe Z
	Eye      string // payout eye rotation, about the radial axis; lathe A
	Radial   string // cross-feed / mandrel radius; lathe X
	Spindle  string // mandrel rotation, about the carriage axis; lathe C
}

// DefaultAxisLetters is the lathe convention.
func DefaultAxisLetters() AxisLetters {
	return AxisLetters{Carriage: "Z", Eye: "A", Radial: "X", Spindle: "C"}
}

// withDefaults fills any unset letter from the lathe convention, so a config
// can alias just one axis without having to restate the rest.
func (a AxisLetters) withDefaults() AxisLetters {
	d := DefaultAxisLetters()
	if a.Carriage == "" {
		a.Carriage = d.Carriage
	}
	if a.Eye == "" {
		a.Eye = d.Eye
	}
	if a.Radial == "" {
		a.Radial = d.Radial
	}
	if a.Spindle == "" {
		a.Spindle = d.Spindle
	}
	return a
}

// Machine holds properties of the winder itself rather than of the part.
// This is the seed of the machine profile described in docs/REQUIREMENTS.md;
// axis letters and G-code flavor will join it.
type Machine struct {
	// SpindleDirection is "forward" (default) or "reverse".
	SpindleDirection SpindleDirection

	// Axes maps functional axes onto G-code letters.
	Axes AxisLetters
}

// Sign returns the multiplier to apply to emitted spindle angles.
func (m Machine) Sign() float64 {
	if m.SpindleDirection == SpindleReverse {
		return -1
	}
	return 1
}

// Wind represents a complete wind configuration.
// This holds the mandrel, filament info, and all layers.
type Wind struct {
	Machine    Machine  // Machine properties (spindle direction, later: axes/flavor)
	Mandrel    *Mandrel // Pointer to Mandrel (like a reference in Python)
	Filament   Filament // Filament properties
	Layers     []Layer  // All layers in this wind
	StartGcode []string // G-code header commands
	EndGcode   []string // G-code footer commands
	BodyGcode  []string // Generated body G-code
}

// Filament holds filament properties.
// In Python this was a SimpleNamespace, here it's a proper struct.
type Filament struct {
	Width     float64 // Filament width in mm
	Thickness float64 // Filament thickness in mm
	Feedrate  float64 // Feedrate in mm/s or deg/s
}
