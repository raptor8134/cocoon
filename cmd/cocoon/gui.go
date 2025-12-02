// Package main - GUI implementation for Cocoon.
// This file contains all the graphical user interface code using Gio.
package main

import (
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"cocoon/internal"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/font/opentype"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/oligo/gvcode"
	gvcolor "github.com/oligo/gvcode/color"
	"github.com/oligo/gvcode/textstyle/syntax"
)

//go:embed assets/fonts/DejaVuSansMono.ttf
var dejaVuSansMonoTTF []byte

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

		// Load embedded DejaVu Sans Mono font
		fontCollection, err := opentype.ParseCollection(dejaVuSansMonoTTF)
		if err != nil {
			// Fallback to system fonts if embedding fails
			panic("Failed to load embedded DejaVu Sans Mono font: " + err.Error())
		}
		shaper := text.NewShaper(text.WithCollection(fontCollection))

		// Get the font from the collection (first font face)
		var dejaVuFont font.Font
		if len(fontCollection) > 0 {
			dejaVuFont = fontCollection[0].Font
		} else {
			// Fallback if collection is empty
			dejaVuFont = font.Font{Typeface: font.Typeface("DejaVu Sans Mono")}
		}

		// GUI state
		var (
			editor           = &gvcode.Editor{}
			burgerMenuBtn    widget.Clickable
			settingsOpen     bool
			dividerPos       float32 = 0.4 // 40% from left (editor width)
			dividerTag               = new(int)
			dividerDragging          = false
			lastPointerX     float32
			lastText         string             // Track text changes for re-tokenization
			windRenderer     *internal.Renderer // Renderer for the wind object
			currentFile      string             // Current file path
			fileDialogOpen   bool               // Whether file dialog is open
			fileDialogMode   string             // "open", "save", or "saveas"
			filePathEditor   widget.Editor      // Editor for file path in dialog
			fileDialogOk     widget.Clickable   // OK button for file dialog
			fileDialogCancel widget.Clickable   // Cancel button for file dialog
			fileNameEditor   widget.Editor      // Editor for file name in header
			fileNameEditing  bool               // Whether file name is being edited
			fileNameClick    widget.Clickable   // Clickable for file name header
			openBtn          widget.Clickable   // Open button in menu
			saveBtn          widget.Clickable   // Save button in menu
			saveAsBtn        widget.Clickable   // Save As button in menu
		)

		// Create a simple color scheme for syntax highlighting
		colorScheme := &syntax.ColorScheme{}
		// Add basic styles for JSON/JSON5
		colorScheme.AddStyle("keyword", syntax.Bold, gvcolor.MakeColor(color.NRGBA{R: 0, G: 0, B: 255, A: 255}), gvcolor.MakeColor(color.NRGBA{})) // Blue keywords
		colorScheme.AddStyle("string", 0, gvcolor.MakeColor(color.NRGBA{R: 0, G: 128, B: 0, A: 255}), gvcolor.MakeColor(color.NRGBA{}))            // Green strings
		colorScheme.AddStyle("number", 0, gvcolor.MakeColor(color.NRGBA{R: 128, G: 0, B: 128, A: 255}), gvcolor.MakeColor(color.NRGBA{}))          // Purple numbers
		colorScheme.AddStyle("comment", 0, gvcolor.MakeColor(color.NRGBA{R: 128, G: 128, B: 128, A: 255}), gvcolor.MakeColor(color.NRGBA{}))       // Gray comments

		// Configure gvcode editor with options
		editor.WithOptions(
			gvcode.WithLineNumber(true),
			gvcode.WithTextSize(unit.Sp(14)),        // DejaVu Sans Mono at size 14
			gvcode.WithLineHeight(unit.Sp(18), 1.2), // Adjusted line height for size 14
			gvcode.WithColorScheme(*colorScheme),
			gvcode.WithFont(dejaVuFont),
		)
		// Try to load winds/test.json as default
		defaultFile := "winds/test.json"
		var initialText string
		if data, err := os.ReadFile(defaultFile); err == nil {
			initialText = string(data)
			editor.SetText(initialText)
			currentFile = defaultFile
			// Create initial renderer
			windRenderer = parseAndCreateRenderer(initialText)
		} else {
			// Fallback to placeholder if file doesn't exist
			initialText = "// Editor placeholder\n// G-code / JSON will go here\n"
			editor.SetText(initialText)
			currentFile = ""
		}
		lastText = editor.Text()
		filePathEditor.SetText("")
		fileNameEditor.SetText("")

		// Initial tokenization
		updateSyntaxHighlighting(editor, colorScheme)

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
					fmt.Println("[DEBUG] Burger menu button clicked")
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

				// Update gvcode editor (handles keyboard input, clipboard operations, and events)
				// The editor should automatically handle Ctrl+C/X/V with system clipboard when focused
				editor.Update(gtx)

				// Handle file name editing
				if fileNameEditing {
					fileNameEditor.Update(gtx)
					// Check if Enter was pressed (Submit is true when Enter is pressed)
					if fileNameEditor.Submit {
						fmt.Println("[DEBUG] File name editing submitted")
						newName := fileNameEditor.Text()
						if newName != "" && currentFile != "" {
							// Rename file
							dir := filepath.Dir(currentFile)
							newPath := filepath.Join(dir, newName)
							if err := os.Rename(currentFile, newPath); err == nil {
								fmt.Printf("[DEBUG] File renamed from %s to %s\n", currentFile, newPath)
								currentFile = newPath
							} else {
								fmt.Printf("[DEBUG] Failed to rename file: %v\n", err)
							}
						}
						fileNameEditing = false
						fileNameEditor.Submit = false // Reset submit flag
					}
				}

				// Check if text changed and update syntax highlighting
				currentText := editor.Text()
				if currentText != lastText {
					lastText = currentText
					updateSyntaxHighlighting(editor, colorScheme)

					// Try to parse JSON and update renderer
					windRenderer = parseAndCreateRenderer(currentText)
				}

				// Main layout: vertical (toolbar + content)
				layout.Flex{
					Axis: layout.Vertical,
				}.Layout(gtx,
					// Toolbar
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layoutToolbar(gtx, th, &burgerMenuBtn, settingsOpen, &fileDialogOpen, &fileDialogMode, &filePathEditor, &fileDialogOk, &fileDialogCancel, editor, &currentFile, &lastText, &windRenderer, &openBtn, &saveBtn, &saveAsBtn)
					}),
					// Content area: horizontal split with resizable divider
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layoutContent(gtx, th, editor, dividerPos, dividerTag, shaper, windRenderer, &currentFile, &fileNameEditor, &fileNameEditing, &fileNameClick)
					}),
				)

				// Render menu overlay if open (must be after main layout)
				if settingsOpen {
					layoutMenuOverlay(gtx, th, &settingsOpen, &fileDialogOpen, &fileDialogMode, &filePathEditor, editor, &currentFile, &lastText, &windRenderer, &openBtn, &saveBtn, &saveAsBtn)
				}

				// Render file dialog as overlay if open (must be after menu overlay)
				if fileDialogOpen {
					layoutFileDialog(gtx, th, &fileDialogMode, &filePathEditor, &fileDialogOk, &fileDialogCancel, &fileDialogOpen, editor, &currentFile, &lastText, &windRenderer)
				}

				ev.Frame(gtx.Ops)
			}
		}
	}()

	// Start Gio event loop (blocks until all windows close)
	app.Main()
}

