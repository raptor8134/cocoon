// Package wind - G-code generation functions.
// This file converts point paths into G-code commands.
package wind

import (
	"fmt"
	"math"
	"strings"
)

// TotalPoints sums the generated geometry across every enabled layer.
func TotalPoints(layers []Layer) int {
	n := 0
	for i := range layers {
		if !layers[i].Disabled {
			n += len(layers[i].FullPath)
		}
	}
	return n
}

// CheckTotalPoints reports whether a wind fits in the aggregate budget.
// Callers should run it after generating all layers and before rendering or
// emitting, since exceeding it is a memory failure rather than a bad value.
func CheckTotalPoints(layers []Layer) error {
	if n := TotalPoints(layers); n > MaxTotalPoints {
		return fmt.Errorf(
			"this wind generates %d points across %d layers (limit %d); reduce layer count, increase filament width, or coarsen the path resolution",
			n, len(layers), MaxTotalPoints)
	}
	return nil
}

// Layers2Gcode converts a slice of layers into G-code commands.
// This is the main function that takes calculated paths and outputs G-code strings.
// Parameters:
//   - layers: Slice of Layer objects with FullPath already calculated
//   - mode: Optional mode string (e.g., "DEMO" for demo mode)
//
// Returns a slice of G-code command strings.
// WindPath concatenates every enabled layer into one continuous toolpath,
// offsetting each layer so it begins where the previous one ended.
//
// Shared by G-code emission and metrics so the two can never disagree about
// what the machine will actually do.
func WindPath(layers []Layer) []Point {
	var path []Point
	aStart := 0.0

	for i := range layers {
		layer := &layers[i]

		// A disabled layer (nrepeat == 0) emits nothing and does not advance
		// the running angle, so toggling one off leaves every other layer's
		// output byte-identical.
		if layer.Disabled || len(layer.FullPath) == 0 {
			continue
		}

		// If we need to set absolute rotation, do it
		if layer.AbsRot && math.Mod(aStart, 360.0) != 0 {
			daToZero := 360.0 - math.Mod(aStart, 360.0)
			daPoint := layer.FullPath[0]
			daPoint.A += daToZero
			path = append(path, daPoint)
			aStart += daToZero
		}

		for _, pOrig := range layer.FullPath {
			p := pOrig
			p.A += aStart
			path = append(path, p)
		}
		aStart += layer.DAOuter
	}
	return path
}

// ComputeMetrics estimates run time and material use for a wind.
//
// It walks the same path the emitter does, so the reported time is the sum of
// the durations actually commanded rather than an independent guess.
func ComputeMetrics(layers []Layer, machine Machine) WindMetrics {
	speed := machine.SurfaceSpeed
	if speed <= 0 {
		speed = 100
	}
	path := WindPath(layers)

	m := WindMetrics{MinSeconds: math.Inf(1)}
	for i := 1; i < len(path); i++ {
		mm := MoveBetween(path[i-1], path[i], speed)
		if mm.Seconds <= 0 {
			continue
		}
		m.TowLength += mm.SurfaceLength
		m.Seconds += mm.Seconds
		m.Moves++
		if mm.Seconds < m.MinSeconds {
			m.MinSeconds = mm.Seconds
		}
	}
	if math.IsInf(m.MinSeconds, 1) {
		m.MinSeconds = 0
	}
	return m
}

func Layers2Gcode(layers []Layer, machine Machine) []string {
	// First: Concatenate all layer paths into one long toolpath
	pathFinal := make([]interface{}, 0)
	for _, p := range WindPath(layers) {
		pathFinal = append(pathFinal, p)
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

	// Every move is a feed move; nothing here emits G0.
	//
	// The operator prepares a wind by hand -- a few turns at the Z=0 end to get
	// the tow snug -- so the machine is already at the start with fiber under
	// tension when the program begins. A rapid would either slew away from that
	// carefully set start or drag the tow at maximum axis rate.
	//
	// (This previously keyed off "p.A == 0", which emitted a rapid wherever the
	// angle happened to be zero -- in practice at every layer boundary, since
	// DAOuter was never set. That is fixed, but the rule itself was wrong too.)
	ax := machine.Axes.withDefaults()

	// Surface speed drives everything: the F word is derived from how long each
	// move should take at that speed, not the other way round.
	speed := machine.SurfaceSpeed
	if speed <= 0 {
		speed = 100 // mm/s fallback; see the Filament.Feedrate note below
	}
	inverseTime := machine.FeedMode != FeedUnitsPerMinute

	// G93/G94 is modal, so it is declared once at the top of the program.
	if inverseTime {
		gcodeList = append(gcodeList, "G93 ; inverse time feed: F = 1/minutes per move")
	} else {
		gcodeList = append(gcodeList, "G94 ; units per minute feed")
	}

	var prev *Point

	for _, item := range result {
		switch v := item.(type) {
		case Point:
			p := v

			// The first point is a position, not a move: there is nothing to
			// travel from, so it carries no meaningful duration. Emitting it
			// without an F word under G93 would be rejected, so it is skipped
			// entirely -- the operator has already set the head there by hand.
			if prev == nil {
				pc := p
				prev = &pc
				continue
			}

			mm := MoveBetween(*prev, p, speed)
			pc := p
			prev = &pc

			// A zero-length move has no duration and no meaning; dropping it
			// avoids a divide-by-zero in the inverse-time F word.
			if mm.Seconds <= 0 {
				continue
			}

			const g = "G1" // Linear (feed) move; see the note above

			var f float64
			if inverseTime {
				// F = 1 / minutes.
				f = 60.0 / mm.Seconds
			} else {
				// G94 on a controller that computes feed from the linear axes
				// when any are moving: give it the linear distance over the
				// intended duration so it lands on the same time.
				f = mm.SurfaceLength / (mm.Seconds / 60.0)
			}

			// Word order is the same semantic order the generator has always
			// used (carriage, eye, radial, spindle) so diffs stay readable;
			// G-code does not care about word order within a line.
			gcodeList = append(gcodeList, fmt.Sprintf("%s %s%.3f %s%.3f %s%.3f %s%.3f F%.2f",
				g,
				ax.Carriage, p.X,
				ax.Eye, p.Y,
				ax.Radial, p.Z,
				ax.Spindle, p.A*machine.Sign(),
				f))

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
	var builder strings.Builder
	builder.Grow(len(gcode) * 50) // Pre-allocate space (estimate 50 chars per line)

	for _, line := range gcode {
		builder.WriteString(line)
		// Motion lines get a trailing ";" (an empty comment) as they always
		// have. Comment and blank lines must NOT: the header embeds the source
		// configuration verbatim, and appending a character to each line would
		// corrupt what it is supposed to reproduce.
		if !strings.HasPrefix(strings.TrimSpace(line), ";") && strings.TrimSpace(line) != "" {
			builder.WriteString(";")
		}
		builder.WriteString("\n")
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
