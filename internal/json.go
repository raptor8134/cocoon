// Package internal - JSON parsing functions.
// This file handles parsing JSON wind configuration files into Mandrel and Wind objects.
package internal

import (
	"fmt"
	"os"

	json "github.com/KevinWang15/go-json5"
)

// WindJSON represents the structure of a JSON wind configuration file.
// This matches the structure used in the Python gcode_gen project.
type WindJSON struct {
	Comment  string                   `json:"comment"`
	Filament map[string]interface{}   `json:"filament"`
	Mandrel  map[string]interface{}   `json:"mandrel"`
	Layers   []map[string]interface{} `json:"layers"`
}

// ParseWindFromJSON parses a JSON file and creates a Wind object.
// This function handles both "cylindrical" and "arbitrary_axial" mandrel types.
func ParseWindFromJSON(filename string) (*Wind, error) {
	// Read the JSON file
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read JSON file: %w", err)
	}

	return ParseWindFromJSONBytes(data)
}

// ParseWindFromJSONText parses JSON/JSON5 text and creates a Wind object.
// This is primarily for GUI/editor integrations.
func ParseWindFromJSONText(text string) (*Wind, error) {
	return ParseWindFromJSONBytes([]byte(text))
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

	// Create Wind object
	wind := &Wind{
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

	// Override with explicit values if provided
	if width, ok := filamentMap["width"].(float64); ok {
		filament.Width = width
	}
	if thickness, ok := filamentMap["thickness"].(float64); ok {
		filament.Thickness = thickness
	}
	if feedrate, ok := filamentMap["feedrate"].(float64); ok {
		filament.Feedrate = feedrate
	}

	// Validate that we have at least some values
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