// layoutToolbar renders the toolbar with burger menu for settings and file operations.
func layoutToolbar(gtx layout.Context, th *material.Theme, burgerBtn *widget.Clickable, settingsOpen bool, fileDialogOpen *bool, fileDialogMode *string, filePathEditor *widget.Editor, fileDialogOk *widget.Clickable, fileDialogCancel *widget.Clickable, editor *gvcode.Editor, currentFile *string, lastText *string, windRenderer **internal.Renderer, openBtn *widget.Clickable, saveBtn *widget.Clickable, saveAsBtn *widget.Clickable) layout.Dimensions {
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

		return burgerDims
	})
}

// layoutMenuOverlay renders the settings menu as an overlay above all other content.
func layoutMenuOverlay(gtx layout.Context, th *material.Theme, settingsOpen *bool, fileDialogOpen *bool, fileDialogMode *string, filePathEditor *widget.Editor, editor *gvcode.Editor, currentFile *string, lastText *string, windRenderer **internal.Renderer, openBtn *widget.Clickable, saveBtn *widget.Clickable, saveAsBtn *widget.Clickable) layout.Dimensions {
	// Menu position (below burger button, top-left)
	menuX := gtx.Dp(unit.Dp(4))
	menuY := gtx.Dp(unit.Dp(40)) // Below the burger button
	menuWidth := gtx.Dp(unit.Dp(250))
	menuHeight := gtx.Dp(unit.Dp(120)) // Approximate height for 3 buttons

	menuRect := image.Rectangle{
		Min: image.Point{X: menuX, Y: menuY},
		Max: image.Point{X: menuX + menuWidth, Y: menuY + menuHeight},
	}

	// Draw menu background with border
	bgArea := clip.Rect(menuRect).Push(gtx.Ops)
	paint.Fill(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 255}) // White background
	bgArea.Pop()

	// Draw border around menu
	borderPath := clip.Rect(menuRect).Path()
	paint.FillShape(gtx.Ops, color.NRGBA{R: 150, G: 150, B: 150, A: 255}, clip.Stroke{
		Path:  borderPath,
		Width: 1,
	}.Op())

	// Layout menu content with proper positioning
	stack := op.Offset(image.Point{X: menuX, Y: menuY}).Push(gtx.Ops)
	defer stack.Pop()

	menuGtx := gtx
	menuGtx.Constraints.Max = image.Point{X: menuWidth, Y: menuHeight}
	menuGtx.Constraints.Min = image.Point{X: menuWidth, Y: menuHeight}

	inset := layout.UniformInset(unit.Dp(4))
	return inset.Layout(menuGtx, func(gtx layout.Context) layout.Dimensions {
		// File menu items - use persistent clickables from main state
		return layout.Flex{
			Axis: layout.Vertical,
		}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				btn := material.Button(th, openBtn, "  Open...")
				if openBtn.Clicked(gtx) {
					fmt.Println("[DEBUG] Open button clicked")
					*fileDialogOpen = true
					*fileDialogMode = "open"
					*settingsOpen = false
				}
				return btn.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				btn := material.Button(th, saveBtn, "  Save")
				if saveBtn.Clicked(gtx) {
					fmt.Println("[DEBUG] Save button clicked")
					if *currentFile != "" {
						if err := os.WriteFile(*currentFile, []byte(editor.Text()), 0644); err == nil {
							fmt.Println("[DEBUG] File saved successfully")
						} else {
							fmt.Printf("[DEBUG] Failed to save file: %v\n", err)
						}
					} else {
						*fileDialogOpen = true
						*fileDialogMode = "saveas"
						*settingsOpen = false
					}
				}
				return btn.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				btn := material.Button(th, saveAsBtn, "  Save As...")
				if saveAsBtn.Clicked(gtx) {
					fmt.Println("[DEBUG] Save As button clicked")
					*fileDialogOpen = true
					*fileDialogMode = "saveas"
					if *currentFile != "" {
						filePathEditor.SetText(*currentFile)
					}
					*settingsOpen = false
				}
				return btn.Layout(gtx)
			}),
		)
	})
}

