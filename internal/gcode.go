// Package internal - G-code generation functions.
// This file converts point paths into G-code commands.
package internal

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
func Layers2Gcode(layers []Layer) []string {
	// First: Concatenate all layer paths into one long toolpath
	// Reset angle to zero between each one
	pathFinal := make([]interface{}, 0) // Use interface{} to hold both Points and strings
	aStart := 0.0

	for i := range layers {
		layer := &layers[i] // Get pointer to avoid copying

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

	// Second: Intersperse progress update markers
	// Calculate total time
	totalTime := 0.0
	for _, item := range pathFinal {
		if p, ok := item.(Point); ok {
			if p.F != 0 {
				totalTime += p.A / p.F
			}
		}
	}

	// Insert progress markers
	timeCounter := 0.0
	progRes := 20 // Number of lines to update progress

	// We need to build a new slice with progress markers inserted
	// Go slices don't have insert, so we build a new one
	result := make([]interface{}, 0, len(pathFinal)+len(pathFinal)/progRes)
	insertCount := 0

	for i, item := range pathFinal {
		if p, ok := item.(Point); ok {
			if p.F != 0 {
				timeCounter += p.A / p.F
			}

			// Insert progress marker every progRes points
			if i%progRes == 0 {
				percent := (timeCounter / totalTime) * 100.0
				if totalTime > 0 {
					progressMsg := fmt.Sprintf("M117 %.0f%% complete", percent)
					result = append(result, progressMsg)
					insertCount++
				}
			}
		}
		result = append(result, item)
	}

	// Third: Convert all Points to G-code strings
	gcodeList := make([]string, 0, len(result))

	for _, item := range result {
		switch v := item.(type) {
		case Point:
			p := v
			var g string
			if p.A == 0 {
				g = "G0" // Rapid move
			} else {
				g = "G1" // Linear move
			}

			// Format G-code command
			// In Python: f"{G} X{m.X} Y{m.Y} Z{m.Z} A{m.A} F{m.F*1000}"
			// In Go: fmt.Sprintf does the same thing
			gcodeList = append(gcodeList, fmt.Sprintf("%s X%.3f Y%.3f Z%.3f A%.3f F%.0f", g, p.X, p.Y, p.Z, p.A, p.F*1000))

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
