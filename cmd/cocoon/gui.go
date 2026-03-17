package main

import (
	"cocoon/internal"
	"cocoon/internal/storage"
	_ "embed"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"cogentcore.org/core/base/fileinfo"
	"cogentcore.org/core/colors"
	"cogentcore.org/core/core"
	"cogentcore.org/core/events"
	"cogentcore.org/core/icons"
	"cogentcore.org/core/math32"
	"cogentcore.org/core/styles"
	"cogentcore.org/core/styles/abilities"
	"cogentcore.org/core/styles/units"
	"cogentcore.org/core/text/lines"
	"cogentcore.org/core/text/text"
	"cogentcore.org/core/text/textcore"
	"cogentcore.org/core/xyz"
	"cogentcore.org/core/xyz/xyzcore"
)

// AppState holds minimal GUI state for the placeholder GUI.
type AppState struct {
	Lines *lines.Lines

	wind *internal.Wind
	err  error

	sc *xyz.Scene

	// sunMain is a camera-aligned directional light (view-locked "sun")
	// that provides the primary curvature cues on the mandrel.
	sunMain *xyz.Directional

	// sunFill is a very dim opposing directional that gently fills the
	// back side so there is no completely "dark" side while still
	// preserving a sense of primary light direction.
	sunFill *xyz.Directional

	// sunSideA/B are low-intensity side fills to soften the hard
	// Lambertian terminator on round solids.
	sunSideA *xyz.Directional
	sunSideB *xyz.Directional

	// lcsRoot is the root of the local coordinate system that
	// contains mandrel / axes / paths / bounding sphere. Camera
	// navigation manipulates this node instead of moving the camera.
	lcsRoot *xyz.Group

	mandrelCenter math32.Vector3
	mandrelRadius float32

	// lastRenderStats holds coarse-grained metrics from the most recent
	// 3D scene rebuild. It is populated whenever BuildXYZScene runs.
	lastRenderStats internal.RenderStats

	// EditorErrText shows parser/path errors at the bottom of the editor pane.
	EditorErrText *core.Text

	// EditorTitle is the label above the JSON editor showing the current file name.
	EditorTitle *core.Text

	// GcodeEditor is the read-only editor showing generated G-code in the GCODE tab.
	GcodeEditor *textcore.Editor

	// OnWindUpdated is set by setupViewerContent when the 3D viewer is ready.
	// When non-nil, calling it rebuilds the 3D scene from the latest parsed wind.
	OnWindUpdated func()

	// debounceTimer schedules a single recompute 100ms after the last editor input.
	debounceTimer *time.Timer

	// CurrentName is the logical JSON filename associated with the
	// current editor contents (e.g. "test.json"). It is used by the
	// File menu Save/Save As actions.
	CurrentName string

	// Dirty is true when the editor contents differ from the last
	// successful load or save operation.
	Dirty bool
}

//go:embed winds/test.json
var defaultWindJSON string

// launchGUI starts the graphical user interface using Cogent Core.
func launchGUI() {
	// TODO(wasm): re-enable Cogent Core prefs/settings once AppDataDir and
	// filesystem-backed settings are reliably supported in the web build.
	// For now we disable prefs to avoid WASM crashes from OpenSettings/WriteFile.
	//core. = true

	mainrunGUI().RunMainWindow()
}

