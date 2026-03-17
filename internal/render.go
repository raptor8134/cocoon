package internal

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"cogentcore.org/core/math32"
	"cogentcore.org/core/xyz"
)

// RenderStats captures coarse-grained metrics about a single scene rebuild.
// It is intentionally simple and only tracks data we can reliably observe
// at this layer; low-level WebGPU draw-call or buffer stats live in the
// underlying Cogent Core / xyz renderer and are not exposed here.
type RenderStats struct {
	// Layers is the number of logical wind layers that contributed
	// at least one path segment to the scene.
	Layers int

	// Segments is the total number of posed line segments created for
	// all layers in the current scene.
	Segments int

	// BuildMillis is the wall-clock time spent inside BuildXYZScene
	// for the last rebuild, in milliseconds.
	BuildMillis float64

	// MandrelRebuilt is true if the mandrel mesh/solid was regenerated during
	// this BuildXYZScene call. When false, the previous mandrel content was reused.
	MandrelRebuilt bool
}

// BuildXYZScene constructs the 3D mandrel and path visualization in the xyz.Scene.
// It returns the mandrel center and radius used for camera clipping, along with
// coarse-grained render statistics for instrumentation/diagnostics.
func BuildXYZScene(sc *xyz.Scene, w *Wind) (mandrelCenter math32.Vector3, mandrelRadius float32, stats RenderStats) {
	if sc == nil || w == nil || w.Mandrel == nil {
		return
	}

	start := time.Now()
	// start is used for BuildMillis timing below.

	// Create or reuse the 3-space coordinate system:
	// - orbitRoot (second LCS): origin fixed at global (0,0,0); we rotate/scale
	//   this node for camera navigation in global space.
	// - shiftRoot (third LCS): translates winder space so the center of rotation
	//   coincides with orbitRoot/global origin.
	// - winderRoot (first LCS): true winder space coordinates for all geometry.
	var orbitRoot *xyz.Group
	if nd := sc.ChildByName("orbitRoot", 0); nd != nil {
		if g, ok := nd.(*xyz.Group); ok {
			orbitRoot = g
		}
	}
	if orbitRoot == nil {
		orbitRoot = xyz.NewGroup(sc)
		orbitRoot.SetName("orbitRoot")
		orbitRoot.Pose.SetIdentity()
	}
	// Ensure a shiftRoot exists under orbitRoot.
	var shiftRoot *xyz.Group
	if nd := orbitRoot.AsTree().ChildByName("shiftRoot", 0); nd != nil {
		if g, ok := nd.(*xyz.Group); ok {
			shiftRoot = g
		}
	}
	if shiftRoot == nil {
		shiftRoot = xyz.NewGroup(orbitRoot)
		shiftRoot.SetName("shiftRoot")
		shiftRoot.Pose.SetIdentity()
	}
	// Ensure a child winderRoot exists under shiftRoot to hold all geometry in
	// true winder coordinates (unshifted).
	var winderRoot *xyz.Group
	if nd := shiftRoot.AsTree().ChildByName("winderRoot", 0); nd != nil {
		if g, ok := nd.(*xyz.Group); ok {
			winderRoot = g
		}
	}
	if winderRoot == nil {
		winderRoot = xyz.NewGroup(shiftRoot)
		winderRoot.SetName("winderRoot")
		winderRoot.Pose.SetIdentity()
	}
	// winderRoot is true winder space; keep it identity and drive the
	// translation via shiftRoot.
	winderRoot.Pose.SetIdentity()

	// Always rebuild helper visualization content we own inside winderRoot
	// while leaving viewer-owned infrastructure such as lights intact.
	// Replace only the dynamic scene content we own, without destroying
	// lights or other scene infrastructure created by the viewer.
	//
	// We intentionally *reuse* the mandrel mesh/solid when it hasn't changed,
	// because it is relatively expensive and doesn't need rebuilding for
	// every editor keystroke if only layers/path params are changing.
	winderRoot.DeleteChildByName("pathsRoot")
	winderRoot.DeleteChildByName("mandrelSolid")
	winderRoot.DeleteChildByName("mandrelShell")

	// Mandrel visualization: render as a solid of revolution using the
	// mandrel profile, with an optional shell line cage for bounds.
	if w.Mandrel != nil && len(w.Mandrel.XPoints) > 1 {
		// Build or update the mandrel mesh in the scene's mesh library.
		const mandrelMeshName = "mandrel-solid-mesh"
		// keylist.List provides At / Set; this keeps meshes keyed by name.
		mmesh, ok := sc.Meshes.AtTry(mandrelMeshName)
		if !ok {
			// First-time creation.
			mmesh = mandrelRevolveMesh(mandrelMeshName, w.Mandrel.XPoints, w.Mandrel.ZPoints, 128)
			// Use Scene.SetMesh so live GPU renderer gets the update.
			sc.SetMesh(mmesh.(xyz.Mesh))
			stats.MandrelRebuilt = true
		} else {
			// Replace existing mesh contents in-place so any solids referencing
			// it automatically receive the updated geometry.
			nmesh := mandrelRevolveMesh(mandrelMeshName, w.Mandrel.XPoints, w.Mandrel.ZPoints, 128)
			if gm, ok2 := mmesh.(*xyz.GenMesh); ok2 {
				// mandrelRevolveMesh already returns *xyz.GenMesh; no
				// interface assertion needed here.
				nm := nmesh
				gm.Vertex = nm.Vertex
				gm.Normal = nm.Normal
				gm.TexCoord = nm.TexCoord
				gm.Index = nm.Index
				// Notify live renderer that this mesh changed.
				sc.SetMesh(gm)
				stats.MandrelRebuilt = true
			}
		}

		if mmesh != nil {
			mandrelSolid := xyz.NewSolid(winderRoot)
			mandrelSolid.SetName("mandrelSolid")
			mandrelSolid.SetMesh(mmesh)
			// Neutral gray, slightly bright, with modest shininess.
			mandrelSolid.Material.SetColor(color.RGBA{R: 150, G: 150, B: 150, A: 255})
			mandrelSolid.Material.Shiny = 20
			mandrelSolid.Material.Reflective = 2.5
			mandrelSolid.Material.Bright = 1.0
		}
	}

	// Winder-space axes at the winder origin.
	drawWinderAxes(sc, w, winderRoot)

	pathsRoot := xyz.NewGroup(winderRoot)
	pathsRoot.SetName("pathsRoot")

	// Paths rendered as batched ribbon meshes (one solid per layer) instead of
	// camera-facing lines. This gives each path a constant world-space width /
	// thickness and lets us align normals with the mandrel surface for stable
	// shading. We use a fixed cross-section of 1 (width) × 0.1 (depth) in
	// winder units.
	const pathWidth = float32(1.0)
	const pathDepth = float32(0.1)

	// Simple LOD heuristic: when the camera is far away, reduce angular subdivision
	// to keep point counts manageable for very dense winds.
	camDist := sc.Camera.DistanceTo(sc.Camera.Target)
	approxRadius := float32(w.Mandrel.Length/2.0 + w.Mandrel.ZMax + 1.0)
	baseMaxDeg := 5.0
	switch {
	case camDist > 0 && approxRadius > 0 && camDist > 10*approxRadius:
		baseMaxDeg = 30.0
	case camDist > 0 && approxRadius > 0 && camDist > 6*approxRadius:
		baseMaxDeg = 18.0
	case camDist > 0 && approxRadius > 0 && camDist > 3*approxRadius:
		baseMaxDeg = 10.0
	}

	for i := range w.Layers {
		layer := &w.Layers[i]
		if len(layer.FullPath) < 2 {
			continue
		}
		stats.Layers++

		clr := color.RGBA{R: 80, G: 140, B: 240, A: 255}
		switch layer.LType {
		case "hoop":
			clr = color.RGBA{R: 220, G: 60, B: 60, A: 255}
		case "helical":
			// Darker green so it is visually distinct from the Y axis helper.
			clr = color.RGBA{R: 40, G: 130, B: 70, A: 255}
		}

		// Subdivide large angular moves so that pure angular segments follow
		// circular arcs instead of long chords. Use a camera-distance-based
		// LOD to avoid excessive point counts at far zoom.
		maxDeg := baseMaxDeg
		if len(layer.FullPath) > 50_000 {
			// Hard cap for extreme cases.
			maxDeg = math.Max(maxDeg, 45.0)
		} else if len(layer.FullPath) > 10_000 {
			maxDeg = math.Max(maxDeg, 25.0)
		}

		// Expand cylindrical path into a polyline of Cartesian points, inserting
		// intermediate points for large angular steps to better follow arcs.
		// We estimate point count up front to reduce allocations for large paths.
		extraPts := 0
		for j := 1; j < len(layer.FullPath); j++ {
			dA := layer.FullPath[j].A - layer.FullPath[j-1].A
			steps := int(math.Ceil(math.Abs(dA) / maxDeg))
			if steps < 1 {
				steps = 1
			}
			extraPts += (steps - 1)
		}
		points := make([]math32.Vector3, 0, len(layer.FullPath)+extraPts)
		{
			r0 := layer.FullPath[0].ToRect()
			points = append(points, math32.Vec3(float32(r0.X), float32(r0.Y), float32(r0.Z)))
		}

		for j := 1; j < len(layer.FullPath); j++ {
			p0Cyl := layer.FullPath[j-1]
			p1Cyl := layer.FullPath[j]

			dA := p1Cyl.A - p0Cyl.A
			steps := int(math.Ceil(math.Abs(dA) / maxDeg))
			if steps < 1 {
				steps = 1
			}

			for s := 1; s <= steps; s++ {
				t1 := float64(s) / float64(steps)

				q1 := interpPoint(p0Cyl, p1Cyl, t1)

				r1 := q1.ToRect()
				points = append(points, math32.Vec3(float32(r1.X), float32(r1.Y), float32(r1.Z)))
			}
		}

		stats.Segments += max(0, len(points)-1)

		meshName := fmt.Sprintf("pathmesh-%s-%d", layer.LType, i)
		// Reuse and update a single mesh object per layer name so the renderer
		// can update GPU buffers in-place (same strategy as the mandrel mesh).
		pathMesh := buildPathRibbonMesh(sc, meshName, points, pathWidth, pathDepth)

		pathSolid := xyz.NewSolid(pathsRoot).SetMesh(pathMesh)
		pathSolid.SetName(fmt.Sprintf("path-%s-%d", layer.LType, i))
		pathSolid.Material.SetColor(clr)
		// Restore simple lit material for paths; depth cues come mainly
		// from the mandrel, but paths remain clearly visible and colored.
		pathSolid.Material.Shiny = 2
		pathSolid.Material.Reflective = 0
		pathSolid.Material.Bright = 0.6
		pathSolid.Material.Emissive = color.RGBA{R: clr.R / 2, G: clr.G / 2, B: clr.B / 2, A: 255}
	}

	// Frame camera to the mandrel bounds.
	cx := float32((w.Mandrel.XMin + w.Mandrel.XMax) / 2.0)
	l := float32(w.Mandrel.Length)
	ctr := math32.Vec3(cx, 0, 0) // center of mandrel axis in winder space

	// Position the shift coordinate system so that the global/orbit origin
	// coincides with the mandrel axis center. The orbitRoot itself stays
	// fixed at the global origin; shiftRoot translates the entire winder space.
	shiftRoot.Pose.Pos = ctr.Negate() // shift mandrel center to (0,0,0)

	// From the global point of view the mandrel center is now at the origin.
	mandrelCenter = math32.Vec3(0, 0, 0)
	mandrelRadius = l/2 + 20.0

	// Explicitly set orbit/pan origin to the global origin, which now
	// coincides with the mandrel axis center.
	// xyz navigation orbits around Camera.Target.
	sc.Camera.Target = mandrelCenter
	sc.Camera.UpDir = math32.Vec3(0, 1, 0)

	// Default to a top-down view along +Y, placed at (0, 2*mandrel_length, 0)
	// in the global coordinate system, looking at the origin.
	yDist := 2 * l
	if yDist <= 0 {
		yDist = 100
	}
	sc.Camera.Pose.Pos = math32.Vec3(0, yDist, 0)
	sc.Camera.LookAtTarget()

	// Save the initial framing as "home" so the GUI button can restore it.
	sc.SaveCamera("home")

	// Commit graph + mesh edits so the renderer sees updates.
	// Without this, subsequent rebuilds can mutate mesh data and recreate nodes
	// but the render backend may continue using stale GPU buffers.
	sc.Update()

	stats.BuildMillis = float64(time.Since(start).Microseconds()) / 1000.0

	return
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
			// two triangles (winding flipped to match FrontFaceCW defaults):
			// a-c-b and a-d-c
			idx = append(idx, a, c, b, a, d, c)
		}
	}

	// Caps: close the ends so the inside isn't visible when back-face culling is enabled.
	// We duplicate the rim vertices for each cap so they can have correct cap normals.
	//
	// Start cap (ring 0): normal points toward -X.
	startRimBase := uint32(len(vtx) / 3)
	for j := 0; j < segs; j++ {
		src := (0*segs + j) * 3
		vtx = append(vtx, vtx[src], vtx[src+1], vtx[src+2])
		nrm = append(nrm, -1, 0, 0)
		uv = append(uv, 0, 0)
	}
	startCenter := uint32(len(vtx) / 3)
	vtx = append(vtx, float32(xPts[0]), 0, 0)
	nrm = append(nrm, -1, 0, 0)
	uv = append(uv, 0, 0)
	for j := 0; j < segs; j++ {
		j2 := (j + 1) % segs
		a := startRimBase + uint32(j)
		b := startRimBase + uint32(j2)
		// Winding chosen for outward-facing cap with normal -X.
		idx = append(idx, startCenter, a, b)
	}

	// End cap (last ring): normal points toward +X.
	endRimBase := uint32(len(vtx) / 3)
	lastRing := (rings - 1)
	for j := 0; j < segs; j++ {
		src := (lastRing*segs + j) * 3
		vtx = append(vtx, vtx[src], vtx[src+1], vtx[src+2])
		nrm = append(nrm, 1, 0, 0)
		uv = append(uv, 0, 0)
	}
	endCenter := uint32(len(vtx) / 3)
	vtx = append(vtx, float32(xPts[rings-1]), 0, 0)
	nrm = append(nrm, 1, 0, 0)
	uv = append(uv, 0, 0)
	for j := 0; j < segs; j++ {
		j2 := (j + 1) % segs
		a := endRimBase + uint32(j)
		b := endRimBase + uint32(j2)
		// Winding chosen for outward-facing cap with normal +X.
		idx = append(idx, endCenter, b, a)
	}

	// Ensure shading normals always point outward from the mandrel axis.
	// In YZ (radial) plane, flip any normal whose direction disagrees with
	// the vertex's radial direction so the dot product is non-negative.
	for i := 0; i+2 < len(vtx) && i+2 < len(nrm); i += 3 {
		vy, vz := vtx[i+1], vtx[i+2]
		ny, nz := nrm[i+1], nrm[i+2]
		if vy*ny+vz*nz < 0 {
			nrm[i] = -nrm[i]
			nrm[i+1] = -nrm[i+1]
			nrm[i+2] = -nrm[i+2]
		}
	}

	// Debug printing of sample positions / normals intentionally disabled.

	return &xyz.GenMesh{
		MeshBase: xyz.MeshBase{Name: name},
		Vertex:   vtx,
		Normal:   nrm,
		TexCoord: uv,
		Index:    idx,
	}
}

