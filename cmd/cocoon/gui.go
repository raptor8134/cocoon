// Package main - GUI implementation for Cocoon.
// This file contains all the graphical user interface code using Gio.
package main

import (
	"image"
	"image/color"
	"os"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// launchGUI starts the graphical user interface.
// Gio implementation: opens a window with toolbar, editor, and path viewer.
func launchGUI() {
	// Create a new window
	go func() {
		w := new(app.Window)
		w.Option(app.Title("Cocoon - G-code Generator"))
		w.Option(app.Size(unit.Dp(1200), unit.Dp(800)))

		var ops op.Ops
		th := material.NewTheme()

		// GUI state
		var (
			editor          widget.Editor
			burgerMenuBtn   widget.Clickable
			settingsOpen    bool
			dividerPos      float32 = 0.4 // 40% from left (editor width)
			dividerTag              = new(int)
			dividerDragging         = false
			lastPointerX    float32
		)
		editor.SingleLine = false
		editor.SetText("// Editor placeholder\n// G-code / JSON will go here\n")

		for {
			e := w.Event()
			switch ev := e.(type) {
			case app.DestroyEvent:
				// Window closed; exit program
				os.Exit(0)

			case app.FrameEvent:
				ops.Reset()
				gtx := layout.Context{
					Ops:    &ops,
					Now:    ev.Now,
					Metric: ev.Metric,
					Source: ev.Source,
					Constraints: layout.Constraints{
						Max: ev.Size,
					},
				}

				// Handle burger menu button click
				if burgerMenuBtn.Clicked(gtx) {
					settingsOpen = !settingsOpen
				}

				// Handle pointer events for divider dragging
				// Process pointer events for the divider
				for {
					evt, ok := ev.Source.Event(pointer.Filter{
						Target: dividerTag,
						Kinds:  pointer.Press | pointer.Release | pointer.Drag | pointer.Cancel,
					})
					if !ok {
						break
					}
					e := evt.(pointer.Event)
					switch e.Kind {
					case pointer.Press:
						dividerDragging = true
						lastPointerX = e.Position.X
					case pointer.Release, pointer.Cancel:
						dividerDragging = false
					case pointer.Drag:
						if dividerDragging {
							totalWidth := float32(ev.Size.X)
							if totalWidth > 0 {
								// Calculate delta from last position
								deltaX := e.Position.X - lastPointerX
								currentEditorWidth := dividerPos * totalWidth
								newEditorWidth := currentEditorWidth + deltaX
								dividerPos = newEditorWidth / totalWidth
								// Clamp between 0.2 and 0.8 (20% to 80%)
								if dividerPos < 0.2 {
									dividerPos = 0.2
								}
								if dividerPos > 0.8 {
									dividerPos = 0.8
								}
								lastPointerX = e.Position.X
							}
						}
					}
				}

				// The editor will handle its own keyboard input automatically through material.Editor

				// Main layout: vertical (toolbar + content)
				layout.Flex{
					Axis: layout.Vertical,
				}.Layout(gtx,
					// Toolbar
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layoutToolbar(gtx, th, &burgerMenuBtn, settingsOpen)
					}),
					// Content area: horizontal split with resizable divider
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layoutContent(gtx, th, &editor, dividerPos, dividerTag)
					}),
				)

				ev.Frame(gtx.Ops)
			}
		}
	}()

	// Start Gio event loop (blocks until all windows close)
	app.Main()
}

// layoutToolbar renders the toolbar with burger menu for settings.
func layoutToolbar(gtx layout.Context, th *material.Theme, burgerBtn *widget.Clickable, settingsOpen bool) layout.Dimensions {
	inset := layout.UniformInset(unit.Dp(4))
	return inset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// Burger menu button (☰ symbol)
		burgerDims := layout.Flex{
			Axis: layout.Horizontal,
		}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				btn := material.Button(th, burgerBtn, "☰")
				btn.Background = th.Palette.ContrastBg
				return btn.Layout(gtx)
			}),
		)

		// Render settings menu if open
		if settingsOpen {
			menuInset := layout.Inset{
				Top:    unit.Dp(40),
				Left:   unit.Dp(4),
				Right:  unit.Dp(0),
				Bottom: unit.Dp(0),
			}
			menuGtx := gtx
			menuGtx.Constraints.Max.X = 250 // Menu width
			menuGtx.Constraints.Min.X = 250
			menuInset.Layout(menuGtx, func(gtx layout.Context) layout.Dimensions {
				// Settings menu items (placeholder)
				return layout.Flex{
					Axis: layout.Vertical,
				}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return material.Body1(th, "  Settings").Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return material.Body1(th, "  Preferences").Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return material.Body1(th, "  About").Layout(gtx)
					}),
				)
			})
		}

		return burgerDims
	})
}