// mainrunGUI creates a simple two-pane window: editor on the left, placeholder on the right.
func mainrunGUI() *core.Body {
	b := core.NewBody("Cocoon")
	main := core.NewFrame(b)
	main.Styler(func(s *styles.Style) {
		s.Direction = styles.Column
		s.Grow.X = 1
		s.Grow.Y = 1
	})

	state := &AppState{}

	// True top bar: File / Edit dropdown menus at the top of the window.
	menuBar := core.NewFrame(main)
	menuBar.Styler(func(s *styles.Style) {
		s.Direction = styles.Row
		s.Gap.Set(units.Dp(4))
		// Menubar items flush against top/left; buttons keep their internal padding.
		s.Padding.Set(units.Dp(0))
	})

	fileBtn := core.NewButton(menuBar).SetText("File")
	fileBtn.Type = core.ButtonText
	fileBtn.SetMenu(func(m *core.Scene) {
		core.NewButton(m).SetText("Open…").OnClick(func(e events.Event) {
			if state.CurrentName == "" {
				state.CurrentName = "test.json"
			}
			// Reuse existing helper; on desktop/web it routes through storage.
			// We need an editor to load into, so defer wiring to the concrete
			// editor instance below via a closure; see note in setup below.
		})
		core.NewButton(m).SetText("Save").OnClick(func(e events.Event) {
			if err := saveCurrent(state); err != nil {
				fmt.Println("Save error:", err)
			}
		})
		core.NewButton(m).SetText("Save As…").OnClick(func(e events.Event) {
			name := state.CurrentName
			if name == "" {
				name = "untitled.json"
			}
			name = "copy_of_" + name
			if err := saveAs(state, name); err != nil {
				fmt.Println("Save As error:", err)
			}
		})
		core.NewSeparator(m)
		core.NewButton(m).SetText("Download…").OnClick(func(e events.Event) {
			if err := downloadCurrent(state); err != nil {
				fmt.Println("Download error:", err)
			}
		})
	})

	editBtn := core.NewButton(menuBar).SetText("Edit")
	editBtn.Type = core.ButtonText
	editBtn.SetMenu(func(m *core.Scene) {
		core.NewButton(m).SetText("Preferences…").OnClick(func(e events.Event) {
			fmt.Println("Preferences dialog not yet implemented.")
		})
	})

	sp := core.NewSplits(main)
	sp.Styler(func(s *styles.Style) {
		s.Direction = styles.Row
		s.Grow.X = 1
		s.Grow.Y = 1
	})
	sp.SetSplits(0.4, 0.6)

	edFrame := core.NewFrame(sp)
	edFrame.Styler(func(s *styles.Style) {
		s.Direction = styles.Column
		s.Grow.X = 1
		s.Grow.Y = 1
	})

	viewFrame := core.NewFrame(sp)
	viewFrame.Styler(func(s *styles.Style) {
		s.Direction = styles.Column
		s.Grow.X = 1
		s.Grow.Y = 1
	})

	// Left pane: two tabs — JSON input (editable) and GCODE output (read-only).
	ts := core.NewTabs(edFrame)
	ts.Styler(func(s *styles.Style) {
		s.Grow.X = 1
		s.Grow.Y = 1
	})
	jsonFrame, jsonTab := ts.NewTab("Editor")
	jsonTab.Styler(func(s *styles.Style) {
		// Make the tab buttons share available width evenly and center their labels.
		s.Grow.X = 1
		s.Align.Items = styles.Center
		s.Justify.Content = styles.Center
	})
	jsonFrame.Styler(func(s *styles.Style) {
		s.Direction = styles.Column
		s.Grow.X = 1
		s.Grow.Y = 1
	})
	gcodeFrame, gcodeTab := ts.NewTab("Viewer")
	gcodeTab.Styler(func(s *styles.Style) {
		// Make the tab buttons share available width evenly and center their labels.
		s.Grow.X = 1
		s.Align.Items = styles.Center
		s.Justify.Content = styles.Center
	})
	gcodeFrame.Styler(func(s *styles.Style) {
		s.Direction = styles.Column
		s.Grow.X = 1
		s.Grow.Y = 1
	})
	settingsFrame, settingsTab := ts.NewTab("Settings")
	settingsTab.Styler(func(s *styles.Style) {
		// Make the tab buttons share available width evenly and center their labels.
		s.Grow.X = 1
		s.Align.Items = styles.Center
		s.Justify.Content = styles.Center
	})
	settingsFrame.Styler(func(s *styles.Style) {
		s.Direction = styles.Column
		s.Grow.X = 1
		s.Grow.Y = 1
	})

	ed := setupEditor(jsonFrame, state)
	setupGcodeOutput(gcodeFrame, state)
	setupViewer(viewFrame, state, ed)

	// Now that we have a concrete editor, wire the File→Open… menu to use it.
	fileBtn.SetMenu(func(m *core.Scene) {
		core.NewButton(m).SetText("Open…").OnClick(func(e events.Event) {
			if state.CurrentName == "" {
				state.CurrentName = "test.json"
			}
			if err := loadFrom(state, ed, state.CurrentName); err != nil {
				fmt.Println("Open error:", err)
			}
		})
		core.NewButton(m).SetText("Save").OnClick(func(e events.Event) {
			if err := saveCurrent(state); err != nil {
				fmt.Println("Save error:", err)
			}
		})
		core.NewButton(m).SetText("Save As…").OnClick(func(e events.Event) {
			name := state.CurrentName
			if name == "" {
				name = "untitled.json"
			}
			name = "copy_of_" + name
			if err := saveAs(state, name); err != nil {
				fmt.Println("Save As error:", err)
			}
		})
		core.NewSeparator(m)
		core.NewButton(m).SetText("Download…").OnClick(func(e events.Event) {
			if err := downloadCurrent(state); err != nil {
				fmt.Println("Download error:", err)
			}
		})
	})

	return b
}

