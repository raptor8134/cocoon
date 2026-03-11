package main

import (
	"cocoon/internal"
	"os"
	"path/filepath"

	"cogentcore.org/core/base/fileinfo"
	"cogentcore.org/core/colors"
	"cogentcore.org/core/core"
	"cogentcore.org/core/events"
	"cogentcore.org/core/icons"
	"cogentcore.org/core/math32"
	"cogentcore.org/core/styles"
	"cogentcore.org/core/styles/units"
	"cogentcore.org/core/text/lines"
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

	mandrelCenter math32.Vector3
	mandrelRadius float32
}

// launchGUI starts the graphical user interface using Cogent Core.
func launchGUI() {
	mainrunGUI().RunMainWindow()
}

// mainrunGUI creates a simple two-pane window: editor on the left, placeholder on the right.
func mainrunGUI() *core.Body {
	b := core.NewBody("Cocoon - G-code Generator")

	main := core.NewFrame(b)
	main.Styler(func(s *styles.Style) {
		s.Direction = styles.Column
		s.Grow.X = 1
		s.Grow.Y = 1
	})

	core.NewText(main).SetText("Cocoon - G-code Generator (Cogent Core placeholder GUI)").SetType(core.TextHeadlineMedium)

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

	state := &AppState{}
	ed := setupEditor(edFrame, state)
	setupViewer(viewFrame, state, ed)

	return b
}

// setupEditor creates a simple text editor for JSON configuration on the left pane.
func setupEditor(parent *core.Frame, state *AppState) *textcore.Editor {
	core.NewText(parent).SetText("Editor (JSON configuration)").SetType(core.TextTitleMedium)

	ed := textcore.NewEditor(parent)
	ed.Styler(func(s *styles.Style) {
		s.Grow.X = 1
		s.Grow.Y = 1
	})
	ed.Lines.SetLanguage(fileinfo.Json)

	// Default-load test.json into the editor.
	// Prefer local `winds/test.json`, but fall back to a minimal stub on error.
	defaultPath := filepath.Join("winds", "test.json")
	if b, err := os.ReadFile(defaultPath); err == nil {
		ed.Lines.SetString(string(b))
	} else {
		ed.Lines.SetString("{\n  // failed to load winds/test.json\n}\n")
	}

	state.Lines = ed.Lines
	return ed
}

