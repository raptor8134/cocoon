//go:build js && wasm

// Command wasm exposes the Cocoon winding core to the browser UI.
//
// This is the only Go code in the web build. It is deliberately thin: parse,
// generate, hand back results. All presentation lives in web/ as HTML/JS.
//
// Everything is exported onto window.cocoonCore, which web/js/wasm.js wraps.
package main

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"syscall/js"
	"time"

	"cocoon/internal/wind"

	json5 "github.com/KevinWang15/go-json5"
)

func main() {
	js.Global().Set("cocoonCore", map[string]any{
		"parse":     js.FuncOf(parse),
		"canonical": js.FuncOf(canonical),
		"generate":  js.FuncOf(generate),
	})
	// Signal readiness so the page can stop showing its loading state.
	if cb := js.Global().Get("cocoonCoreReady"); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}
	select {} // keep the Go runtime alive to service calls
}

// sourceName pulls an optional filename from the second argument, used only
// for the provenance header.
func sourceName(args []js.Value) string {
	if len(args) > 1 && args[1].Type() == js.TypeString {
		return args[1].String()
	}
	return "(unsaved)"
}

// errResult is the shared shape for failures, so JS can always check .error.
func errResult(msg string) any {
	return map[string]any{"error": msg}
}

// parse converts JSON5 source into strict JSON.
//
// The UI's block editor needs a plain object, and the wind files people write
// are JSON5 (unquoted keys, comments, trailing commas). Rather than ship a
// JSON5 parser to JS as well, we reuse the one the core already depends on:
// JS calls this and then JSON.parse's the result.
func parse(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("parse: missing source argument")
	}
	var v any
	if err := json5.Unmarshal([]byte(args[0].String()), &v); err != nil {
		return errResult("Invalid JSON: " + err.Error())
	}
	b, err := json.Marshal(v)
	if err != nil {
		return errResult("could not normalize config: " + err.Error())
	}
	return map[string]any{"json": string(b)}
}

// canonical pretty-prints a strict-JSON config, used when the UI switches from
// the block editor back to the text view.
func canonical(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("canonical: missing source argument")
	}
	var v any
	if err := json5.Unmarshal([]byte(args[0].String()), &v); err != nil {
		return errResult("Invalid JSON: " + err.Error())
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errResult(err.Error())
	}
	return map[string]any{"json": string(b)}
}

// maxSegmentDeg bounds the angle spanned by one rendered line segment, so long
// angular moves follow the mandrel's curve instead of cutting across it as a
// straight chord.
const maxSegmentDeg = 5.0