// setupEditor creates a simple text editor for JSON configuration on the left pane,
// with an error line at the bottom for parser/path generator messages.
func setupEditor(parent *core.Frame, state *AppState) *textcore.Editor {
	title := core.NewText(parent).SetText("Wind editor: no file")
	title.SetType(core.TextTitleMedium)
	title.Styler(func(s *styles.Style) {
		s.Align.Items = styles.Center
		s.Text.Align = text.Center
	})
	state.EditorTitle = title

	ed := textcore.NewEditor(parent)
	ed.Styler(func(s *styles.Style) {
		s.Grow.X = 1
		s.Grow.Y = 1
	})
	ed.Lines.SetLanguage(fileinfo.Json)

	// Initial load: try winds/test.json via storage, then embedded JSON.
	if b, err := storage.Default().Load("test.json"); err == nil {
		println("setupEditor: loaded default wind JSON from storage as test.json")
		ed.Lines.SetString(string(b))
		state.CurrentName = "test.json"
		state.Dirty = false
	} else if defaultWindJSON != "" {
		println("setupEditor: FAILED to load test.json from storage, using embedded default JSON:", err.Error())
		ed.Lines.SetString(defaultWindJSON)
		state.CurrentName = "embedded.json"
		state.Dirty = false
	} else {
		println("setupEditor: no storage or embedded default JSON available")
		ed.Lines.SetString("{\n  // failed to load winds/test.json\n}\n")
		state.CurrentName = "untitled.json"
		state.Dirty = true
	}

	state.Lines = ed.Lines

	// Now that CurrentName has been initialized, update the title to reflect it.
	updateEditorTitle(state)

	// Error area at bottom of editor: always visible, never covered. Reserve space so editor can't shrink it away.
	errorFrame := core.NewFrame(parent)
	errorFrame.Styler(func(s *styles.Style) {
		s.Grow.X = 1
		s.Grow.Y = 0
		s.Min.Y = units.Em(2.5)
		s.Padding.Set(units.Dp(6))
		s.Background = colors.Scheme.SurfaceContainerHighest
		s.Border.Radius.Set(units.Dp(6))
	})
	editorErr := core.NewText(errorFrame)
	editorErr.Styler(func(s *styles.Style) {
		s.Color = colors.Scheme.Error.Base
		s.Grow.X = 1
	})
	editorErr.SetType(core.TextBodySmall)
	editorErr.SetText("no errors (init)")
	state.EditorErrText = editorErr

	// Debounced validation: at most once per 100ms after typing. On web we use
	// setTimeout so the callback runs on the browser main thread (avoids panic
	// in Cogent text styling).
	ed.On(events.Input, func(e events.Event) {
		scheduleDebouncedRecompute(state, func() {
			txt := state.Lines.String()
			w, msg := ValidateContent(txt)
			state.wind = w
			if w == nil {
				state.err = fmt.Errorf("%s", msg)
			} else {
				state.err = nil
				updateGcodeForState(state)
			}
			updateEditorErrorText(state, msg)
			// Any successful input path marks the document as dirty.
			state.Dirty = true
			if state.OnWindUpdated != nil {
				state.OnWindUpdated()
			}
		})
	})

	// Initial validation so load errors show immediately and viewer can render.
	txt := ed.Lines.String()
	w, msg := ValidateContent(txt)
	state.wind = w
	if w == nil {
		println("setupEditor: initial ValidateContent returned nil wind, msg:", msg)
	} else {
		println("setupEditor: initial ValidateContent returned non-nil wind, msg:", msg)
		updateGcodeForState(state)
	}
	if w == nil {
		state.err = fmt.Errorf("%s", msg)
	} else {
		state.err = nil
	}
	updateEditorErrorText(state, msg)
	if state.OnWindUpdated != nil {
		state.OnWindUpdated()
	}

	return ed
}

// setupGcodeOutput creates the view-only G-code editor. It is placed in the
// second tab of the left pane and is regenerated whenever the JSON parses and
// path generation succeeds (see updateGcodeForState usage).
func setupGcodeOutput(parent *core.Frame, state *AppState) {
	title := core.NewText(parent).SetText("G-code output (view only)")
	title.SetType(core.TextTitleMedium)
	title.Styler(func(s *styles.Style) {
		s.Align.Items = styles.Center
		s.Text.Align = text.Center
	})

	gcodeEd := textcore.NewEditor(parent)
	gcodeEd.Styler(func(s *styles.Style) {
		s.Grow.X = 1
		s.Grow.Y = 1
		// Keep focusable so caret / selection works even though it is read-only.
		s.SetAbilities(true, abilities.Focusable)
	})
	gcodeEd.SetReadOnly(true)

	state.GcodeEditor = gcodeEd

	// Populate initial output if we already have a valid wind.
	updateGcodeForState(state)
}