// buildPathRibbonMesh constructs an independent ribbon-like mesh for a single
// layer path. The mesh is not registered in the scene's mesh library; it is
// owned directly by the Solid that uses it, avoiding any shared-mesh/staleness
// issues. The input points are Cartesian winder-space positions on the mandrel
// surface. We generate a constant-width / constant-thickness tube whose wide
// faces have normals aligned with the local mandrel normal, i.e. the radial
// direction in the YZ plane from the mandrel axis (+X).
func buildPathRibbonMesh(sc *xyz.Scene, name string, pts []math32.Vector3, width, depth float32) *xyz.GenMesh {
	// Reuse an existing mesh object for this name when possible so the renderer
	// can update GPU buffers in-place, mirroring mandrelRevolveMesh.
	var gm *xyz.GenMesh
	if sc != nil {
		if m, ok := sc.Meshes.AtTry(name); ok {
			if g, ok2 := m.(*xyz.GenMesh); ok2 {
				gm = g
			}
		}
	}
	if gm == nil {
		gm = &xyz.GenMesh{MeshBase: xyz.MeshBase{Name: name}}
		if sc != nil {
			// Use Scene.SetMesh so live GPU renderer gets the update.
			sc.SetMesh(gm)
		}
	}

	if len(pts) < 2 {
		// Ensure stale geometry is cleared if a layer becomes empty.
		gm.Vertex = gm.Vertex[:0]
		gm.Normal = gm.Normal[:0]
		gm.TexCoord = gm.TexCoord[:0]
		gm.Index = gm.Index[:0]
		// Notify live renderer that this mesh changed.
		if sc != nil {
			sc.SetMesh(gm)
		}
		return gm
	}

	halfW := width / 2
	halfD := depth / 2

	nPts := len(pts)

	vtx := make(math32.ArrayF32, 0, nPts*4*3)
	nrm := make(math32.ArrayF32, 0, nPts*4*3)
	uv := make(math32.ArrayF32, 0, nPts*4*2)
	idx := make(math32.ArrayU32, 0, (nPts-1)*4*6)

	// Per-point frame: radial normal and binormal around the mandrel.
	type frame struct {
		rad math32.Vector3
		bin math32.Vector3
	}
	frames := make([]frame, nPts)

	for i := 0; i < nPts; i++ {
		p := pts[i]

		// Radial direction from mandrel axis (+X): project onto YZ plane.
		r := math32.Vec3(0, p.Y, p.Z)
		if r.Length() == 0 {
			// Fallback to +Z if we somehow land exactly on the axis.
			r = math32.Vec3(0, 0, 1)
		}
		r = r.Normal()

		// Tangent along the path: use forward/backward difference.
		var t math32.Vector3
		switch {
		case i == 0:
			t = pts[i+1].Sub(p)
		case i == nPts-1:
			t = p.Sub(pts[i-1])
		default:
			t = pts[i+1].Sub(pts[i-1])
		}
		if t.Length() == 0 {
			t = math32.Vec3(1, 0, 0)
		}
		t = t.Normal()

		// Binormal completes an orthogonal frame.
		b := t.Cross(r)
		if b.Length() == 0 {
			b = math32.Vec3(0, 0, 1)
		}
		b = b.Normal()

		frames[i] = frame{rad: r, bin: b}
	}

	// Build vertices: for each point, emit a 4-vertex cross-section.
	for i := 0; i < nPts; i++ {
		p := pts[i]
		r := frames[i].rad
		b := frames[i].bin

		// Cross-section vertices around the path center.
		// Long side (width) runs around the mandrel (binormal),
		// thin side (depth) runs radially.
		offsets := []math32.Vector3{
			// +width/2 along binormal, +depth/2 along radial
			b.MulScalar(halfW).Add(r.MulScalar(halfD)),
			// -width/2 along binormal, +depth/2 along radial
			b.MulScalar(-halfW).Add(r.MulScalar(halfD)),
			// -width/2 along binormal, -depth/2 along radial
			b.MulScalar(-halfW).Add(r.MulScalar(-halfD)),
			// +width/2 along binormal, -depth/2 along radial
			b.MulScalar(halfW).Add(r.MulScalar(-halfD)),
		}

		for _, off := range offsets {
			v := p.Add(off)
			vtx = append(vtx, v.X, v.Y, v.Z)
			// For shading, align normals with the mandrel radial direction.
			nrm = append(nrm, r.X, r.Y, r.Z)
			uv = append(uv, 0, 0)
		}
	}

	// Connect consecutive cross-sections into a tube (4 quads per segment).
	for i := 0; i < nPts-1; i++ {
		base0 := uint32(i * 4)
		base1 := uint32((i + 1) * 4)

		// Quad 0
		idx = append(idx,
			base0+0, base0+1, base1+1,
			base0+0, base1+1, base1+0,
		)
		// Quad 1
		idx = append(idx,
			base0+1, base0+2, base1+2,
			base0+1, base1+2, base1+1,
		)
		// Quad 2
		idx = append(idx,
			base0+2, base0+3, base1+3,
			base0+2, base1+3, base1+2,
		)
		// Quad 3
		idx = append(idx,
			base0+3, base0+0, base1+0,
			base0+3, base1+0, base1+3,
		)
	}

	// Replace contents in-place on the reused mesh object.
	gm.Vertex = vtx
	gm.Normal = nrm
	gm.TexCoord = uv
	gm.Index = idx
	// Notify live renderer that this mesh changed.
	if sc != nil {
		sc.SetMesh(gm)
	}
	return gm
}