// setupViewer creates the right-hand pane with a live path renderer.
func setupViewer(parent *core.Frame, state *AppState, ed *textcore.Editor) {
	// Error text stays under the render area.
	errText := core.NewText(parent)
	errText.Styler(func(s *styles.Style) {
		s.Color = colors.Scheme.Error.Base
	})

	// 3D scene viewer with built-in mouse navigation:
	// - Drag: orbit (Shift: pan)
	// - Scroll: zoom
	// Reduce sensitivity a bit from defaults.
	xyz.OrbitFactor = 0.01
	xyz.PanFactor = 0.0004

	// Container for render + overlays, stacked so overlays appear on top.
	renderFrame := core.NewFrame(parent)
	renderFrame.Styler(func(s *styles.Style) {
		s.Grow.Set(1, 1)
		s.Display = styles.Stacked
	})

	state.sc = xyz.NewScene()
	// Darker background so mandrel and paths stand out.
	state.sc.Background = colors.Scheme.SurfaceDim
	xyz.NewAmbient(state.sc, "ambient", 0.35, xyz.DirectSun)
	dir := xyz.NewDirectional(state.sc, "sun", 0.9, xyz.DirectSun)
	dir.Pos.Set(0, 2, 1)

	sw := xyzcore.NewSceneForScene(state.sc, renderFrame)
	sw.Styler(func(s *styles.Style) {
		s.Grow.Set(1, 1)
	})

	// Overlay Home button in the top-left of the render.
	header := core.NewFrame(renderFrame)
	header.Styler(func(s *styles.Style) {
		s.Direction = styles.Row
		s.Align.Items = styles.Center
		// Anchored top-left with a bit of margin.
		s.Margin.Set(units.Dp(8))
	})
	homeBtn := core.NewButton(header).SetIcon(icons.Home).SetTooltip("Reset camera (home)")
	homeBtn.OnClick(func(e events.Event) {
		if state.sc == nil {
			return
		}
		_ = state.sc.SetCamera("home")
		updateCameraClip(state)
		sw.NeedsRender()
	})

	// Ensure arrow-key navigation etc also respects zoom clamp and clip planes.
	sw.OnFirst(events.KeyChord, func(e events.Event) {
		e.SetHandled()
		if state.sc == nil || state.sc.NoNav {
			return
		}
		state.sc.KeyChordEvent(e)
		updateCameraClip(state)
		sw.NeedsRender()
	})

	// Mouse-drag navigation:
	// - Left-drag: orbit / rotate
	// - Right-drag: orbit / rotate (same as left)
	// (Arrows still work via xyz's KeyChord handler.)
	sw.OnFirst(events.SlideMove, func(e events.Event) {
		e.SetHandled()
		if state.sc == nil || state.sc.NoNav {
			return
		}
		pos := sw.Geom.ContentBBox.Min
		e.SetLocalOff(e.LocalOff().Add(pos))

		del := e.PrevDelta()
		dx := float32(del.X)
		dy := float32(del.Y)

		cam := &state.sc.Camera
		cdist := math32.Max(cam.DistanceTo(cam.Target), 1.0)
		orbDel := xyz.OrbitFactor * cdist

		if e.MouseButton() == events.Left || e.MouseButton() == events.Right {
			state.sc.Camera.Orbit(-dx*orbDel, -dy*orbDel)
			updateCameraClip(state)
			sw.NeedsRender()
		}
	})

	// Reduce scroll zoom sensitivity by scaling wheel delta before xyz handles it.
	// We mark the event handled so the default handler doesn't also run.
	sw.OnFirst(events.Scroll, func(e events.Event) {
		e.SetHandled()
		if state.sc == nil || state.sc.NoNav {
			return
		}
		me, ok := e.(*events.MouseScroll)
		if !ok {
			return
		}
		me.Delta.Y *= 0.35
		// Dolly zoom: change camera distance only (keep Target fixed).
		// This avoids "moving around" behavior of zoom-to-cursor.
		cdist := math32.Max(state.sc.Camera.DistanceTo(state.sc.Camera.Target), 1.0)
		zoomDel := float32(.02) * cdist
		zoom := float32(me.Delta.Y)
		zoomPct := (zoom * zoomDel) / cdist
		state.sc.Camera.Zoom(zoomPct)

		// Clamp zoom to 10% - 1000% of "home" camera distance.
		updateCameraClip(state)
		sw.NeedsRender()
	})

	recompute := func() {
		txt := state.Lines.String()
		w, err := internal.ParseWindFromJSONText(txt)
		if err == nil {
			for i := range w.Layers {
				_, err = internal.Layer2Path(w.Mandrel, w.Filament, &w.Layers[i])
				if err != nil {
					break
				}
			}
		}
		state.wind = w
		state.err = err
		if err != nil {
			errText.SetText(err.Error())
		} else {
			errText.SetText("")
		}

		if err == nil && w != nil {
			buildXYZScene(state, w)
		} else if state.sc != nil {
			state.sc.DeleteChildren()
			state.sc.Update()
		}
		sw.NeedsRender()
	}

	// Render on every keystroke.
	ed.On(events.Input, func(e events.Event) {
		recompute()
	})

	// Initial render.
	recompute()
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

	// Use the same rule inside/outside the sphere to avoid sudden clipping changes.
	near := d - r*2
	far := d + r*1.1
	if near < 0.01 {
		near = 0.01
	}
	if far <= near+0.01 {
		far = near + 0.01
	}
	cam.Near = near
	cam.Far = far
	cam.UpdateMatrix()
}
