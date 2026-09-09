// 3D viewport, replacing the old Go/xyz renderer.
//
// Two things are drawn: the mandrel as a lathed surface of revolution, and
// each layer's path as a LineSegments run coloured by layer type.
//
// Paths are drawn as lines rather than the extruded ribbons the Go renderer
// built. Ribbons gave constant world-space width, but cost four vertices and
// eight triangles per point; for a dense wind that is millions of triangles
// for something a line conveys just as well at this zoom. If ribbon width
// turns out to matter for judging coverage, that is the thing to revisit.

import * as THREE from "three";
import { OrbitControls } from "three/addons/controls/OrbitControls.js";

const COLORS = {
  hoop: 0xe04a4a,
  helical: 0x34a862,
  default: 0x5b9cff,
};

/**
 * Build a camera-facing text label as a sprite.
 *
 * Drawn to a canvas texture rather than using CSS2DRenderer, which would mean
 * vendoring another addon and overlaying a second DOM layer. Sprites stay
 * readable at any orbit angle and cost one draw call each.
 */
function makeLabel(text, color = "#e6eaf0", scale = 1) {
  const pad = 8;
  const font = "600 44px system-ui, -apple-system, Segoe UI, Roboto, sans-serif";

  const measure = document.createElement("canvas").getContext("2d");
  measure.font = font;
  const w = Math.ceil(measure.measureText(text).width) + pad * 2;
  const h = 62;

  const canvas = document.createElement("canvas");
  canvas.width = w;
  canvas.height = h;
  const ctx = canvas.getContext("2d");
  ctx.font = font;
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";

  // Dark plate behind the glyphs so labels stay legible against both the pale
  // mandrel and the dark background.
  ctx.fillStyle = "rgba(13,16,21,0.72)";
  ctx.beginPath();
  ctx.roundRect(0, 0, w, h, 10);
  ctx.fill();

  ctx.fillStyle = color;
  ctx.fillText(text, w / 2, h / 2 + 2);

  const tex = new THREE.CanvasTexture(canvas);
  tex.anisotropy = 4;
  const sprite = new THREE.Sprite(new THREE.SpriteMaterial({
    map: tex,
    depthTest: false,   // labels should never be swallowed by the mandrel
    transparent: true,
  }));
  sprite.renderOrder = 999;
  sprite.scale.set((w / h) * scale, scale, 1);
  return sprite;
}

export class Viewer {
  constructor(host) {
    this.host = host;

    this.renderer = new THREE.WebGLRenderer({ antialias: true, alpha: false });
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
    host.append(this.renderer.domElement);

    this.scene = new THREE.Scene();
    this.scene.background = new THREE.Color(0x0d1015);

    this.camera = new THREE.PerspectiveCamera(45, 1, 0.1, 10000);
    this.camera.position.set(0, 120, 260);

    this.controls = new OrbitControls(this.camera, this.renderer.domElement);
    this.controls.enableDamping = true;
    this.controls.dampingFactor = 0.08;

    // Two directional lights plus ambient: enough to read curvature on the
    // mandrel without the hard terminator a single light produces on a
    // cylinder.
    this.scene.add(new THREE.AmbientLight(0xffffff, 0.55));
    const key = new THREE.DirectionalLight(0xffffff, 1.6);
    key.position.set(1, 1.4, 1);
    this.scene.add(key);
    const fill = new THREE.DirectionalLight(0xffffff, 0.5);
    fill.position.set(-1, -0.5, -1);
    this.scene.add(fill);

    this.mandrelGroup = new THREE.Group();
    this.pathGroup = new THREE.Group();
    this.axesGroup = new THREE.Group();
    this.scene.add(this.mandrelGroup, this.pathGroup, this.axesGroup);

    // Drawn direction of the spindle arrow; set from the config so the
    // picture matches what the machine will actually do.
    this.spindleReversed = false;

    // Letters shown on the axis labels. Aliasable in Settings, so the picture
    // always matches what the generated G-code will actually say.
    this.axisLetters = { carriage: "Z", radial: "X", eye: "A", spindle: "C" };

    this.homeTarget = new THREE.Vector3();
    this.homePosition = this.camera.position.clone();

    // ResizeObserver rather than a window listener: the viewport is a flex
    // child, so it changes size when the panes reflow, not only when the
    // window does.
    this.resizeObserver = new ResizeObserver(() => this.resize());
    this.resizeObserver.observe(host);
    this.resize();

    this.running = true;
    this.animate = this.animate.bind(this);
    requestAnimationFrame(this.animate);
  }

  resize() {
    const w = this.host.clientWidth || 1;
    const h = this.host.clientHeight || 1;
    this.renderer.setSize(w, h, false);
    this.camera.aspect = w / h;
    this.camera.updateProjectionMatrix();
  }

  animate() {
    if (!this.running) return;
    this.controls.update();
    this.renderer.render(this.scene, this.camera);
    requestAnimationFrame(this.animate);
  }