// drawArrow creates a single arrow from start to end with the given color.
// The arrow consists of a shaft (line) and a head (two lines), and is made
// slightly emissive for visibility.
func drawArrow(parent *xyz.Group, name string, start, end math32.Vector3, clr color.RGBA, shaftWidth, headFrac, headWidth float32, sc *xyz.Scene) {
	// Create shaft line
	shaftPts := []math32.Vector3{start, end}
	shaftMesh := xyz.NewLines(sc, name+"-shaft", shaftPts, math32.Vec2(shaftWidth, shaftWidth), xyz.OpenLines)
	shaftSolid := xyz.NewSolid(parent).SetMesh(shaftMesh)
	shaftSolid.SetName(name + "-shaft-solid")
	shaftSolid.Material.SetColor(clr)
	shaftSolid.Material.Emissive = color.RGBA{
		R: uint8(float32(clr.R) * 0.21),
		G: uint8(float32(clr.G) * 0.21),
		B: uint8(float32(clr.B) * 0.21),
		A: 255,
	}

	// Calculate direction and arrowhead base
	dir := end.Sub(start).Normal()
	arrowLen := end.Sub(start).Length()
	headLen := headFrac * arrowLen
	if headLen == 0 || arrowLen == 0 {
		return
	}
	headBase := end.Sub(dir.MulScalar(headLen))

	// Define an arbitrary vector to generate head orientation
	var ortho math32.Vector3
	if math32.Abs(dir.Z) < 0.99 {
		ortho = math32.Vec3(0, 0, 1)
	} else {
		ortho = math32.Vec3(0, 1, 0)
	}
	// Head side vectors
	side := dir.Cross(ortho).Normal().MulScalar(headWidth * headLen)
	h1 := headBase.Add(side)
	h2 := headBase.Sub(side)

	headMesh := xyz.NewLines(sc, name+"-head", []math32.Vector3{h1, end, h2}, math32.Vec2(shaftWidth, shaftWidth), xyz.OpenLines)
	headSolid := xyz.NewSolid(parent).SetMesh(headMesh)
	headSolid.SetName(name + "-head-solid")
	headSolid.Material.SetColor(clr)
	headSolid.Material.Emissive = color.RGBA{
		R: uint8(float32(clr.R) * 0.21),
		G: uint8(float32(clr.G) * 0.21),
		B: uint8(float32(clr.B) * 0.21),
		A: 255,
	}
}