// layoutFileDialog renders a file dialog for open/save operations.
func layoutFileDialog(gtx layout.Context, th *material.Theme, mode *string, pathEditor *widget.Editor, okBtn *widget.Clickable, cancelBtn *widget.Clickable, dialogOpen *bool, editor *gvcode.Editor, currentFile *string, lastText *string, windRenderer **internal.Renderer) layout.Dimensions {
	// Draw semi-transparent overlay (less transparent for better visibility)
	overlayColor := color.NRGBA{R: 0, G: 0, B: 0, A: 200} // Increased from 128 to 200
	rect := image.Rectangle{Max: gtx.Constraints.Max}
	area := clip.Rect(rect).Push(gtx.Ops)
	paint.Fill(gtx.Ops, overlayColor)
	area.Pop()

	// Dialog size and position (centered)
	dialogWidth := gtx.Dp(unit.Dp(400))
	dialogHeight := gtx.Dp(unit.Dp(150))
	dialogX := (gtx.Constraints.Max.X - dialogWidth) / 2
	dialogY := (gtx.Constraints.Max.Y - dialogHeight) / 2

	dialogRect := image.Rectangle{
		Min: image.Point{X: dialogX, Y: dialogY},
		Max: image.Point{X: dialogX + dialogWidth, Y: dialogY + dialogHeight},
	}

	// Draw dialog background with border
	bgArea := clip.Rect(dialogRect).Push(gtx.Ops)
	paint.Fill(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	bgArea.Pop()

	// Draw border around dialog
	borderPath := clip.Rect(dialogRect).Path()
	paint.FillShape(gtx.Ops, color.NRGBA{R: 100, G: 100, B: 100, A: 255}, clip.Stroke{
		Path:  borderPath,
		Width: 2,
	}.Op())

	// Layout dialog content with proper constraints
	// Use a stack to position the dialog content correctly
	stack := op.Offset(image.Point{X: dialogX, Y: dialogY}).Push(gtx.Ops)
	defer stack.Pop()

	dialogGtx := gtx
	dialogGtx.Constraints.Max = image.Point{X: dialogWidth, Y: dialogHeight}
	dialogGtx.Constraints.Min = image.Point{X: dialogWidth, Y: dialogHeight}

	inset := layout.UniformInset(unit.Dp(16))
	return inset.Layout(dialogGtx, func(gtx layout.Context) layout.Dimensions {
		var title string
		switch *mode {
		case "open":
			title = "Open File"
		case "save", "saveas":
			title = "Save File"
		default:
			title = "File Dialog"
		}

		return layout.Flex{
			Axis: layout.Vertical,
		}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return material.H6(th, title).Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				pathEditor.SingleLine = true
				pathEditor.Submit = true
				ed := material.Editor(th, pathEditor, "File path")
				return ed.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{
					Axis:    layout.Horizontal,
					Spacing: layout.SpaceEnd,
				}.Layout(gtx,
					layout.Flexed(1, layout.Spacer{}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						cancelButton := material.Button(th, cancelBtn, "Cancel")
						cancelButton.Background = color.NRGBA{R: 200, G: 200, B: 200, A: 255} // Solid gray background
						if cancelBtn.Clicked(gtx) {
							fmt.Println("[DEBUG] Dialog Cancel button clicked")
							*dialogOpen = false
						}
						return cancelButton.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						okButton := material.Button(th, okBtn, "OK")
						okButton.Background = color.NRGBA{R: 70, G: 130, B: 180, A: 255} // Solid blue background
						if okBtn.Clicked(gtx) {
							fmt.Println("[DEBUG] Dialog OK button clicked")
							filePath := pathEditor.Text()
							if filePath != "" {
								handleFileDialogAction(mode, filePath, editor, currentFile, lastText, windRenderer)
								*dialogOpen = false
							}
						}
						return okButton.Layout(gtx)
					}),
				)
			}),
		)
	})
}

