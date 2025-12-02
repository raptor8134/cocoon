// Package internal - 3D rendering widget for Wind objects.
// This file provides a Gio widget that renders wind layers in 3D with rotation and zoom controls.
package internal

import (
	"image"
	"image/color"
	"math"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// Renderer holds the state for rendering a Wind object.
type Renderer struct {
	wind        *Wind
	scale       float32   // Base scale (pixels per mm)
	zoom        float32   // Zoom level (multiplier)
	rotationX   float32   // Rotation around X axis (pitch) in radians
	rotationY   float32   // Rotation around Y axis (yaw) in radians
	center      f32.Point // Center of the viewport
	rotating    bool      // Whether we're currently rotating
	lastPointer f32.Point // Last pointer position for rotation
	tag         *int      // Event tag for pointer events
}

// NewRenderer creates a new renderer for the given Wind object.
func NewRenderer(wind *Wind) *Renderer {
	return &Renderer{
		wind:      wind,
		scale:     1.0, // 1 pixel per mm
		zoom:      1.0, // Start at 1x zoom
		rotationX: 0.0, // Start with no rotation
		rotationY: 0.0,
		tag:       new(int), // Unique tag for event handling
	}
}

// Layout renders the wind layers in a 3D view with rotation and zoom.
// This is a Gio layout widget that can be used in a layout.Flex or similar.
func (r *Renderer) Layout(gtx layout.Context) layout.Dimensions {
	if r.wind == nil || len(r.wind.Layers) == 0 {
		// Render placeholder if no wind data
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}

	// Update center to be the middle of the viewport
	r.center = f32.Point{
		X: float32(gtx.Constraints.Max.X) / 2,
		Y: float32(gtx.Constraints.Max.Y) / 2,
	}

	// Handle pointer events for rotation (right-click drag)
	r.handlePointerEvents(gtx)

	// Clip all drawing to viewport
	clipRect := image.Rectangle{Max: gtx.Constraints.Max}
	clipArea := clip.Rect(clipRect).Push(gtx.Ops)
	defer clipArea.Pop()

	// Draw background
	r.drawBackground(gtx)

	// Calculate bounds for scaling
	maxX, maxZ, maxY := r.getMaxBounds()

	// Calculate effective scale with zoom
	effectiveScale := r.scale * r.zoom

	// Draw all layers in 3D
	for i, layer := range r.wind.Layers {
		if len(layer.FullPath) == 0 {
			continue
		}
		r.drawLayer3D(gtx, &layer, i, effectiveScale, maxX, maxZ, maxY)
	}

	return layout.Dimensions{Size: gtx.Constraints.Max}
}

// getMaxBounds calculates the maximum X, Z, and Y bounds across all layers.
func (r *Renderer) getMaxBounds() (maxX, maxZ, maxY float64) {
	for _, layer := range r.wind.Layers {
		for _, point := range layer.FullPath {
			if point.X > maxX {
				maxX = point.X
			}
			if point.Z > maxZ {
				maxZ = point.Z
			}
			// Y is the layer index, we'll use the number of layers
		}
	}
	maxY = float64(len(r.wind.Layers))
	return maxX, maxZ, maxY
}

// handlePointerEvents processes pointer events for rotation and scroll for zoom.
func (r *Renderer) handlePointerEvents(gtx layout.Context) {
	// Set up pointer event area
	rect := image.Rectangle{Max: gtx.Constraints.Max}
	area := clip.Rect(rect).Push(gtx.Ops)
	event.Op(gtx.Ops, r.tag)
	pointer.CursorGrab.Add(gtx.Ops)
	area.Pop()

	// Process pointer events
	scrollRange := pointer.ScrollRange{Min: math.MinInt32, Max: math.MaxInt32}

	for {
		evt, ok := gtx.Source.Event(pointer.Filter{
			Target:  r.tag,
			Kinds:   pointer.Press | pointer.Release | pointer.Drag | pointer.Scroll,
			ScrollX: scrollRange,
			ScrollY: scrollRange,
		})
		if !ok {
			break
		}

		e := evt.(pointer.Event)
		switch e.Kind {
		case pointer.Press:
			// Right-click (button 2) starts rotation
			if e.Buttons == pointer.ButtonSecondary {
				r.rotating = true
				r.lastPointer = e.Position
			}
		case pointer.Release:
			r.rotating = false
		case pointer.Drag:
			if r.rotating && e.Buttons == pointer.ButtonSecondary {
				// Calculate rotation delta
				deltaX := e.Position.X - r.lastPointer.X
				deltaY := e.Position.Y - r.lastPointer.Y

				// Apply rotation (sensitivity factor)
				sensitivity := float32(0.01)
				r.rotationY += deltaX * sensitivity
				r.rotationX += deltaY * sensitivity

				// Clamp X rotation to prevent flipping
				if r.rotationX > math.Pi/2 {
					r.rotationX = math.Pi / 2
				}
				if r.rotationX < -math.Pi/2 {
					r.rotationX = -math.Pi / 2
				}

				r.lastPointer = e.Position
			}
		case pointer.Scroll:
			// Zoom with scroll
			zoomSpeed := float32(0.1)
			if e.Scroll.Y > 0 {
				r.zoom *= (1.0 + zoomSpeed)
			} else if e.Scroll.Y < 0 {
				r.zoom *= (1.0 - zoomSpeed)
			}
			// Clamp zoom
			if r.zoom < 0.1 {
				r.zoom = 0.1
			}
			if r.zoom > 10.0 {
				r.zoom = 10.0
			}
		}
	}
}

// cylindricalTo3D converts cylindrical coordinates (X, Z, A) to 3D Cartesian (x, y, z).
// X is axial position, Z is radius, A is angle in degrees.
func cylindricalTo3D(p Point) (x, y, z float32) {
	angleRad := float32(p.A) * math.Pi / 180.0
	x = float32(p.X)
	y = float32(p.Z) * float32(math.Cos(float64(angleRad)))
	z = float32(p.Z) * float32(math.Sin(float64(angleRad)))
	return x, y, z
}

// rotate3D applies rotation around X and Y axes to a 3D point.
func rotate3D(x, y, z, rotX, rotY float32) (xOut, yOut, zOut float32) {
	// Rotate around Y axis (yaw)
	cosY := float32(math.Cos(float64(rotY)))
	sinY := float32(math.Sin(float64(rotY)))
	x1 := x*cosY + z*sinY
	z1 := -x*sinY + z*cosY

	// Rotate around X axis (pitch)
	cosX := float32(math.Cos(float64(rotX)))
	sinX := float32(math.Sin(float64(rotX)))
	yOut = y*cosX - z1*sinX
	zOut = y*sinX + z1*cosX
	xOut = x1

	return xOut, yOut, zOut
}

// project3DTo2D projects a 3D point to 2D screen coordinates (orthographic projection).
func (r *Renderer) project3DTo2D(x, y, z, scale float32) f32.Point {
	// Apply scale
	x *= scale
	y *= scale
	z *= scale

	// Project to 2D (orthographic - just drop Z)
	return f32.Point{
		X: r.center.X + x,
		Y: r.center.Y - y, // Flip Y axis (screen Y increases downward)
	}
}

// drawLayer3D draws a single layer in 3D space with rotation.
func (r *Renderer) drawLayer3D(gtx layout.Context, layer *Layer, layerIndex int, scale float32, maxX, maxZ, maxY float64) {
	if len(layer.FullPath) < 2 {
		return
	}

	// Calculate layer Y position (stack layers vertically in 3D space)
	layerY3D := float32(layerIndex) * float32(maxY) / float32(len(r.wind.Layers))

	// Get color for this layer
	pathColor := getLayerColor(layer.LType)

	// Draw path segments
	for i := 0; i < len(layer.FullPath)-1; i++ {
		p1 := layer.FullPath[i]
		p2 := layer.FullPath[i+1]

		segments := interpolatePointsByAngle(p1, p2, 5.0)
		if len(segments) < 2 {
			continue
		}

		for j := 0; j < len(segments)-1; j++ {
			s1 := segments[j]
			s2 := segments[j+1]

			// Convert cylindrical to 3D
			x1, y1, z1 := cylindricalTo3D(s1)
			x2, y2, z2 := cylindricalTo3D(s2)

			// Offset by layer Y position
			y1 += layerY3D
			y2 += layerY3D

			// Center the model (subtract center of bounds)
			centerX := float32(maxX) / 2
			x1 -= centerX
			x2 -= centerX

			// Apply rotation
			x1r, y1r, z1r := rotate3D(x1, y1, z1, r.rotationX, r.rotationY)
			x2r, y2r, z2r := rotate3D(x2, y2, z2, r.rotationX, r.rotationY)

			// Project to 2D
			pt1 := r.project3DTo2D(x1r, y1r, z1r, scale)
			pt2 := r.project3DTo2D(x2r, y2r, z2r, scale)

			// Draw line segment with layer-specific color
			r.drawLine(gtx, pt1.X, pt1.Y, pt2.X, pt2.Y, pathColor, 1.5)
		}
	}
}

// drawBackground draws the background of the render area.
func (r *Renderer) drawBackground(gtx layout.Context) {
	bgColor := color.NRGBA{R: 240, G: 240, B: 240, A: 255}
	rect := image.Rectangle{Max: gtx.Constraints.Max}
	area := clip.Rect(rect).Push(gtx.Ops)
	paint.Fill(gtx.Ops, bgColor)
	area.Pop()
}

// drawLine draws a line segment between two points.
func (r *Renderer) drawLine(gtx layout.Context, x1, y1, x2, y2 float32, col color.NRGBA, width float32) {
	// Create a path for the line
	path := clip.Path{}
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Point{X: x1, Y: y1})
	path.LineTo(f32.Point{X: x2, Y: y2})

	// Stroke the path
	paint.FillShape(gtx.Ops, col, clip.Stroke{
		Path:  path.End(),
		Width: width,
	}.Op())
}

