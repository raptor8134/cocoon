package main

import (
	"image/color"
	"math"

	"cocoon/internal"
	"cogentcore.org/core/colors"
	"cogentcore.org/core/math32"
	"cogentcore.org/core/xyz"
)

// buildXYZScene constructs the 3D mandrel and path visualization in the xyz.Scene.
func buildXYZScene(state *AppState, w *internal.Wind) {
	sc := state.sc
	if sc == nil || w == nil || w.Mandrel == nil {
		return
	}

	// Replace scene contents but keep lights, meshes, etc.
	sc.DeleteChildren()

	// Mandrel solid mesh (surface of revolution around X axis).
	mandrelMesh := mandrelRevolveMesh("mandrel", w.Mandrel.XPoints, w.Mandrel.ZPoints, 96)
	sc.SetMesh(mandrelMesh)

	mandrel := xyz.NewSolid(sc).SetMesh(mandrelMesh)
	// Lighter mandrel than background so paths have good contrast.
	mandrel.Material.SetColor(colors.ToUniform(colors.Scheme.SurfaceBright))
	mandrel.Material.Shiny = 5
	mandrel.Material.Bright = 1.1

	// Paths as true 3D segments.
	// Note: xyz.Lines is designed for XY-plane polylines; feeding full 3D curves
	// can produce raster artifacts. We use a unit line mesh and pose it per segment.
	lineWidth := float32(math.Max(0.3, w.Filament.Thickness))
	lineMesh := xyz.UnitLineMesh(sc)

	for i := range w.Layers {
		layer := &w.Layers[i]
		if len(layer.FullPath) < 2 {
			continue
		}
		pathGrp := xyz.NewGroup(sc)
		pathGrp.SetName("path-" + layer.LType)

		clr := color.RGBA{R: 80, G: 140, B: 240, A: 255}
		switch layer.LType {
		case "hoop":
			clr = color.RGBA{R: 220, G: 60, B: 60, A: 255}
		case "helical":
			clr = color.RGBA{R: 60, G: 200, B: 90, A: 255}
		}

		// Build posed segment solids, subdividing large angular moves so that
		// pure angular segments follow circular arcs instead of long chords.
		const maxDeg = 5.0
		for j := 1; j < len(layer.FullPath); j++ {
			p0Cyl := layer.FullPath[j-1]
			p1Cyl := layer.FullPath[j]

			dA := p1Cyl.A - p0Cyl.A
			steps := int(math.Ceil(math.Abs(dA) / maxDeg))
			if steps < 1 {
				steps = 1
			}

			for s := 1; s <= steps; s++ {
				t0 := float64(s-1) / float64(steps)
				t1 := float64(s) / float64(steps)

				q0 := interpPoint(p0Cyl, p1Cyl, t0)
				q1 := interpPoint(p0Cyl, p1Cyl, t1)

				r0 := q0.ToRect()
				r1 := q1.ToRect()

				st := math32.Vec3(float32(r0.X), float32(r0.Y), float32(r0.Z))
				ed := math32.Vec3(float32(r1.X), float32(r1.Y), float32(r1.Z))

				seg := xyz.NewSolid(pathGrp).SetMesh(lineMesh)
				seg.Pose.Scale.Set(1, lineWidth, lineWidth)
				xyz.SetLineStartEnd(&seg.Pose, st, ed)

				// Make paths less affected by lighting: low shiny/reflective, modest emissive,
				// and slightly reduced Bright so they read clearly without harsh shadows.
				seg.Material.SetColor(clr)
				seg.Material.Shiny = 2
				seg.Material.Reflective = 0
				seg.Material.Bright = 0.6
				seg.Material.Emissive = color.RGBA{R: clr.R / 2, G: clr.G / 2, B: clr.B / 2, A: 255}
			}
		}
	}

	// Frame camera to the mandrel bounds.
	cx := float32((w.Mandrel.XMin + w.Mandrel.XMax) / 2.0)
	r := float32(w.Mandrel.ZMax)
	l := float32(w.Mandrel.Length)
	ctr := math32.Vec3(cx, 0, 0) // center of mandrel axis

	state.mandrelCenter = ctr
	state.mandrelRadius = l/2 + 20.0

	// Explicitly set orbit/pan origin to mandrel axis center.
	// xyz navigation orbits around Camera.Target.
	sc.Camera.Target = ctr
	sc.Camera.UpDir = math32.Vec3(0, 1, 0)

	// A decent default view: back a bit in Z, lift in Y, offset in -X.
	sc.Camera.Pose.Pos = ctr.Add(math32.Vec3(-0.35*l, 1.25*r, 3.0*r+0.25*l))
	sc.Camera.LookAtTarget()

	updateCameraClip(state)

	// Save the initial framing as "home" so the GUI button can restore it.
	sc.SaveCamera("home")
}