// handleFileDialogAction handles the action when OK is clicked in file dialog.
func handleFileDialogAction(mode *string, filePath string, editor *gvcode.Editor, currentFile *string, lastText *string, windRenderer **internal.Renderer) {
	switch *mode {
	case "open":
		// Open file
		if data, err := os.ReadFile(filePath); err == nil {
			content := string(data)
			editor.SetText(content)
			*currentFile = filePath
			*lastText = content
			// Update renderer
			*windRenderer = parseAndCreateRenderer(content)
		}
	case "save", "saveas":
		// Save file
		content := editor.Text()
		if err := os.WriteFile(filePath, []byte(content), 0644); err == nil {
			*currentFile = filePath
		}
	}
}

// layoutFileNameHeader renders the file name header above the editor.
func layoutFileNameHeader(gtx layout.Context, th *material.Theme, currentFile *string, fileNameEditor *widget.Editor, fileNameEditing *bool, fileNameClick *widget.Clickable) layout.Dimensions {
	headerHeight := gtx.Dp(unit.Dp(32))
	inset := layout.Inset{
		Left:   unit.Dp(8),
		Right:  unit.Dp(8),
		Top:    unit.Dp(4),
		Bottom: unit.Dp(4),
	}

	return inset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.Y = headerHeight
		gtx.Constraints.Max.Y = headerHeight

		// Draw header background
		headerRect := image.Rectangle{Max: image.Point{X: gtx.Constraints.Max.X, Y: headerHeight}}
		headerArea := clip.Rect(headerRect).Push(gtx.Ops)
		paint.Fill(gtx.Ops, color.NRGBA{R: 245, G: 245, B: 245, A: 255}) // Light gray background
		headerArea.Pop()

		// Draw bottom border
		borderRect := image.Rectangle{
			Min: image.Point{Y: headerHeight - 1},
			Max: image.Point{X: gtx.Constraints.Max.X, Y: headerHeight},
		}
		borderArea := clip.Rect(borderRect).Push(gtx.Ops)
		paint.Fill(gtx.Ops, color.NRGBA{R: 200, G: 200, B: 200, A: 255})
		borderArea.Pop()

		if *fileNameEditing {
			// Show editor for renaming
			fileNameEditor.SingleLine = true
			fileNameEditor.Submit = true
			ed := material.Editor(th, fileNameEditor, "File name")
			return ed.Layout(gtx)
		} else {
			// Show file name as clickable text
			displayName := "Untitled"
			if *currentFile != "" {
				displayName = filepath.Base(*currentFile)
			}

			// Draw clickable file name area first
			clickArea := clip.Rect(headerRect).Push(gtx.Ops)
			event.Op(gtx.Ops, fileNameClick)
			pointer.CursorPointer.Add(gtx.Ops)
			clickArea.Pop()

			// Check if clicked
			if fileNameClick.Clicked(gtx) {
				fmt.Println("[DEBUG] File name header clicked")
				*fileNameEditing = true
				if *currentFile != "" {
					fileNameEditor.SetText(filepath.Base(*currentFile))
				} else {
					fileNameEditor.SetText("")
				}
			}

			return layout.Flex{
				Axis:      layout.Horizontal,
				Alignment: layout.Middle,
			}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					label := material.Body1(th, displayName)
					label.Color = color.NRGBA{R: 50, G: 50, B: 50, A: 255}
					return label.Layout(gtx)
				}),
			)
		}
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
func layoutContent(gtx layout.Context, th *material.Theme, editor *gvcode.Editor, dividerPos float32, dividerTag *int, shaper *text.Shaper, renderer *internal.Renderer, currentFile *string, fileNameEditor *widget.Editor, fileNameEditing *bool, fileNameClick *widget.Clickable) layout.Dimensions {
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
				return layout.Flex{
					Axis: layout.Vertical,
				}.Layout(gtx,
					// File name header
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layoutFileNameHeader(gtx, th, currentFile, fileNameEditor, fileNameEditing, fileNameClick)
					}),
					// Editor content
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						inset := layout.UniformInset(unit.Dp(8))
						return inset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							// Layout gvcode editor with syntax highlighting
							// gvcode should handle Ctrl+C/X/V automatically with system clipboard when the editor is focused
							return editor.Layout(gtx, shaper)
						})
					}),
				)
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
					// Render the wind object if available
					if renderer != nil {
						return renderer.Layout(gtx)
					}
					// Show placeholder if no valid wind data
					return material.Body1(th, "Path View\n\nEnter valid JSON/JSON5 wind configuration in the editor to see the layer visualization.").Layout(gtx)
				})
			})
		}),
	)
}

