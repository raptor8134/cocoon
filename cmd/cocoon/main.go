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

func main() {
	args := os.Args[1:]

	// No arguments: launch the GUI.
	if len(args) == 0 {
		launchGUI()
		return
	}

	// Handle global flags (help, etc.) and reserve space for future flags.
	for _, a := range args {
		switch a {
		case "-h", "--help", "help":
			printUsage()
			return
		}
	}

	// Positional JSON file argument plus future options.
	var jsonFile string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			// Unknown flag for now; keep usage text centralized.
			fmt.Printf("Unknown option: %s\n\n", a)
			printUsage()
			os.Exit(1)
		}
		if jsonFile == "" {
			jsonFile = a
		} else {
			fmt.Println("Too many positional arguments.")
			printUsage()
			os.Exit(1)
		}
	}

	if jsonFile == "" {
		fmt.Println("No JSON file specified.")
		printUsage()
		os.Exit(1)
	}

	if !strings.HasSuffix(strings.ToLower(jsonFile), ".json") {
		fmt.Printf("Expected a .json file, got: %q\n\n", jsonFile)
		printUsage()
		os.Exit(1)
	}

	runJSONMode(jsonFile)
}

func printUsage() {
	fmt.Println("Cocoon - CNC Operated COmposite Overwrap Navigator")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  cocoon                 Launch the GUI")
	fmt.Println("  cocoon <file.json>     Generate G-code from a JSON configuration")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  -h, --help, help       Show this help message and exit")
}

// launchGUI starts the graphical user interface.
// Implementation is in gui.go

// runJSONMode parses a JSON file and generates G-code from it.
func runJSONMode(jsonFile string) {
	fmt.Printf("Parsing JSON file: %s\n", jsonFile)

	// Parse the JSON file into a Wind object
	wind, err := internal.ParseWindFromJSONFile(jsonFile)
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
	gcode := internal.Layers2Gcode(wind.Layers)

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