// saveCurrent saves the current editor contents using the logical name in state.
// If no name is set, it returns an error; menu handlers can fall back to Save As.
func saveCurrent(state *AppState) error {
	if state == nil || state.Lines == nil {
		return fmt.Errorf("no document to save")
	}
	if state.CurrentName == "" {
		return fmt.Errorf("no current filename")
	}
	data := []byte(state.Lines.String())
	if err := storage.Default().Save(state.CurrentName, data); err != nil {
		return err
	}
	state.Dirty = false
	return nil
}

// saveAs saves the current buffer under a new logical name and updates state.
func saveAs(state *AppState, name string) error {
	if name == "" {
		return fmt.Errorf("empty filename")
	}
	state.CurrentName = name
	updateEditorTitle(state)
	return saveCurrent(state)
}

// loadFrom loads the named JSON into the editor, re-runs validation, and
// triggers a viewer update.
func loadFrom(state *AppState, ed *textcore.Editor, name string) error {
	if state == nil || ed == nil {
		return fmt.Errorf("no editor to load into")
	}
	data, err := storage.Default().Load(name)
	if err != nil {
		return err
	}
	ed.Lines.SetString(string(data))
	state.Lines = ed.Lines
	state.CurrentName = name
	state.Dirty = false
	updateEditorTitle(state)

	txt := ed.Lines.String()
	w, msg := ValidateContent(txt)
	state.wind = w
	if w == nil {
		state.err = fmt.Errorf("%s", msg)
	} else {
		state.err = nil
		updateGcodeForState(state)
	}
	if state.EditorErrText != nil {
		state.EditorErrText.SetText(msg)
		state.EditorErrText.NeedsRender()
	}
	if state.OnWindUpdated != nil {
		state.OnWindUpdated()
	}
	return nil
}

// setEditorErrorFromContent validates the editor content and sets the error area text.
// It uses state.Lines so it sees the same buffer as the viewer's recompute.
func setEditorErrorFromContent(state *AppState, txt string) {
	if state.EditorErrText == nil {
		return
	}
	_, msg := ValidateContent(txt)
	updateEditorErrorText(state, msg)
}

// updateEditorTitle updates the JSON editor title to reflect the current file name.
func updateEditorTitle(state *AppState) {
	if state == nil || state.EditorTitle == nil {
		return
	}
	name := state.CurrentName
	if name == "" {
		state.EditorTitle.SetText("Wind editor: no file")
	} else {
		state.EditorTitle.SetText("Wind editor: " + name)
	}
	state.EditorTitle.NeedsRender()
}

// updateEditorErrorText sets the error text and color based on the message content.
// When there is no error ("no errors" prefix), the text is shown in a success green;
// otherwise it uses the scheme's error color.
func updateEditorErrorText(state *AppState, msg string) {
	if state == nil || state.EditorErrText == nil {
		return
	}
	state.EditorErrText.SetText(msg)
	if strings.HasPrefix(msg, "no errors") {
		state.EditorErrText.Styler(func(s *styles.Style) {
			s.Color = colors.Scheme.Success.Base
			s.Grow.X = 1
		})
	} else {
		state.EditorErrText.Styler(func(s *styles.Style) {
			s.Color = colors.Scheme.Error.Base
			s.Grow.X = 1
		})
	}
	state.EditorErrText.NeedsRender()
}

// updateGcodeForState regenerates the G-code text for the current wind and
// pushes it into the view-only G-code editor. It is safe to call any time
// after ValidateContent has populated state.wind and layer paths.
func updateGcodeForState(state *AppState) {
	if state == nil || state.wind == nil || state.GcodeEditor == nil || state.GcodeEditor.Lines == nil {
		return
	}
	if len(state.wind.Layers) == 0 {
		state.GcodeEditor.Lines.SetString("// no layers; nothing to generate\n")
		state.GcodeEditor.NeedsRender()
		return
	}

	// Work on a copy so that G-code generation cannot mutate the live wind.
	layers := make([]internal.Layer, len(state.wind.Layers))
	copy(layers, state.wind.Layers)

	gcLines := internal.Layers2Gcode(layers)
	gcText := internal.FormatGcodeLines(gcLines)
	state.GcodeEditor.Lines.SetString(gcText)
	state.GcodeEditor.NeedsRender()
}

// Flow when the renderer needs JSON parsed and paths generated:
//
//  1. Content source: state.Lines (same as ed.Lines) holds the JSON text.
//  2. Parse: internal.ParseWindFromJSONBytes([]byte(txt)) returns (*Wind, error).
//     - On error: JSON syntax (Unmarshal) or structure (filament/mandrel/layers).
//  3. Path gen: for each layer, internal.Layer2Path(mandrel, filament, &layer) returns ([]Point, error).
//     - Fills layer.FWPath, .BWPath, .FullPath; returns error if params are invalid.
//  4. Scene: if no error, internal.BuildXYZScene(scene, wind) builds the 3D view.
//
// We use ValidateContent(txt) as the single place that runs parse + path and returns
// a display message, so the editor error area and viewer stay in sync.