// updateSyntaxHighlighting tokenizes the editor text and applies syntax highlighting.
func updateSyntaxHighlighting(editor *gvcode.Editor, colorScheme *syntax.ColorScheme) {
	text := editor.Text()
	if text == "" {
		editor.SetSyntaxTokens()
		return
	}

	// Try to detect JSON or JSON5, prefer JSON5 for comment support
	// JSON5 supports both // and /* */ style comments
	lexer := lexers.Get("json5")
	if lexer == nil {
		lexer = lexers.Get("json")
	}
	if lexer == nil {
		// Try alternative names
		lexer = lexers.Get("JSON")
		if lexer == nil {
			lexer = lexers.Get("JSON5")
		}
	}
	if lexer == nil {
		// No highlighting if we can't find a lexer
		editor.SetSyntaxTokens()
		return
	}

	// Tokenize the text
	iterator, err := lexer.Tokenise(nil, text)
	if err != nil {
		editor.SetSyntaxTokens()
		return
	}

	// Convert Chroma tokens to gvcode syntax tokens
	// Process ALL tokens to ensure nothing is missed, even empty ones
	var tokens []syntax.Token
	byteOffset := 0
	runeOffset := 0

	for _, chromaToken := range iterator.Tokens() {
		value := chromaToken.Value

		// Skip empty tokens but still advance offsets
		if value == "" {
			continue
		}

		// Map Chroma token types to our color scheme scopes
		// Also check token value as fallback for numbers (in case lexer doesn't tag them correctly)
		scope := mapChromaTypeToScope(chromaToken.Type, value)

		// Calculate rune offsets accurately
		// Count runes from the start of the text up to the current byte offset
		// This ensures we handle multi-byte UTF-8 characters correctly
		startRunes := runeOffset
		valueRunes := utf8.RuneCountInString(value)

		// Only add non-empty tokens
		if valueRunes > 0 {
			tokens = append(tokens, syntax.Token{
				Start: startRunes,
				End:   startRunes + valueRunes,
				Scope: syntax.StyleScope(scope),
			})
		}

		// Update offsets for next token
		byteOffset += len(value)
		runeOffset += valueRunes
	}

	// Set all tokens at once - this ensures proper highlighting at all depths
	editor.SetSyntaxTokens(tokens...)
}

