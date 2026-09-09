// Package wind - G-code generation functions.
// This file converts point paths into G-code commands.
package wind

import (
	"fmt"
	"math"
	"strings"
)

// Layers2Gcode converts a slice of layers into G-code commands.
// This is the main function that takes calculated paths and outputs G-code strings.
// Parameters:
//   - layers: Slice of Layer objects with FullPath already calculated
//   - mode: Optional mode string (e.g., "DEMO" for demo mode)
//
// Returns a slice of G-code command strings.
func Layers2Gcode(layers []Layer, machine Machine) []string {
	// First: Concatenate all layer paths into one long toolpath
	// Reset angle to zero between each one
	pathFinal := make([]interface{}, 0) // Use interface{} to hold both Points and strings
	aStart := 0.0

	for i := range layers {
		layer := &layers[i] // Get pointer to avoid copying

		// A disabled layer (nrepeat == 0) emits nothing and does not advance
		// the running angle, so toggling one off leaves every other layer's
		// output byte-identical.
		if layer.Disabled || len(layer.FullPath) == 0 {
			continue
		}

		// If we need to set absolute rotation, do it
		if layer.AbsRot && math.Mod(aStart, 360.0) != 0 {
			daToZero := 360.0 - math.Mod(aStart, 360.0)
			// In Python: da_point = deepcopy(layer.fullpath[0])
			// In Go: struct assignment copies the value
			if len(layer.FullPath) > 0 {
				daPoint := layer.FullPath[0]
				daPoint.A += daToZero
				pathFinal = append(pathFinal, daPoint)
				aStart += daToZero
			}
		}

		// Add to final path by factoring in aStart
		// Make copies of points to keep layer.FullPath unchanged
		for _, pOrig := range layer.FullPath {
			p := pOrig // Copy the point (structs are value types in Go)
			p.A += aStart
			pathFinal = append(pathFinal, p)
		}
		aStart += layer.DAOuter
	}

	// Second: Intersperse progress update markers.
	//
	// TODO(feedrate): the percentage below is positional, not temporal -- it
	// reports "fraction of points emitted", which only equals "fraction of
	// time elapsed" if every move takes the same time. It does not: moves
	// differ in length, and the mandrel radius (and so surface speed at a
	// given angular rate) varies along the profile.
	//
	// The previous attempt summed p.A / p.F as a "time", which is not a time
	// in any unit: A is an accumulated angle in degrees and F is a unitless
	// multiplier that is currently always 1.0. Deliberately removed rather
	// than left to look meaningful.
	//
	// Doing this properly needs the feedrate work, which is also deferred:
	// Filament.Feedrate is parsed and then ignored, so every emitted line
	// carries F1000 regardless of configuration.

	// Number of progress updates: at most 100, and at most one per 10 points,
	// so short programs are not swamped with M117 lines and long ones do not
	// accumulate thousands of them.
	nPoints := len(pathFinal)
	updates := nPoints / 10
	if updates > 100 {
		updates = 100
	}
	progRes := 0 // emit markers every progRes points; 0 disables them
	if updates > 0 {
		progRes = nPoints / updates
	}

	extra := 0
	if progRes > 0 {
		extra = nPoints / progRes
	}
	result := make([]interface{}, 0, nPoints+extra)

	for i, item := range pathFinal {
		// i > 0 skips the marker at the very start: "0% complete" says
		// nothing, and emitting it would produce updates+1 markers rather
		// than the requested count.
		if _, ok := item.(Point); ok && progRes > 0 && i > 0 && i%progRes == 0 {
			percent := (float64(i) / float64(nPoints)) * 100.0
			result = append(result, fmt.Sprintf("M117 %.0f%% complete", percent))
		}
		result = append(result, item)
	}

	// Third: Convert all Points to G-code strings
	gcodeList := make([]string, 0, len(result))

	// Only the very first move is a rapid: it positions the head at the start
	// of the wind with no fiber being laid. Everything after is cutting fiber
	// and must be a coordinated feed move.
	//
	// This used to key off "p.A == 0", which emitted a G0 wherever the angle
	// happened to be zero -- in practice at every layer boundary, because
	// DAOuter was never set so each layer restarted at A=0. A rapid there
	// would slew all axes at maximum rate mid-wind.
	firstMove := true
	ax := machine.Axes.withDefaults()

	for _, item := range result {
		switch v := item.(type) {
		case Point:
			p := v
			g := "G1" // Linear (feed) move
			if firstMove {
				g = "G0" // Rapid to the starting position
				firstMove = false
			}

			// Format G-code command
			// In Python: f"{G} X{m.X} Y{m.Y} Z{m.Z} A{m.A} F{m.F*1000}"
			// In Go: fmt.Sprintf does the same thing
			//
			// TODO(feedrate): p.F is hardcoded to 1.0 everywhere, so this
			// always emits F1000 and Filament.Feedrate (and Thickness with
			// it) is ignored. See the progress-marker note above.
			// The spindle sign is applied here, at emit time, rather than
			// during path generation: the geometry is identical either way,
			// only the commanded direction differs.
			// Word order is the same semantic order the generator has always
			// used (carriage, eye, radial, spindle) so diffs stay readable;
			// G-code does not care about word order within a line.
			gcodeList = append(gcodeList, fmt.Sprintf("%s %s%.3f %s%.3f %s%.3f %s%.3f F%.0f",
				g,
				ax.Carriage, p.X,
				ax.Eye, p.Y,
				ax.Radial, p.Z,
				ax.Spindle, p.A*machine.Sign(),
				p.F*1000))

		case string:
			// Progress marker or other string
			gcodeList = append(gcodeList, v)

		default:
			// This shouldn't happen, but handle it gracefully
			// Log error and skip this item
			// In production, you might want to return an error, but for now we'll skip
			continue
		}
	}

	return gcodeList
}