  dispose() {
    this.running = false;
    this.resizeObserver.disconnect();
    this.clearGroup(this.pathGroup);
    this.clearGroup(this.mandrelGroup);
    this.renderer.dispose();
  }

  /** Free GPU buffers before dropping a group's children. */
  clearGroup(group) {
    for (const child of group.children) {
      child.geometry?.dispose();
      if (Array.isArray(child.material)) child.material.forEach((m) => m.dispose());
      else child.material?.dispose();
    }
    group.clear();
  }

  setSpindleReversed(v) { this.spindleReversed = !!v; }

  setAxisLetters(letters) { this.axisLetters = { ...this.axisLetters, ...letters }; }

  setMandrelVisible(v) { this.mandrelGroup.visible = v; }
  setAxesVisible(v) { this.axesGroup.visible = v; }

  /** Replace the scene contents from a generate() result. */
  update(result) {
    this.buildMandrel(result.mandrel);
    this.buildPaths(result);
    this.buildAxes(result.mandrel);
    this.frame(result.mandrel);
  }

  buildMandrel({ x, r }) {
    this.clearGroup(this.mandrelGroup);
    if (!x || x.length < 2) return;

    // LatheGeometry revolves a profile around +Y, but the winder's axis is X,
    // so the points go in as (radius, axial) and the mesh is rotated onto X
    // afterwards.
    //
    // The radius is shrunk slightly so paths sit proudly on the surface
    // instead of z-fighting with it.
    const pts = [];
    for (let i = 0; i < x.length; i++) {
      pts.push(new THREE.Vector2(Math.max(r[i] * 0.985, 0.0001), x[i]));
    }

    const geom = new THREE.LatheGeometry(pts, 128);
    const mat = new THREE.MeshStandardMaterial({
      color: 0x8a8f98,
      roughness: 0.65,
      metalness: 0.15,
      flatShading: false,
    });
    const mesh = new THREE.Mesh(geom, mat);
    mesh.rotation.z = -Math.PI / 2; // revolve axis +Y -> +X
    this.mandrelGroup.add(mesh);
  }

  buildPaths({ positions, layers }) {
    this.clearGroup(this.pathGroup);
    if (!positions || positions.length === 0) return;

    // One BufferGeometry per layer, all viewing the same underlying buffer
    // via a shared attribute plus a draw range -- no per-layer copying.
    const attr = new THREE.BufferAttribute(positions, 3);

    for (const layer of layers) {
      if (layer.count < 2) continue;
      const geom = new THREE.BufferGeometry();
      geom.setAttribute("position", attr);
      geom.setDrawRange(layer.start, layer.count);

      const mat = new THREE.LineBasicMaterial({
        color: COLORS[layer.type] ?? COLORS.default,
      });
      // Line (not LineSegments): consecutive vertices form one continuous
      // polyline, which is exactly how the path is generated.
      this.pathGroup.add(new THREE.Line(geom, mat));
    }
  }

  buildAxes({ length, zmax, xmin, xmax }) {
    this.clearGroup(this.axesGroup);
    const L = Math.max(length * 0.28, zmax * 1.5, 1);
    const origin = new THREE.Vector3(xmin ?? 0, 0, 0);
    const R = zmax ?? 25;
    const labelScale = L * 0.16;
    const A = this.axisLetters;

    // Lathe convention (ISO 841), oriented so that the spindle turning forward
    // is +C. That constraint fixes everything else:
    //
    //   +C about +Z rotates X toward Y (right-hand rule). Forward sweeps the
    //   surface up and over the back, so X = up and Y = back, which makes
    //   Z = X x Y point OPPOSITE the direction the carriage advances.
    //
    // That last consequence is deliberately drawn rather than hidden: the
    // carriage moving in -Z is surprising, and it is better seen than
    // discovered later in a G-code file.
    for (const a of [
      { dir: [0, 1, 0], color: 0xff5a5a, css: "#ff8a8a", label: `+${A.radial} — radial (up)` },
      // Y completes the right-handed linear set. Nothing is emitted on it --
      // the machine has no second linear radial axis -- but drawing it makes
      // the frame's handedness checkable by eye.
      { dir: [0, 0, -1], color: 0x5aff7a, css: "#8dffa8", label: "+Y — (unused, completes frame)" },
    ]) {
      const dir = new THREE.Vector3(...a.dir);
      this.axesGroup.add(new THREE.ArrowHelper(dir, origin, L, a.color, L * 0.13, L * 0.075));
      const label = makeLabel(a.label, a.css, labelScale);
      // The back-pointing axis foreshortens to almost nothing on screen, so
      // push its label further out to keep it clear of the spindle arc.
      const out = a.dir[2] === -1 ? 1.55 : 1.12;
      label.position.copy(origin).addScaledVector(dir, L * out);
      this.axesGroup.add(label);
    }

    // The carriage axis runs along the mandrel, so it is offset below the
    // surface to stay visible. It points from the far end back toward the
    // origin, which is the +Z direction derived above.
    const axialY = -R * 1.45;
    const axialLen = Math.max(((xmax ?? 0) - (xmin ?? 0)) * 0.55, L);
    const axialFrom = new THREE.Vector3((xmin ?? 0) + axialLen, axialY, 0);
    this.axesGroup.add(
      new THREE.ArrowHelper(
        new THREE.Vector3(-1, 0, 0), axialFrom, axialLen,
        0x5a8aff, L * 0.13, L * 0.075
      )
    );
    const axialLabel = makeLabel(`+${A.carriage} — carriage`, "#8dabff", labelScale);
    axialLabel.position.set((xmin ?? 0) - labelScale * 2.4, axialY, 0);
    this.axesGroup.add(axialLabel);

    // The payout eye rotates ABOUT the radial axis, so its letter belongs
    // beside that arrow rather than on an axis of its own.
    const eyeNote = makeLabel(`${A.eye} — eye rotates about ${A.radial}`, "#ffb0b0", labelScale * 0.82);
    // Sit it partway up the radial arrow it refers to, rather than beyond the
    // tip where the +X and spindle labels already compete for space.
    eyeNote.position.copy(origin).addScaledVector(new THREE.Vector3(0, 1, 0), L * 0.5);
    this.axesGroup.add(eyeNote);

    // Spindle rotation indicator.
    this.axesGroup.add(this.buildSpindleArrow(xmin, xmax, zmax, L));
  }