// formatEditorError returns a user-facing message for parse/path errors.
// When there is no error it returns "no errors" so the error area is always visible.
func formatEditorError(err error, fromPathgen bool) string {
	if err == nil {
		return "no errors"
	}
	msg := err.Error()
	if fromPathgen {
		return "Pathgen error: " + msg
	}
	if strings.Contains(msg, "failed to parse JSON") {
		return "Invalid JSON: " + msg
	}
	return "Parsing error (mismatch between given fields and expected fields): " + msg
}

// ValidateContent parses txt as wind JSON, runs path generation for all layers,
// and returns (wind, displayMessage). displayMessage is "no errors" on success, or
// a labeled string (Invalid JSON / Parsing error / Pathgen error) on failure.
// This is the single source of truth for validation and error text.
func ValidateContent(txt string) (wind *internal.Wind, displayMsg string) {
	w, err := internal.ParseWindFromJSONBytes([]byte(txt))
	if err != nil {
		return nil, formatEditorError(err, false)
	}
	for i := range w.Layers {
		_, err = internal.Layer2Path(w.Mandrel, w.Filament, &w.Layers[i])
		if err != nil {
			return nil, formatEditorError(err, true)
		}
	}
	return w, "no errors"
}

func setupViewer(parent *core.Frame, state *AppState, ed *textcore.Editor) {
	renderFrame := core.NewFrame(parent)
	renderFrame.Styler(func(s *styles.Style) {
		s.Grow.Set(1, 1)
		s.Display = styles.Stacked
		s.Border.Radius.Set(units.Dp(6))
	})

	// Create the viewer widget tree immediately so it participates in the
	// initial layout. On web, deferring widget creation until after async
	// WebGPU adapter/device probing can result in the underlying surface
	// only becoming drawable after a subsequent resize.
	setupViewerContent(renderFrame, state, ed)
}