// drawWinderAxes draws X/Y/Z arrow axes at the winder origin in winder space.
func drawWinderAxes(sc *xyz.Scene, w *Wind, winderRoot *xyz.Group) {
	if sc == nil || w == nil || w.Mandrel == nil || winderRoot == nil {
		return
	}
	YZaxisArrowLen := float32(w.Mandrel.ZMax * 1.25)
	XaxisArrowLen := float32(w.Mandrel.XMax * 1.25)

	if YZaxisArrowLen <= 0 {
		YZaxisArrowLen *= -1
	}
	if XaxisArrowLen <= 0 {
		XaxisArrowLen *= -1
	}

	shaftWidth := float32(0.2)
	headFrac := float32(0.14)  // fraction of arrow length that is the head
	headWidth := float32(0.14) // relative width factor for the arrowhead

	// X Axis (red)
	drawArrow(winderRoot, "winder-axis-x",
		math32.Vec3(0, 0, 0), math32.Vec3(XaxisArrowLen, 0, 0),
		color.RGBA{R: 255, G: 80, B: 80, A: 255}, shaftWidth, headFrac, headWidth, sc)
	// Y Axis (green)
	drawArrow(winderRoot, "winder-axis-y",
		math32.Vec3(0, 0, 0), math32.Vec3(0, YZaxisArrowLen, 0),
		color.RGBA{R: 80, G: 255, B: 80, A: 255}, shaftWidth, headFrac, headWidth, sc)
	// Z Axis (blue)
	drawArrow(winderRoot, "winder-axis-z",
		math32.Vec3(0, 0, 0), math32.Vec3(0, 0, YZaxisArrowLen),
		color.RGBA{R: 80, G: 80, B: 255, A: 255}, shaftWidth, headFrac, headWidth, sc)
}

// interpPoint linearly interpolates between two cylindrical Points in the internal
// coordinate system, including angle, then the result is converted to Cartesian
// via Point.ToRect when rendering. This allows the renderer to interpret large
// angular moves as circular arcs without changing the underlying path.
func interpPoint(p0, p1 Point, t float64) Point {
	return Point{
		X: p0.X + t*(p1.X-p0.X),
		Y: p0.Y + t*(p1.Y-p0.Y),
		Z: p0.Z + t*(p1.Z-p0.Z),
		A: p0.A + t*(p1.A-p0.A),
		F: p0.F + t*(p1.F-p0.F),
	}
}