  /** An arc with an arrowhead showing which way the mandrel turns. */
  buildSpindleArrow(xmin, xmax, zmax, L) {
    const group = new THREE.Group();
    const r = (zmax ?? 25) * 1.35;
    const x = (xmin ?? 0) + ((xmax ?? 0) - (xmin ?? 0)) * 0.18;

    // Render frame is Y = r*sin(A), Z = r*cos(A), so increasing A sweeps from
    // +Z toward +Y: up over the top, which is the left-handed sense.
    // Sweeping from -50 to 130 degrees runs up over the top (the left-handed
    // sense); swapping the endpoints reverses the arrowhead with it.
    let from = -50, to = 130;
    if (this.spindleReversed) [from, to] = [to, from];
    const steps = 48;
    const pts = [];
    for (let i = 0; i <= steps; i++) {
      const aDeg = from + ((to - from) * i) / steps;
      const a = (aDeg * Math.PI) / 180;
      pts.push(new THREE.Vector3(x, r * Math.sin(a), r * Math.cos(a)));
    }
    const geom = new THREE.BufferGeometry().setFromPoints(pts);
    group.add(new THREE.Line(geom, new THREE.LineBasicMaterial({ color: 0xffc857 })));

    // Arrowhead tangent to the arc at its leading end.
    const aEnd = (to * Math.PI) / 180;
    const tip = new THREE.Vector3(x, r * Math.sin(aEnd), r * Math.cos(aEnd));
    const tangent = new THREE.Vector3(0, Math.cos(aEnd), -Math.sin(aEnd)).normalize();
    group.add(new THREE.ArrowHelper(tangent, tip, r * 0.28, 0xffc857, r * 0.28, r * 0.16));

    // Park the label out along the arc rather than straight up, where it
    // would sit on top of the +radial (up) arrow's label.
    const labelA = (68 * Math.PI) / 180;
    const sign = this.spindleReversed ? "\u2212" : "+";
    const dir = this.spindleReversed ? "reverse" : "forward";
    const label = makeLabel(
      `${sign}${this.axisLetters.spindle} — spindle (${dir})`,
      "#ffd98a", L * 0.16);
    label.position.set(x, r * 1.7 * Math.sin(labelA), r * 1.7 * Math.cos(labelA));
    group.add(label);
    return group;
  }

  /** Point the camera at the whole mandrel and remember that as "home". */
  frame({ xmin, xmax, zmax, length }) {
    const cx = ((xmin ?? 0) + (xmax ?? length ?? 0)) / 2;
    const target = new THREE.Vector3(cx, 0, 0);

    // Distance that fits the mandrel's bounding sphere in the vertical FOV,
    // with a margin so it does not touch the edges.
    const radius = Math.max(Math.hypot((length ?? 100) / 2, zmax ?? 25), 1);
    const dist = (radius / Math.sin((this.camera.fov * Math.PI) / 360)) * 1.25;

    this.camera.position.set(cx + dist * 0.1, dist * 0.45, dist * 0.85);
    this.controls.target.copy(target);
    this.camera.near = Math.max(dist / 1000, 0.01);
    this.camera.far = dist * 10;
    this.camera.updateProjectionMatrix();
    this.controls.update();

    this.homeTarget.copy(target);
    this.homePosition.copy(this.camera.position);
  }

  goHome() {
    this.camera.position.copy(this.homePosition);
    this.controls.target.copy(this.homeTarget);
    this.controls.update();
  }
}