// drawBorderedRegion draws a border around a region and then lays out the content inside.
func drawBorderedRegion(gtx layout.Context, th *material.Theme, content layout.Widget) layout.Dimensions {
	borderWidthDp := unit.Dp(1)
	borderWidthPx := float32(gtx.Metric.Dp(borderWidthDp))
	borderColor := color.NRGBA{A: 200, R: 150, G: 150, B: 150} // Light gray border

	// Draw border
	borderRect := image.Rectangle{
		Min: image.Point{},
		Max: image.Point{X: gtx.Constraints.Max.X, Y: gtx.Constraints.Max.Y},
	}

	// Draw border outline using stroke
	path := clip.Rect(borderRect).Path()
	paint.FillShape(gtx.Ops, borderColor, clip.Stroke{
		Path:  path,
		Width: borderWidthPx,
	}.Op())

	// Layout content inside border with padding for the border
	inset := layout.Inset{
		Top:    unit.Dp(borderWidthDp),
		Bottom: unit.Dp(borderWidthDp),
		Left:   unit.Dp(borderWidthDp),
		Right:  unit.Dp(borderWidthDp),
	}
	return inset.Layout(gtx, content)
}

// layoutContent renders the main content area with editor (left), divider, and viewer (right).
func layoutContent(gtx layout.Context, th *material.Theme, editor *widget.Editor, dividerPos float32, dividerTag *int) layout.Dimensions {
	totalWidth := float32(gtx.Constraints.Max.X)
	dividerWidthDp := unit.Dp(4)
	dividerWidthPx := gtx.Metric.Dp(dividerWidthDp)
	editorWidth := int(dividerPos * totalWidth)
	viewerWidth := gtx.Constraints.Max.X - editorWidth - dividerWidthPx

	return layout.Flex{
		Axis: layout.Horizontal,
	}.Layout(gtx,
		// Left: Editor with border
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = editorWidth
			gtx.Constraints.Min.X = editorWidth
			return drawBorderedRegion(gtx, th, func(gtx layout.Context) layout.Dimensions {
				// Draw white background for editor
				whiteBg := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
				editorRect := image.Rectangle{
					Min: image.Point{},
					Max: image.Point{X: gtx.Constraints.Max.X, Y: gtx.Constraints.Max.Y},
				}
				area := clip.Rect(editorRect).Push(gtx.Ops)
				paint.Fill(gtx.Ops, whiteBg)
				area.Pop()

				inset := layout.UniformInset(unit.Dp(8))
				return inset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					// Create editor with black text
					editorWidget := material.Editor(th, editor, "Editor")
					editorWidget.Color = color.NRGBA{R: 0, G: 0, B: 0, A: 255} // Black text
					return editorWidget.Layout(gtx)
				})
			})
		}),
		// Divider (draggable)
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			dividerPx := dividerWidthPx
			gtx.Constraints.Max.X = dividerPx
			gtx.Constraints.Min.X = dividerPx
			gtx.Constraints.Max.Y = gtx.Constraints.Max.Y

			// Make divider draggable - expand hit area slightly for easier grabbing
			hitArea := unit.Dp(8)
			hitAreaPx := gtx.Metric.Dp(hitArea)
			dividerRect := image.Rectangle{
				Min: image.Point{X: -hitAreaPx / 2},
				Max: image.Point{X: dividerPx + hitAreaPx/2, Y: gtx.Constraints.Max.Y},
			}
			area := clip.Rect(dividerRect).Push(gtx.Ops)
			event.Op(gtx.Ops, dividerTag)
			pointer.CursorColResize.Add(gtx.Ops)
			area.Pop()

			return layout.Dimensions{
				Size: image.Point{X: dividerPx, Y: gtx.Constraints.Max.Y},
			}
		}),
		// Right: Path rendering view with border
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = viewerWidth
			return drawBorderedRegion(gtx, th, func(gtx layout.Context) layout.Dimensions {
				inset := layout.UniformInset(unit.Dp(8))
				return inset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					// TODO: Replace with actual path rendering
					return material.H3(th, "Path View (placeholder)\n\n3D path rendering will go here").Layout(gtx)
				})
			})
		}),
	)
}