// mandrelRevolveMesh builds a surface-of-revolution mesh for the mandrel profile.
func mandrelRevolveMesh(name string, xPts, rPts []float64, radialSegs int) *xyz.GenMesh {
	if radialSegs < 8 {
		radialSegs = 8
	}
	n := len(xPts)
	if n < 2 || n != len(rPts) {
		return &xyz.GenMesh{MeshBase: xyz.MeshBase{Name: name}}
	}

	rings := n
	segs := radialSegs
	vtxCount := rings * segs
	idxCount := (rings - 1) * segs * 6

	vtx := make(math32.ArrayF32, 0, vtxCount*3)
	nrm := make(math32.ArrayF32, 0, vtxCount*3)
	uv := make(math32.ArrayF32, 0, vtxCount*2)
	idx := make(math32.ArrayU32, 0, idxCount)

	// Precompute 2D normals (x,r) from profile slope.
	nx2 := make([]float64, n)
	nr2 := make([]float64, n)
	for i := 0; i < n; i++ {
		var dx, dr float64
		if i == 0 {
			dx = xPts[1] - xPts[0]
			dr = rPts[1] - rPts[0]
		} else if i == n-1 {
			dx = xPts[n-1] - xPts[n-2]
			dr = rPts[n-1] - rPts[n-2]
		} else {
			dx = xPts[i+1] - xPts[i-1]
			dr = rPts[i+1] - rPts[i-1]
		}
		// normal in (x,r) plane is (-dr, dx)
		nx := -dr
		nr := dx
		len2 := math.Hypot(nx, nr)
		if len2 == 0 {
			nx2[i] = 0
			nr2[i] = 1
		} else {
			nx2[i] = nx / len2
			nr2[i] = nr / len2
		}
	}

	for i := 0; i < rings; i++ {
		x := float32(xPts[i])
		r := float32(rPts[i]) * 0.95 // shrink mandrel 5% so paths don't get hidden
		for j := 0; j < segs; j++ {
			ang := float32(2*math.Pi) * float32(j) / float32(segs)
			sn := float32(math.Sin(float64(ang)))
			cs := float32(math.Cos(float64(ang)))

			y := r * sn
			z := r * cs
			vtx = append(vtx, x, y, z)

			nx := float32(nx2[i])
			nr := float32(nr2[i])
			nrm = append(nrm, nx, nr*sn, nr*cs)

			u := float32(j) / float32(segs)
			v := float32(i) / float32(rings-1)
			uv = append(uv, u, v)
		}
	}

	for i := 0; i < rings-1; i++ {
		for j := 0; j < segs; j++ {
			j2 := (j + 1) % segs
			a := uint32(i*segs + j)
			b := uint32((i+1)*segs + j)
			c := uint32((i+1)*segs + j2)
			d := uint32(i*segs + j2)
			// two triangles: a-b-c and a-c-d
			idx = append(idx, a, b, c, a, c, d)
		}
	}

	return &xyz.GenMesh{
		MeshBase: xyz.MeshBase{Name: name},
		Vertex:   vtx,
		Normal:   nrm,
		TexCoord: uv,
		Index:    idx,
	}
}

// interpPoint linearly interpolates between two cylindrical Points in the internal
// coordinate system, including angle, then the result is converted to Cartesian
// via Point.ToRect when rendering. This allows the renderer to interpret large
// angular moves as circular arcs without changing the underlying path.
func interpPoint(p0, p1 internal.Point, t float64) internal.Point {
	return internal.Point{
		X: p0.X + t*(p1.X-p0.X),
		Y: p0.Y + t*(p1.Y-p0.Y),
		Z: p0.Z + t*(p1.Z-p0.Z),
		A: p0.A + t*(p1.A-p0.A),
		F: p0.F + t*(p1.F-p0.F),
	}
}