// mapChromaTypeToScope maps Chroma token types to our color scheme scopes.
// Chroma uses hierarchical token types, so we check categories which handle all subtypes.
// Order matters: check more specific categories first.
// tokenValue is used as a fallback to detect numbers if the lexer doesn't tag them correctly.
func mapChromaTypeToScope(tokenType chroma.TokenType, tokenValue string) string {
	// Check for number literals FIRST - this is critical for JSON numbers
	// Some JSON lexers might use the base Literal category, so check that too
	if tokenType.InCategory(chroma.LiteralNumber) {
		return "number"
	}
	// Also check if it's in the base Literal category and is a number subtype
	if tokenType.InCategory(chroma.Literal) {
		// Check if it's specifically a number by checking the numeric value range
		// LiteralNumber starts at 3200, so check if token type is in that range
		if int(tokenType) >= int(chroma.LiteralNumber) && int(tokenType) < int(chroma.LiteralNumber)+100 {
			return "number"
		}
	}
	// Also check direct number type matches (in case category check fails)
	if tokenType == chroma.LiteralNumber ||
		tokenType == chroma.LiteralNumberInteger ||
		tokenType == chroma.LiteralNumberFloat ||
		tokenType == chroma.LiteralNumberHex ||
		tokenType == chroma.LiteralNumberOct ||
		tokenType == chroma.LiteralNumberBin {
		return "number"
	}

	// Fallback: if token type detection failed, check if the value looks like a number
	// This handles cases where JSON lexers don't properly tag numbers
	if isNumericValue(tokenValue) {
		return "number"
	}

	// Comments - check for both single-line (//) and multiline (/* */) comments
	// This must come before strings to catch comment tokens correctly
	if tokenType.InCategory(chroma.Comment) {
		return "comment"
	}
	// Also check direct comment type matches
	if tokenType == chroma.Comment ||
		tokenType == chroma.CommentSingle ||
		tokenType == chroma.CommentMultiline ||
		tokenType == chroma.CommentHashbang {
		return "comment"
	}

	// String literals (handles all string subtypes)
	if tokenType.InCategory(chroma.LiteralString) {
		return "string"
	}
	// Also check direct string type matches
	if tokenType == chroma.LiteralString ||
		tokenType == chroma.LiteralStringDouble ||
		tokenType == chroma.LiteralStringSingle {
		return "string"
	}

	// Keywords (handles all keyword subtypes)
	if tokenType.InCategory(chroma.Keyword) {
		return "keyword"
	}

	// Punctuation
	if tokenType.InCategory(chroma.Punctuation) {
		return "keyword" // Use keyword style for punctuation
	}

	// Operators
	if tokenType.InCategory(chroma.Operator) {
		return "keyword" // Operators like : and , use keyword style
	}

	// Names (JSON keys) - should be highlighted as strings
	if tokenType.InCategory(chroma.Name) {
		return "string"
	}

	// Default fallback for any other token types (Text, Generic, etc.)
	return "keyword"
}