// getLayerColor returns a color based on layer type.
func getLayerColor(layerType string) color.NRGBA {
	switch layerType {
	case "hoop":
		return color.NRGBA{R: 0, G: 100, B: 200, A: 255} // Blue for hoop
	case "helical":
		return color.NRGBA{R: 200, G: 100, B: 0, A: 255} // Orange for helical
	default:
		return color.NRGBA{R: 100, G: 100, B: 100, A: 255} // Gray for unknown
	}
}

// interpolatePointsByAngle inserts additional points between p1 and p2 so that
// the angular difference between successive points is at most maxStepDeg.
func interpolatePointsByAngle(p1, p2 Point, maxStepDeg float64) []Point {
	if maxStepDeg <= 0 {
		return []Point{p1, p2}
	}

	delta := math.Abs(p2.A - p1.A)
	if delta <= maxStepDeg {
		return []Point{p1, p2}
	}

	steps := int(math.Ceil(delta / maxStepDeg))
	if steps < 1 {
		steps = 1
	}

	points := make([]Point, 0, steps+1)
	points = append(points, p1)

	for i := 1; i < steps; i++ {
		t := float64(i) / float64(steps)
		points = append(points, lerpPoint(p1, p2, t))
	}

	points = append(points, p2)
	return points
}

// lerpPoint linearly interpolates between two points.
func lerpPoint(p1, p2 Point, t float64) Point {
	return Point{
		X: (1-t)*p1.X + t*p2.X,
		Y: (1-t)*p1.Y + t*p2.Y,
		Z: (1-t)*p1.Z + t*p2.Z,
		A: (1-t)*p1.A + t*p2.A,
		F: (1-t)*p1.F + t*p2.F,
	}
}