func setupViewerContent(renderFrame *core.Frame, state *AppState, ed *textcore.Editor) {
	xyz.OrbitFactor = 0.001

	// Optional debug: enable Cogent Core's 3D update tracing to help diagnose
	// freezes / stalls in complex scenes. This is intentionally noisy.
	if v := os.Getenv("COCOON_DEBUG_3D_TRACE"); v == "1" || v == "true" || v == "TRUE" {
		xyz.Update3DTrace = true
	}

	state.sc = xyz.NewScene()
	// Multisampling is a major GPU cost for line-dense scenes on webgpu.
	// Use a lower sample count on wasm to improve frame time; desktop GPUs
	// can typically handle the default better.
	if runtime.GOOS == "js" {
		state.sc.MultiSample = 1
	}
	state.sc.Background = colors.Scheme.SurfaceDim
	// Soft ambient for baseline visibility; camera-aligned directional lights
	// are added in WebGPUReady once the scene is live so the "lit side"
	// generally faces the viewer without leaving a completely dark side.
	xyz.NewAmbient(state.sc, "ambient", 0.28, xyz.DirectSun)

	sw := xyzcore.NewSceneForScene(state.sc, renderFrame)
	sw.Styler(func(s *styles.Style) {
		s.Grow.Set(1, 1)
	})

	// On web, the underlying WebGPU surface is sometimes not fully sized on
	// first layout, so an initial render request can be dropped until after
	// a resize/aspect-ratio change. Schedule a follow-up render on the next
	// animation frame once the composer is definitely ready so the scene
	// appears without requiring the user to resize the window or inspector.
	internal.RunWhenReady(func() {
		sw.NeedsRender()
	})

	// Overlay frame that spans the render area; controls inside are aligned
	// to the top-left so the home button appears in the corner of the 3D view.
	overlay := core.NewFrame(renderFrame)
	overlay.Styler(func(s *styles.Style) {
		s.Grow.Set(1, 1)
		s.Align.Items = styles.Start
		s.Justify.Content = styles.Start
		s.Padding.Set(units.Dp(8))
		s.Border.Radius.Set(units.Dp(6))
	})

	header := core.NewFrame(overlay)
	header.Styler(func(s *styles.Style) {
		s.Direction = styles.Row
		s.Align.Items = styles.Center
		s.Gap.Set(units.Dp(8))
		s.Padding.Set(units.Dp(4))
		s.Background = colors.Scheme.SurfaceContainerHighest
		s.Border.Radius.Set(units.Dp(6))
	})

	leftHeader := core.NewFrame(header)
	leftHeader.Styler(func(s *styles.Style) {
		s.Direction = styles.Row
		s.Align.Items = styles.Center
		s.Gap.Set(units.Dp(4))
	})

	rightHeader := core.NewFrame(header)
	rightHeader.Styler(func(s *styles.Style) {
		s.Direction = styles.Row
		s.Align.Items = styles.Center
	})

	homeBtn := core.NewButton(leftHeader).SetIcon(icons.Home).SetTooltip("Reset camera (home)")
	homeBtn.Styler(func(s *styles.Style) {
		// Make the home button a prominent square icon.
		s.Min.Set(units.Dp(28), units.Dp(28))
		s.Padding.Set(units.Dp(4))
	})
	homeBtn.OnClick(func(e events.Event) {
		if state.sc == nil {
			return
		}
		_ = state.sc.SetCamera("home")
		updateCameraClip(state)
		sw.NeedsRender()
	})

	// Simple stats label showing coarse 3D scene metrics for diagnostics.
	statsLabel := core.NewText(rightHeader)
	statsLabel.SetType(core.TextBodySmall)
	statsLabel.Styler(func(s *styles.Style) {
		s.Color = colors.Scheme.OnSurfaceVariant
	})

	updateStatsLabel := func() {
		rs := state.lastRenderStats
		statsLabel.SetText(
			fmt.Sprintf("layers: %d  segments: %d  build: %.1f ms  mandrel: %s",
				rs.Layers, rs.Segments, rs.BuildMillis, map[bool]string{true: "rebuilt", false: "reused"}[rs.MandrelRebuilt]),
		)
		statsLabel.NeedsRender()
	}

	sw.OnFirst(events.KeyChord, func(e events.Event) {
		e.SetHandled()
		if state.sc == nil || state.sc.NoNav {
			return
		}
		state.sc.KeyChordEvent(e)
		updateCameraClip(state)
		sw.NeedsRender()
	})

	sw.OnFirst(events.SlideMove, func(e events.Event) {
		e.SetHandled()
		if state.sc == nil || state.sc.NoNav {
			return
		}
		// Require an orbit LCS root to manipulate instead of moving the camera.
		// This is the second LCS ("orbitRoot") whose origin is fixed at the
		// global coordinate system origin.
		lcs := state.lcsRoot
		if lcs == nil {
			if nd := state.sc.ChildByName("orbitRoot", 0); nd != nil {
				if g, ok := nd.(*xyz.Group); ok {
					lcs = g
					state.lcsRoot = g
				}
			}
		}
		if lcs == nil {
			return
		}
		pos := sw.Geom.ContentBBox.Min
		e.SetLocalOff(e.LocalOff().Add(pos))
		del := e.PrevDelta()
		dx := float32(del.X)
		dy := float32(del.Y)
		// Orbit sensitivity should be based on window size (pixels),
		// not on mandrel size / camera distance.
		bsz := sw.Geom.ContentBBox.Size()
		wpx := float32(bsz.X)
		hpx := float32(bsz.Y)
		if wpx <= 1 {
			wpx = 1
		}
		if hpx <= 1 {
			hpx = 1
		}
		// Use an effective screen dimension as the drag denominator; in
		// absence of a global screen resolution, approximate with the
		// average of width and height.
		denom := (wpx + hpx) * 0.5
		if denom <= 1 {
			denom = 1
		}

		// Derive camera-relative axes so drag is interpreted in screen space:
		// viewDir points from camera toward the scene, right is screen-right,
		// and upScreen is screen-up. Rotations are applied about these axes.
		cam := &state.sc.Camera
		viewDir := cam.Target.Sub(cam.Pose.Pos).Normal()
		if viewDir.Length() == 0 {
			viewDir = math32.Vec3(0, 0, -1)
		}
		right := viewDir.Cross(cam.UpDir).Normal()
		if right.Length() == 0 {
			right = math32.Vec3(1, 0, 0)
		}
		upScreen := right.Cross(viewDir).Normal()
		switch e.MouseButton() {
		case events.Left, events.Right:
			// Rotate the local coordinate system about its origin to
			// achieve an orbit-like effect with a fixed camera.
			// Horizontal drag rotates around the camera's screen-up axis,
			// vertical drag rotates around the camera's screen-right axis.
			// Signs are chosen so the visible surface appears to "follow"
			// the mouse drag direction.
			const orbitDegPerFullDrag = float32(180) // degrees for dragging across full effective size
			const orbitSensitivity = float32(2.5)    // overall gain

			// Drag right-to-left should spin the front surface as if pulled
			// right-to-left: rotate about screen-up so points move with dx.
			yawDeg := (dx / denom) * orbitDegPerFullDrag * orbitSensitivity
			// Drag up-to-down should spin about screen-right in a similar sense.
			pitchDeg := (dy / denom) * orbitDegPerFullDrag * orbitSensitivity

			// Rotate orbitRoot around GLOBAL axes (not local axes) by
			// converting the desired world-space axis into the current
			// local frame.
			inv, _ := lcs.Pose.Matrix.Inverse()
			if yawDeg != 0 {
				axisLocal := upScreen.MulMatrix4AsVector4(inv, 0).Normal()
				lcs.Pose.RotateOnAxis(axisLocal.X, axisLocal.Y, axisLocal.Z, yawDeg)
			}
			if pitchDeg != 0 {
				axisLocal := right.MulMatrix4AsVector4(inv, 0).Normal()
				lcs.Pose.RotateOnAxis(axisLocal.X, axisLocal.Y, axisLocal.Z, pitchDeg)
			}
			state.sc.Update()
			updateCameraClip(state)
			sw.NeedsRender()
		}
	})

	sw.OnFirst(events.Scroll, func(e events.Event) {
		e.SetHandled()
		if state.sc == nil || state.sc.NoNav {
			return
		}
		// Zoom manipulates the orbit LCS (second LCS) scale instead of
		// moving the camera along its view vector.
		lcs := state.lcsRoot
		if lcs == nil {
			if nd := state.sc.ChildByName("orbitRoot", 0); nd != nil {
				if g, ok := nd.(*xyz.Group); ok {
					lcs = g
					state.lcsRoot = g
				}
			}
		}
		if lcs == nil {
			return
		}
		me, ok := e.(*events.MouseScroll)
		if !ok {
			return
		}
		me.Delta.Y *= 0.35
		cdist := math32.Max(state.sc.Camera.DistanceTo(state.sc.Camera.Target), 1.0)
		zoomDel := float32(.02) * cdist
		zoom := float32(me.Delta.Y)
		zoomPct := (zoom * zoomDel) / cdist

		// Map zoom percentage to a multiplicative scale factor and
		// clamp to a reasonable range so the LCS cannot collapse or
		// explode numerically.
		curScale := lcs.Pose.Scale
		if curScale == (math32.Vector3{}) {
			curScale.Set(1, 1, 1)
		}
		// Positive scroll zooms in (larger scale), negative zooms out.
		factor := 1 - zoomPct
		if factor <= 0.1 {
			factor = 0.1
		}
		newS := curScale.MulScalar(factor)
		const minS, maxS = 0.01, 100.0
		if newS.X < minS {
			newS.Set(minS, minS, minS)
		} else if newS.X > maxS {
			newS.Set(maxS, maxS, maxS)
		}
		lcs.Pose.Scale = newS
		state.sc.Update()
		updateCameraClip(state)
		sw.NeedsRender()
	})

	updateViewerScene := func() {
		w := state.wind
		if w != nil && state.sc != nil {
			state.mandrelCenter, state.mandrelRadius, state.lastRenderStats = internal.BuildXYZScene(state.sc, w)
			// Cache the local-coordinate-system root created by BuildXYZScene
			// so navigation handlers can manipulate it directly.
			if nd := state.sc.ChildByName("lcsRoot", 0); nd != nil {
				if g, ok := nd.(*xyz.Group); ok {
					state.lcsRoot = g
				}
			}
			updateCameraClip(state)
			updateStatsLabel()
			// On web, also schedule a follow-up render after the next
			// animation frame once the composer is ready, to catch cases
			// where the surface size or layout settles slightly after the
			// scene rebuild triggered by a wind update.
			internal.RunWhenReady(func() {
				sw.NeedsRender()
			})
		} else if state.sc != nil {
			state.sc.DeleteChildren()
			state.sc.Update()
			state.lastRenderStats = internal.RenderStats{}
			updateStatsLabel()
		}
		sw.NeedsRender()
	}

	// Only start building the WebGPU-backed 3D scene after we've confirmed
	// adapter/device acquisition. This avoids attempting to render before
	// the backend is actually ready, while still allowing the widget tree
	// to be laid out immediately.
	internal.WebGPUReady(func(ok bool) {
		if !ok {
			core.NewText(renderFrame).
				SetType(core.TextBodyLarge).
				SetText("3D viewer unavailable in this browser. For the full 3D view, please use a browser and version that supports WebGPU (navigator.gpu), such as a recent Chrome or Chromium build with hardware accelreation enabled.")
			return
		}

		// Add a pair of directional lights that track the camera view:
		// - sunMain: aligned with the camera→mandrel-center vector, like a
		//   soft headlight so the viewer-facing side is gently lit.
		// - sunFill: opposite direction with very low intensity so the back
		//   side is never completely dark.
		// Intensities are boosted (5× original) for stronger curvature cues.
		state.sunMain = xyz.NewDirectional(state.sc, "mandrel-sun-main", 1.50, xyz.DirectSun)
		state.sunFill = xyz.NewDirectional(state.sc, "mandrel-sun-fill", 0.50, xyz.DirectSun)
		// Two additional side fills help eliminate the sharp light/dark cutoff
		// on cylindrical surfaces without requiring shader changes.
		state.sunSideA = xyz.NewDirectional(state.sc, "mandrel-sun-side-a", 0.40, xyz.DirectSun)
		state.sunSideB = xyz.NewDirectional(state.sc, "mandrel-sun-side-b", 0.40, xyz.DirectSun)

		state.OnWindUpdated = updateViewerScene

		// If the editor already validated and populated state.wind before the
		// viewer was ready, render that initial state now.
		updateViewerScene()
	})
}