// SpaceConcat concatenates slices of strings and adds a blank line between each sublist.
// This is useful for separating different blocks of G-code.
// In Python: return sum([x + [""] for x in code_list], [])[:-1]
// Go version is more explicit.
func SpaceConcat(codeList [][]string) []string {
	result := make([]string, 0)

	for i, sublist := range codeList {
		result = append(result, sublist...)
		// Add blank line between sublists (but not after the last one)
		if i < len(codeList)-1 {
			result = append(result, "")
		}
	}

	return result
}

// FormatGcodeLines formats G-code commands by adding semicolons and newlines.
// This is the final step before writing to file.
// Parameters:
//   - gcode: Slice of G-code command strings
//
// Returns a single string with all G-code formatted.
func FormatGcodeLines(gcode []string) string {
	// In Python: [x+";\n" for x in gcode]
	// In Go: use strings.Builder for efficient string concatenation
	var builder strings.Builder
	builder.Grow(len(gcode) * 50) // Pre-allocate space (estimate 50 chars per line)

	for _, line := range gcode {
		builder.WriteString(line)
		builder.WriteString(";\n")
	}

	return builder.String()
}

// DictList2Layers converts a slice of layer dictionaries into Layer structs.
// This function parses JSON-like data structures into typed Go structs.
// In Python, this used SimpleNamespace for dynamic attributes.
// In Go, we use a map[string]interface{} for flexibility, then extract known fields.
func DictList2Layers(dlayers []map[string]interface{}) ([]Layer, error) {
	layers := make([]Layer, 0, len(dlayers))
	revStart := false

	for _, dlayer := range dlayers {
		// Extract type and nrepeat (these are at the top level)
		ltype, ok := dlayer["type"].(string)
		if !ok {
			return nil, fmt.Errorf("layer missing 'type' field or type is not string")
		}

		// Handle nrepeat - could be int or float64 (JSON numbers)
		var repeat int
		switch v := dlayer["nrepeat"].(type) {
		case int:
			repeat = v
		case float64:
			repeat = int(v) // JSON numbers are float64
		default:
			return nil, fmt.Errorf("layer 'nrepeat' must be a number")
		}

		// Create params struct
		params := LayerParams{}

		// Extract stepover for hoop layers
		if stepover, ok := dlayer["stepover"].(float64); ok {
			params.Stepover = stepover
		}

		// Extract angle for helical layers
		if angle, ok := dlayer["angle"].(float64); ok {
			params.Angle = angle
		}

		// Create layer
		layer := Layer{
			LType:    ltype,
			Repeat:   repeat,
			Params:   params,
			RevStart: revStart,
		}

		// Update revStart for next layer (if hoop and odd repeat)
		if layer.LType == "hoop" && layer.Repeat%2 == 1 {
			revStart = !revStart
		}

		layers = append(layers, layer)
	}

	return layers, nil
}