// generate parses a config, builds every layer's path, and returns the G-code
// plus geometry for the 3D view.
//
// Point positions come back as a single packed Float32 buffer rather than a JS
// array: a dense wind is millions of coordinates, and crossing the JS boundary
// value-by-value would dominate the runtime.
func generate(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("generate: missing source argument")
	}
	start := time.Now()

	w, err := wind.ParseWindFromJSONBytes([]byte(args[0].String()))
	if err != nil {
		return errResult(err.Error())
	}
	for i := range w.Layers {
		if _, err := wind.Layer2Path(w.Mandrel, w.Filament, &w.Layers[i]); err != nil {
			return errResult(err.Error())
		}
	}

	// Aggregate budget: a single layer can be within limits while the whole
	// wind is not. Checked before any buffers are allocated.
	if err := wind.CheckTotalPoints(w.Layers); err != nil {
		return errResult(err.Error())
	}

	// Flatten every layer's path into one XYZ buffer, recording each layer's
	// span so the viewer can colour them separately, and accumulate where the
	// fiber actually lands so coverage can be reported.
	var positions []float32
	layerInfo := make([]any, 0, len(w.Layers))

	// 200 axial stations resolves a bulge a few mm wide on a 250 mm mandrel;
	// 180 sectors is 2 degrees, fine enough to see a bare stripe narrower than
	// a typical tow.
	cov := wind.NewCoverage(w.Mandrel, 200, 180)

	for i := range w.Layers {
		layer := &w.Layers[i]
		startIdx := len(positions) / 3
		appendLayerPositions(&positions, layer.FullPath)
		cov.AddPath(layer.FullPath, w.Filament)
		layerInfo = append(layerInfo, map[string]any{
			"type":     layer.LType,
			"start":    startIdx,
			"count":    len(positions)/3 - startIdx,
			"disabled": layer.Disabled,
		})
	}

	// Reduce the deposition grid to a per-station profile for plotting.
	rep := cov.Report(w.Filament)
	covX := make([]float64, len(rep))
	covThick := make([]float64, len(rep))
	covFrac := make([]float64, len(rep))
	covMin := make([]float64, len(rep))
	gapCount := 0
	maxThick, minCovered := 0.0, math.Inf(1)
	for i, st := range rep {
		covX[i], covThick[i], covFrac[i], covMin[i] = st.X, st.Thickness, st.Fraction, st.MinFrac
		if st.Gap {
			gapCount++
		}
		if st.Thickness > maxThick {
			maxThick = st.Thickness
		}
		if st.Fraction < minCovered {
			minCovered = st.Fraction
		}
	}
	if math.IsInf(minCovered, 1) {
		minCovered = 0
	}

	// Full program with provenance header, so what the browser shows is what
	// the CLI would write.
	metrics := wind.ComputeMetrics(w.Layers, w.Machine)
	gcode := wind.FormatGcodeLines(wind.BuildProgram(w, wind.ProgramInfo{
		SourceName: sourceName(args),
		SourceJSON: args[0].String(),
		Density:    2.55, // E-glass
	}))

	totalPoints := 0
	for i := range w.Layers {
		totalPoints += len(w.Layers[i].FullPath)
	}

	return map[string]any{
		"gcode":     gcode,
		"positions": float32Bytes(positions),
		"layers":    layerInfo,
		"coverage": map[string]any{
			"x":             float64Bytes(covX),
			"thickness":     float64Bytes(covThick),
			"fraction":      float64Bytes(covFrac),
			"minFraction":   float64Bytes(covMin),
			"gapStations":   gapCount,
			"maxThickness":  maxThick,
			"worstFraction": minCovered,
			"fiberLength":   cov.FiberLength(),
		},
		"mandrel": map[string]any{
			"x":      float64Bytes(w.Mandrel.XPoints),
			"r":      float64Bytes(w.Mandrel.ZPoints),
			"length": w.Mandrel.Length,
			"zmax":   w.Mandrel.ZMax,
			"xmin":   w.Mandrel.XMin,
			"xmax":   w.Mandrel.XMax,
		},
		"metrics": map[string]any{
			"towLengthMm": metrics.TowLength,
			"seconds":     metrics.Seconds,
			"duration":    wind.FormatDuration(metrics.Seconds),
			"moves":       metrics.Moves,
			"minSeconds":  metrics.MinSeconds,
			"massGrams":   metrics.TowMass(w.Filament, 2.55),
		},
		"stats": map[string]any{
			"layers":   len(w.Layers),
			"points":   totalPoints,
			"vertices": len(positions) / 3,
			"ms":       float64(time.Since(start).Microseconds()) / 1000.0,
		},
	}
}

// appendLayerPositions converts a cylindrical path to Cartesian XYZ, inserting
// intermediate points wherever a move sweeps more than maxSegmentDeg.
func appendLayerPositions(out *[]float32, path []wind.Point) {
	if len(path) == 0 {
		return
	}
	push := func(p wind.Point) {
		r := p.ToRect()
		*out = append(*out, float32(r.X), float32(r.Y), float32(r.Z))
	}
	push(path[0])

	for j := 1; j < len(path); j++ {
		p0, p1 := path[j-1], path[j]
		steps := int(math.Ceil(math.Abs(p1.A-p0.A) / maxSegmentDeg))
		if steps < 1 {
			steps = 1
		}
		for s := 1; s <= steps; s++ {
			t := float64(s) / float64(steps)
			push(wind.Point{
				X: p0.X + t*(p1.X-p0.X),
				Y: p0.Y + t*(p1.Y-p0.Y),
				Z: p0.Z + t*(p1.Z-p0.Z),
				A: p0.A + t*(p1.A-p0.A),
				F: p0.F,
			})
		}
	}
}

// float32Bytes packs a float32 slice into a JS Uint8Array. The caller views it
// as a Float32Array over the same buffer, so no per-element conversion happens.
func float32Bytes(v []float32) js.Value {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	arr := js.Global().Get("Uint8Array").New(len(buf))
	js.CopyBytesToJS(arr, buf)
	return arr
}

// float64Bytes packs a float64 slice the same way, for the mandrel profile
// where the source data is already float64.
func float64Bytes(v []float64) js.Value {
	buf := make([]byte, len(v)*8)
	for i, f := range v {
		binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(f))
	}
	arr := js.Global().Get("Uint8Array").New(len(buf))
	js.CopyBytesToJS(arr, buf)
	return arr
}