// isNumericValue checks if a string value looks like a JSON number.
// This is a fallback for when the lexer doesn't properly tag numbers.
func isNumericValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	// Try to parse as float64 (handles integers, floats, scientific notation)
	_, err := strconv.ParseFloat(value, 64)
	if err == nil {
		return true
	}

	// Also check for negative numbers (might have been split by lexer)
	if strings.HasPrefix(value, "-") {
		_, err := strconv.ParseFloat(value, 64)
		if err == nil {
			return true
		}
	}

	return false
}

// parseAndCreateRenderer attempts to parse the editor text as JSON and create a renderer.
// Returns nil if parsing fails or if the text is not valid JSON.
func parseAndCreateRenderer(text string) *internal.Renderer {
	if text == "" {
		return nil
	}

	// Create a temporary file with the JSON content
	tmpDir := os.TempDir()
	tmpFile := filepath.Join(tmpDir, "cocoon_editor.json")

	// Write text to temp file
	if err := os.WriteFile(tmpFile, []byte(text), 0644); err != nil {
		return nil
	}

	// Clean up temp file when done
	defer os.Remove(tmpFile)

	// Try to parse the JSON file
	wind, err := internal.ParseWindFromJSON(tmpFile)
	if err != nil {
		return nil
	}

	// Generate paths for all layers (required for rendering)
	for i := range wind.Layers {
		layer := &wind.Layers[i]
		// Generate the full path if it doesn't exist
		if len(layer.FullPath) == 0 {
			_, err := internal.Layer2Path(wind.Mandrel, wind.Filament, layer)
			if err != nil {
				return nil // Skip rendering if path generation fails
			}
		}
	}

	// Create renderer
	return internal.NewRenderer(wind)
}
