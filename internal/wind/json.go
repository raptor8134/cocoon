// Package wind - JSON parsing functions.
// This file handles parsing JSON wind configuration files into Mandrel and Wind objects.
package wind

import (
	"fmt"
	"math"
	"os"
	"strings"

	json "github.com/KevinWang15/go-json5"
)

// WindJSON represents the structure of a JSON wind configuration file.
// Cocoon is now the reference implementation for this format; it is no
// longer required to match the original Python gcode_gen project.
type WindJSON struct {
	Comment  string                   `json:"comment"`
	Filament map[string]interface{}   `json:"filament"`
	Mandrel  map[string]interface{}   `json:"mandrel"`
	Layers   []map[string]interface{} `json:"layers"`
}

// ParseWindFromJSONFile parses a JSON file and creates a Wind object.
// This function handles both "cylindrical" and "arbitrary_axial" mandrel types.
func ParseWindFromJSONFile(filename string) (*Wind, error) {
	// Read the JSON file
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read JSON file: %w", err)
	}

	return ParseWindFromJSONBytes(data)
}

// ParseWindFromJSONBytes parses JSON/JSON5 bytes and creates a Wind object.
func ParseWindFromJSONBytes(data []byte) (*Wind, error) {
	// Parse JSON into map structure
	var windJSON map[string]interface{}
	if err := json.Unmarshal(data, &windJSON); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Extract filament
	filament, err := parseFilament(windJSON["filament"])
	if err != nil {
		return nil, fmt.Errorf("failed to parse filament: %w", err)
	}

	// Extract mandrel
	mandrel, err := parseMandrel(windJSON["mandrel"])
	if err != nil {
		return nil, fmt.Errorf("failed to parse mandrel: %w", err)
	}

	// Extract layers
	layersData, ok := windJSON["layers"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("layers must be an array")
	}

	layers := make([]map[string]interface{}, 0, len(layersData))
	for i, layerData := range layersData {
		layerMap, ok := layerData.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("layer %d must be an object", i)
		}
		layers = append(layers, layerMap)
	}

	parsedLayers, err := DictList2Layers(layers)
	if err != nil {
		return nil, fmt.Errorf("failed to parse layers: %w", err)
	}

	// Machine settings are optional; absent means the historical defaults.
	machine := Machine{SpindleDirection: SpindleForward}
	if mj, ok := windJSON["machine"].(map[string]interface{}); ok {
		if d, ok := mj["spindle_direction"].(string); ok {
			switch SpindleDirection(d) {
			case SpindleForward, SpindleReverse:
				machine.SpindleDirection = SpindleDirection(d)
			default:
				return nil, fmt.Errorf("machine spindle_direction must be %q or %q, got %q",
					SpindleForward, SpindleReverse, d)
			}
		}
	}

	if aj, ok := windJSON["machine"].(map[string]interface{}); ok {
		if axes, ok := aj["axes"].(map[string]interface{}); ok {
			get := func(k string) (string, error) {
				v, present := axes[k]
				if !present {
					return "", nil
				}
				str, ok := v.(string)
				if !ok {
					return "", fmt.Errorf("machine axes %q must be a string", k)
				}
				// A G-code word is a single letter; anything else would
				// produce output no controller can parse.
				if len(str) != 1 || !((str[0] >= 'A' && str[0] <= 'Z') || (str[0] >= 'a' && str[0] <= 'z')) {
					return "", fmt.Errorf("machine axes %q must be a single letter, got %q", k, str)
				}
				return strings.ToUpper(str), nil
			}
			var err error
			if machine.Axes.Carriage, err = get("carriage"); err != nil {
				return nil, err
			}
			if machine.Axes.Eye, err = get("eye"); err != nil {
				return nil, err
			}
			if machine.Axes.Radial, err = get("radial"); err != nil {
				return nil, err
			}
			if machine.Axes.Spindle, err = get("spindle"); err != nil {
				return nil, err
			}
		}
	}

	if mj, ok := windJSON["machine"].(map[string]interface{}); ok {
		if fm, ok := mj["feed_mode"].(string); ok {
			switch FeedMode(fm) {
			case FeedInverseTime, FeedUnitsPerMinute:
				machine.FeedMode = FeedMode(fm)
			default:
				return nil, fmt.Errorf("machine feed_mode must be %q or %q, got %q",
					FeedInverseTime, FeedUnitsPerMinute, fm)
			}
		}
		if ss, ok := mj["surface_speed"].(float64); ok {
			if ss <= 0 || math.IsNaN(ss) {
				return nil, fmt.Errorf("machine surface_speed must be greater than 0, got %g", ss)
			}
			machine.SurfaceSpeed = ss
		}
	}
	// Filament.Feedrate is the historical home for this; use it when the
	// machine block does not override.
	if machine.SurfaceSpeed == 0 {
		machine.SurfaceSpeed = filament.Feedrate
	}

	// Create Wind object
	wind := &Wind{
		Machine:  machine,
		Mandrel:  mandrel,
		Filament: filament,
		Layers:   parsedLayers,
	}

	return wind, nil
}

