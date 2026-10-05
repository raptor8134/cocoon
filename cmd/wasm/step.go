//go:build js && wasm

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"syscall/js"
	"time"

	"cocoon/internal/wind"
	"cocoon/internal/wind/step"
)

// importStep parses a STEP file and returns the candidate surfaces for review.
//
// Nothing is committed here. The parse produces an interpretation -- which axis,
// which surfaces, what was rejected and why -- and the UI presents it for
// confirmation before any of it becomes the mandrel. A wrong profile yields a
// plausible-looking wind and a ruined part, so the interpretation has to be
// inspectable rather than merely correct-by-assertion.
//
// args: (Uint8Array content, string filename, number lastModifiedMs)
func importStep(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("importStep: missing file content")
	}
	buf := make([]byte, args[0].Get("length").Int())
	js.CopyBytesToGo(buf, args[0])

	// Hash the bytes, not the parse. Identity is content: a file that hashes
	// the same is the same geometry regardless of what it has been renamed to
	// or when it was last touched.
	sum := sha256.Sum256(buf)
	hash := hex.EncodeToString(sum[:])

	doc, err := step.Parse(bytes.NewReader(buf))
	if err != nil {
		return errResult("Could not read STEP file: " + err.Error())
	}
	model, err := step.ReadModel(doc)
	if err != nil {
		return errResult(err.Error())
	}

	filename := ""
	if len(args) > 1 && args[1].Type() == js.TypeString {
		filename = args[1].String()
	}
	modified := ""
	if len(args) > 2 && args[2].Type() == js.TypeNumber {
		modified = time.UnixMilli(int64(args[2].Float())).UTC().Format(time.RFC3339)
	}

	cands := make([]any, 0, len(model.Candidates))
	for _, c := range model.Candidates {
		cands = append(cands, map[string]any{
			"id":        c.ID,
			"type":      c.Type,
			"name":      c.Name,
			"coaxial":   c.Coaxial,
			"rejected":  c.Rejected,
			"axialMin":  c.AxialMin,
			"axialMax":  c.AxialMax,
			"radiusMin": c.RadiusMin,
			"radiusMax": c.RadiusMax,
			// Segments travel as JSON so the UI can preview a candidate's
			// contribution and send back exactly what was accepted.
			"segments": segmentsJSON(c.Segments),
		})
	}

	return map[string]any{
		"hash":      hash,
		"filename":  filename,
		"modified":  modified,
		"sizeBytes": len(buf),
		"header": map[string]any{
			"name":              model.Header.Name,
			"timestamp":         model.Header.Timestamp,
			"author":            model.Header.Author,
			"organization":      model.Header.Organization,
			"preprocessor":      model.Header.PreprocessorVer,
			"originatingSystem": model.Header.OriginatingSystem,
			"schema":            model.Header.Schema,
			"units":             model.Header.Units,
		},
		"axis":       model.AxisLabel,
		"candidates": cands,
	}
}

// segmentsJSON converts step segments into the wind.Segment shape the design
// file stores, so what the UI previews is exactly what gets saved.
func segmentsJSON(segs []step.Seg) string {
	out := make([]wind.Segment, 0, len(segs))
	for _, s := range segs {
		w := wind.Segment{
			Kind:    wind.SegmentKind(s.Kind),
			Start:   wind.Pt{X: s.Start.X, R: s.Start.R},
			End:     wind.Pt{X: s.End.X, R: s.End.R},
			Center:  wind.Pt{X: s.Center.X, R: s.Center.R},
			Radius:  s.Radius,
			Degree:  s.Degree,
			Knots:   s.Knots,
			Weights: s.Weights,
		}
		for _, c := range s.Control {
			w.Control = append(w.Control, wind.Pt{X: c.X, R: c.R})
		}
		for _, p := range s.Points {
			w.Points = append(w.Points, wind.Pt{X: p.X, R: p.R})
		}
		out = append(out, w)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "[]"
	}
	return string(b)
}