// updateCameraClip tightens the camera near/far planes around the mandrel bounding sphere
// to improve depth precision. The sphere is centered on the mandrel axis midpoint with
// radius stored in AppState (half-length + 20mm).
func updateCameraClip(state *AppState) {
	if state == nil || state.sc == nil {
		return
	}
	sc := state.sc
	r := state.mandrelRadius
	if r <= 0 {
		return
	}
	cam := &sc.Camera

	// Enforce zoom clamp for *all* camera changes, not just scroll.
	if sc.SavedCams != nil {
		if home, ok := sc.SavedCams["home"]; ok {
			homeDist := home.Pose.Pos.Sub(home.Target).Length()
			if homeDist > 0 {
				minDist := 0.1 * homeDist
				maxDist := 10.0 * homeDist
				curVec := cam.Pose.Pos.Sub(cam.Target)
				curDist := curVec.Length()
				if curDist > 0 {
					switch {
					case curDist < minDist:
						cam.Pose.Pos = cam.Target.Add(curVec.Normal().MulScalar(minDist))
						cam.LookAtTarget()
					case curDist > maxDist:
						cam.Pose.Pos = cam.Target.Add(curVec.Normal().MulScalar(maxDist))
						cam.LookAtTarget()
					}
				}
			}
		}
	}

	// Distance from camera to mandrel center.
	dv := cam.Pose.Pos.Sub(state.mandrelCenter)
	d := dv.Length()
	if d <= 0 {
		return
	}

	// If the scene is being scaled in the local coordinate system,
	// expand the effective bounding radius accordingly so the near/far
	// clip planes continue to tightly bound the visible content.
	scaleFactor := float32(1)
	if state.lcsRoot != nil {
		ws := state.lcsRoot.Pose.WorldScale()
		// Assume uniform-ish scale and take the average magnitude.
		scaleFactor = (ws.X + ws.Y + ws.Z) / 3
		if scaleFactor <= 0 {
			scaleFactor = 1
		}
	}
	effectiveR := r * scaleFactor

	// Use the same rule inside/outside the sphere to avoid sudden clipping changes.
	near := d - effectiveR*2
	far := d + effectiveR*1.1
	if near < 0.01 {
		near = 0.01
	}
	if far <= near+0.01 {
		far = near + 0.01
	}
	cam.Near = near
	cam.Far = far

	// Keep the main directional light aligned with the camera→mandrel-center
	// vector, and a very dim fill light in the opposite direction so there
	// is always some illumination on the "back" side.
	if state.sunMain != nil || state.sunFill != nil || state.sunSideA != nil || state.sunSideB != nil {
		dir := state.mandrelCenter.Sub(cam.Pose.Pos)
		if dir.Length() > 0 {
			dir = dir.Normal()
			// Directional lights use their Pos as the origin→light vector;
			// the shader uses -normalize(Pos) as the light direction. So to
			// get light direction = (camera→mandrel), we set Pos = -dir.
			if state.sunMain != nil {
				state.sunMain.Pos = dir.MulScalar(-1)
			}
			// Fill from the opposite side (approximately behind the mandrel
			// relative to the camera) to gently light the back.
			if state.sunFill != nil {
				state.sunFill.Pos = dir // opposite direction of main
			}
			// Side fills from camera-right / camera-left directions.
			right := dir.Cross(cam.UpDir)
			if right.Length() > 0 {
				right = right.Normal()
			} else {
				right = math32.Vec3(1, 0, 0)
			}
			if state.sunSideA != nil {
				// Light direction = +right, so Pos = -right
				state.sunSideA.Pos = right.MulScalar(-1)
			}
			if state.sunSideB != nil {
				// Light direction = -right, so Pos = +right
				state.sunSideB.Pos = right
			}
			// Push updated light directions into the Phong pipeline.
			state.sc.Update()
		}
	}

	cam.UpdateMatrix()
}
