package main

import (
	"github.com/goki/gi/gi"
	"github.com/goki/gi/gimain"
	"github.com/goki/gi/giv"
	"github.com/goki/gi/oswin"
	"github.com/goki/gi/oswin/driver/vkos"
	"github.com/goki/gi/oswin/mouse"
	"github.com/goki/gi/units"
	"github.com/goki/ki/ki"
)

// AppState holds minimal GUI state for the Gi-based placeholder.
type AppState struct {
	Buf *giv.TextBuf
}

// launchGUI starts the graphical user interface using Gi.
func launchGUI() {
	// Ensure the Vulkan/GLFW driver is registered.
	vkos.VkOsDebug = false

	gimain.Main(func() {
		mainrunGUI()
	})
}

// mainrunGUI creates a simple two-pane window: editor on the left, placeholder on the right.
func mainrunGUI() {
	const width = 1200
	const height = 800

	gi.SetAppName("cocoon")
	gi.SetAppAbout(`Cocoon - CNC Operated COmposite Overwrap Navigator`)

	win := gi.NewMainWindow("cocoon", "Cocoon - G-code Generator", width, height)

	vp := win.WinViewport2D()
	updt := vp.UpdateStart()

	mfr := win.SetMainFrame()
	mfr.SetProp("spacing", units.NewEx(1))
	mfr.SetStretchMax()

	// Top title label.
	title := gi.AddNewLabel(mfr, "title", "Cocoon - G-code Generator (Gi placeholder GUI)")
	title.SetProp("font-size", "x-large")
	title.SetProp("margin", units.NewEx(0.5))

	// Horizontal split: left editor, draggable divider, right placeholder viewer.
	row := gi.AddNewLayout(mfr, "main-row", gi.LayoutHoriz)
	row.SetStretchMax()

	// Editor and viewer panels with a narrow divider in between.
	edFrame := gi.AddNewFrame(row, "editor-frame", gi.LayoutVert)
	edFrame.SetStretchMaxHeight()

	divFrame := gi.AddNewFrame(row, "divider-frame", gi.LayoutVert)
	divFrame.SetMinPrefWidth(units.NewPx(4))
	divFrame.SetProp("background-color", "gray")

	viewFrame := gi.AddNewFrame(row, "view-frame", gi.LayoutVert)
	viewFrame.SetStretchMax()

	state := &AppState{}

	// Initial editor width: 40% of total; enforced via MinPrefWidth during drag.
	// Actual px width is computed from mouse position at drag time.
	var dragging bool

	divFrame.ConnectEvent(oswin.MouseEvent, gi.LowPri, func(recv, send ki.Ki, sig int64, d any) {
		me := d.(*mouse.Event)
		switch me.Action {
		case mouse.Press:
			dragging = true
		case mouse.Release:
			dragging = false
		case mouse.Drag:
			if !dragging {
				return
			}
			// Compute desired left width from mouse X relative to the row layout.
		rowLay := row
		total := float32(rowLay.ObjBBox.Size().X)
			if total <= 0 {
				return
			}
			leftPx := float32(me.Where.X - rowLay.ObjBBox.Min.X)

			// Constrain divider so panes stay within [20%, 80%] of total width.
			minPx := 0.2 * total
			maxPx := 0.8 * total
			if leftPx < minPx {
				leftPx = minPx
			}
			if leftPx > maxPx {
				leftPx = maxPx
			}

			edFrame.SetMinPrefWidth(units.NewPx(leftPx))
		}
	})

	setupEditor(edFrame, state)
	setupViewer(viewFrame)

	vp.UpdateEndNoSig(updt)
	win.StartEventLoop()
}

// setupEditor creates a simple text editor for JSON configuration on the left pane.
func setupEditor(parent ki.Ki, state *AppState) {
	gi.AddNewLabel(parent, "editor-label", "Editor (JSON configuration)")

	// TextView with its own layout (scrollbars etc).
	tv, tvLay := giv.AddNewTextViewLayout(parent, "editor")
	tvLay.SetStretchMax()

	buf := giv.NewTextBuf()
	// Hint syntax highlighting to use JSON rules.
	buf.Filename = "editor.json"
	buf.SetText([]byte("// JSON/JSON5 configuration goes here\n"))
	tv.SetBuf(buf)

	state.Buf = buf
}

// setupViewer creates a placeholder right-hand pane.
func setupViewer(parent ki.Ki) {
	gi.AddNewLabel(parent, "viewer-label",
		"Path View placeholder\n\nA Gi/gi3d 3D visualization will be added here.")
}

