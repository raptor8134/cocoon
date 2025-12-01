// Package main - Cocoon application (CLI and GUI).
// Cocoon: Cnc Operated COmposite Overwrap Navigator
// This is the main entry point for the Cocoon G-code generator.
package main

import (
	"cocoon/internal"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Example usage of the Go path generation library.
// This demonstrates how to:
// 1. Create a mandrel
// 2. Define layers
// 3. Generate paths
// 4. Convert to G-code
func main() {
	// If no arguments provided, launch GUI
	// Otherwise, check if first argument is a JSON file
	if len(os.Args) == 1 {
		launchGUI()
	} else {
		firstArg := os.Args[1]
		// Check if the first argument is a JSON file
		if strings.HasSuffix(strings.ToLower(firstArg), ".json") {
			runJSONMode(firstArg)
		} else {
			fmt.Println("Bad filename: `", firstArg, "`\n")
		}
	}
}

// launchGUI starts the graphical user interface.
// This is a placeholder that will be implemented when you add your GUI framework.
func launchGUI() {
	fmt.Println("GUI mode - launching graphical interface...")
	fmt.Println("(GUI implementation coming soon)")

	// TODO: Initialize your GUI framework here
	// Examples:
	//   - Fyne: app := app.New(); window := app.NewWindow("Cocoon")
	//   - Gio: [your Gio setup]
	//   - Wails: [your Wails setup]
	//   - etc.

	// For now, just exit
	os.Exit(0)
}

// runJSONMode parses a JSON file and generates G-code from it.
func runJSONMode(jsonFile string) {
	fmt.Printf("Parsing JSON file: %s\n", jsonFile)

	// Parse the JSON file into a Wind object
	wind, err := internal.ParseWindFromJSON(jsonFile)
	if err != nil {
		log.Fatalf("Failed to parse JSON file: %v", err)
	}

	fmt.Printf("Parsed mandrel: length=%.2fmm, radius=%.2fmm\n", wind.Mandrel.Length, wind.Mandrel.MaxZ())
	fmt.Printf("Filament: width=%.2fmm, thickness=%.2fmm, feedrate=%.2f\n",
		wind.Filament.Width, wind.Filament.Thickness, wind.Filament.Feedrate)
	fmt.Printf("Number of layers: %d\n", len(wind.Layers))

	// Generate paths for all layers
	for i := range wind.Layers {
		layer := &wind.Layers[i]
		fullpath, err := internal.Layer2Path(wind.Mandrel, wind.Filament, layer)
		if err != nil {
			log.Fatalf("Failed to generate path for layer %d: %v", i, err)
		}
		fmt.Printf("Layer %d (%s): Generated %d points\n", i+1, layer.LType, len(fullpath))
	}

	// Convert to G-code
	gcode := internal.Layers2Gcode(wind.Layers, "")

	fmt.Printf("\nGenerated %d G-code commands\n", len(gcode))

	// Write to file in ./gcode/ directory
	baseName := strings.TrimSuffix(filepath.Base(jsonFile), filepath.Ext(jsonFile))

	// Ensure gcode directory exists
	gcodeDir := "gcode"
	if err := os.MkdirAll(gcodeDir, 0755); err != nil {
		log.Fatalf("Failed to create gcode directory: %v", err)
	}

	outputFile := filepath.Join(gcodeDir, fmt.Sprintf("%s.gcode", baseName))

	// Format G-code
	formattedGcode := internal.FormatGcodeLines(gcode)

	// Write to file
	if err := os.WriteFile(outputFile, []byte(formattedGcode), 0644); err != nil {
		log.Printf("Warning: Failed to write G-code file: %v", err)
	} else {
		fmt.Printf("G-code written to: %s\n", outputFile)
	}
}
