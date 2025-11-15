# Cocoon - G-code Generator (Go Port)

**Cocoon** stands for **C**nc **O**perated **CO**mposite **O**verwrap **N**avigator.

This is a Go port of the core path generation logic from the Python gcode_gen project. The code is heavily annotated to help learn Go syntax and idioms.

## Structure

- `internal/` - Core path generation library (heavily annotated)
- `cmd/cocoon/` - Command-line application

## Quick Start

```bash
# Build the executable
go build ./cmd/cocoon

# Run it
./cocoon

# Or build and run in one step
go run ./cmd/cocoon
```

## Learning Go

See `internal/README.md` for detailed explanations of Go concepts and how they compare to Python.

## Files

- **internal/types.go** - Core data structures (Point, Layer, Wind, etc.)
- **internal/mandrel.go** - Mandrel profile handling and interpolation
- **internal/pathgen.go** - Path generation algorithms (hoop and helical)
- **internal/gcode.go** - G-code string generation

All files contain extensive comments explaining Go syntax and idioms.