// parseFilament extracts filament properties from JSON data.
// Handles both preset references and inline definitions.
func parseFilament(filamentData interface{}) (Filament, error) {
	filamentMap, ok := filamentData.(map[string]interface{})
	if !ok {
		return Filament{}, fmt.Errorf("filament must be an object")
	}

	filament := Filament{}

	// Check for preset (we'll use default values for now, config.json parsing can be added later)
	if preset, ok := filamentMap["preset"].(string); ok {
		// For now, use default values based on common presets
		// TODO: Load from config.json if needed
		switch preset {
		case "amazon_fiberglass":
			filament.Width = 20.0
			filament.Thickness = 0.25
			filament.Feedrate = 100.0
		default:
			// Default values
			filament.Width = 20.0
			filament.Thickness = 0.25
			filament.Feedrate = 100.0
		}
	}

	// Override with explicit values if provided. A value that is present but
	// non-positive is a mistake, not a request for the default: silently
	// substituting one (as this used to) hides the error and generates a wind
	// the user did not ask for.
	for _, f := range []struct {
		key    string
		target *float64
	}{
		{"width", &filament.Width},
		{"thickness", &filament.Thickness},
		{"feedrate", &filament.Feedrate},
	} {
		raw, present := filamentMap[f.key]
		if !present {
			continue
		}
		v, ok := raw.(float64)
		if !ok {
			return Filament{}, fmt.Errorf("filament %q must be a number", f.key)
		}
		if math.IsNaN(v) || v <= 0 {
			return Filament{}, fmt.Errorf("filament %q must be greater than 0, got %g", f.key, v)
		}
		*f.target = v
	}

	// Fill in anything still unset (no preset and no explicit value).
	if filament.Width == 0 {
		filament.Width = 20.0 // Default
	}
	if filament.Thickness == 0 {
		filament.Thickness = 0.25 // Default
	}
	if filament.Feedrate == 0 {
		filament.Feedrate = 100.0 // Default
	}

	return filament, nil
}

// parseMandrel extracts mandrel configuration from JSON data.
// Handles both "cylindrical" and "arbitrary_axial" types.
func parseMandrel(mandrelData interface{}) (*Mandrel, error) {
	mandrelMap, ok := mandrelData.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("mandrel must be an object")
	}

	mandrelType, ok := mandrelMap["type"].(string)
	if !ok {
		return nil, fmt.Errorf("mandrel must have a 'type' field")
	}

	switch mandrelType {
	case "cylindrical":
		return parseCylindricalMandrel(mandrelMap)
	case "arbitrary_axial":
		return parseArbitraryAxialMandrel(mandrelMap)
	default:
		return nil, fmt.Errorf("unsupported mandrel type: %s", mandrelType)
	}
}

// parseCylindricalMandrel creates a Mandrel from cylindrical dimensions.
func parseCylindricalMandrel(mandrelMap map[string]interface{}) (*Mandrel, error) {
	dimensions, ok := mandrelMap["dimensions"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("cylindrical mandrel must have 'dimensions' object")
	}

	var length, diameter float64

	if l, ok := dimensions["length"].(float64); ok {
		length = l
	} else {
		return nil, fmt.Errorf("dimensions must have 'length' as a number")
	}

	if d, ok := dimensions["diameter"].(float64); ok {
		diameter = d
	} else {
		return nil, fmt.Errorf("dimensions must have 'diameter' as a number")
	}

	radius := diameter / 2.0

	// Create points for cylindrical mandrel: [(0, radius), (length, radius)]
	points := [][]float64{
		{0, radius},
		{length, radius},
	}

	return NewMandrelFromPoints(points)
}

// parseArbitraryAxialMandrel creates a Mandrel from a profile (CSV file or points).
func parseArbitraryAxialMandrel(mandrelMap map[string]interface{}) (*Mandrel, error) {
	// Check if profile is a string (CSV filename) or array of points
	profile, ok := mandrelMap["profile"]
	if !ok {
		return nil, fmt.Errorf("arbitrary_axial mandrel must have 'profile' field")
	}

	switch v := profile.(type) {
	case string:
		// It's a CSV filename
		return NewMandrelFromCSV(v)
	case []interface{}:
		// It's an array of points
		points := make([][]float64, 0, len(v))
		for i, pointData := range v {
			pointArray, ok := pointData.([]interface{})
			if !ok {
				return nil, fmt.Errorf("profile point %d must be an array", i)
			}
			if len(pointArray) != 2 {
				return nil, fmt.Errorf("profile point %d must have exactly 2 coordinates", i)
			}

			x, ok1 := pointArray[0].(float64)
			z, ok2 := pointArray[1].(float64)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("profile point %d coordinates must be numbers", i)
			}

			points = append(points, []float64{x, z})
		}
		return NewMandrelFromPoints(points)
	default:
		return nil, fmt.Errorf("profile must be a string (CSV filename) or array of points")
	}
}

// Note: GUI integrations previously lived here (e.g. ParseAndCreateRenderer),
// but the current codebase no longer constructs a Gio-based renderer from this
// package. New GUIs should import ParseWindFromJSON and Layer2Path directly.
