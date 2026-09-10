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
    this.labelsGroup = new THREE.Group();
    this.scene.add(this.mandrelGroup, this.pathGroup, this.axesGroup, this.labelsGroup);

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
    this.clearGroup(this.labelsGroup);
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
  setAxisLabelsVisible(v) { this.labelsGroup.visible = v; }

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
    this.clearGroup(this.labelsGroup);
    const L = Math.max(length * 0.28, zmax * 1.5, 1);
    const origin = new THREE.Vector3(xmin ?? 0, 0, 0);
    const R = zmax ?? 25;
    const labelScale = L * 0.16;
    const A = this.axisLetters;

    // Lathe convention (ISO 841), oriented so forward rotation is +C:
    //
    //   X = horizontal, front-facing (the cross-slide direction on a lathe)
    //   Y = vertical, up
    //   Z = X x Y, which points OPPOSITE the direction the carriage advances
    //
    // That last consequence follows from the right-hand rule once +C is
    // chosen; it is drawn rather than hidden. Labels carry only the letter and
    // sign -- what each axis means lives in Settings.
    const label = (text, css, pos) => {
      const sprite = makeLabel(text, css, labelScale);
      sprite.position.copy(pos);
      this.labelsGroup.add(sprite);
    };

    for (const a of [
      { dir: [0, 0, 1], color: 0xff5a5a, css: "#ff8a8a", letter: A.radial },
      { dir: [0, 1, 0], color: 0x5aff7a, css: "#8dffa8", letter: "Y" },
    ]) {
      const dir = new THREE.Vector3(...a.dir);
      this.axesGroup.add(new THREE.ArrowHelper(dir, origin, L, a.color, L * 0.13, L * 0.075));
      label(`+${a.letter}`, a.css, origin.clone().addScaledVector(dir, L * 1.14));
    }

    // The carriage arrow shows the direction the carriage actually TRAVELS,
    // running from the origin past the far end so its extent reads as the
    // stroke. Because +Z points opposite carriage advance (see above), that
    // direction is -Z, and the label says so.
    const axialY = -R * 1.45;
    const span = Math.max((xmax ?? 0) - (xmin ?? 0), L);
    const axialLen = span * 1.1;
    this.axesGroup.add(
      new THREE.ArrowHelper(
        new THREE.Vector3(1, 0, 0),
        new THREE.Vector3(xmin ?? 0, axialY, 0),
        axialLen, 0x5a8aff, L * 0.13, L * 0.075
      )
    );
    label(`\u2212${A.carriage}`, "#8dabff",
      new THREE.Vector3((xmin ?? 0) + axialLen + labelScale * 1.4, axialY, 0));

    this.axesGroup.add(this.buildSpindleArrow(xmin, xmax, zmax, L, label));
  }

  /** An arc with an arrowhead showing which way the mandrel turns. */
  buildSpindleArrow(xmin, xmax, zmax, L, label) {
    const group = new THREE.Group();
    const r = (zmax ?? 25) * 1.35;
    const x = (xmin ?? 0) + ((xmax ?? 0) - (xmin ?? 0)) * 0.18;

    // Render frame is Y = r*sin(A), Z = r*cos(A), so increasing A sweeps from
    // +Z toward +Y: up over the top, which is the left-handed sense.
    // A short 30 degree arc is enough to read the direction; a long sweep just
    // competes with the path geometry for attention. Centred between +X
    // (front) and +Y (up), where it is visible from the default camera.
    // Swapping the endpoints reverses the arrowhead with it.
    let from = 30, to = 60;
    if (this.spindleReversed) [from, to] = [to, from];
    const steps = 16;
    const pts = [];
    for (let i = 0; i <= steps; i++) {
      const aDeg = from + ((to - from) * i) / steps;
      const a = (aDeg * Math.PI) / 180;
      pts.push(new THREE.Vector3(x, r * Math.sin(a), r * Math.cos(a)));
    }
    const geom = new THREE.BufferGeometry().setFromPoints(pts);
    group.add(new THREE.Line(geom, new THREE.LineBasicMaterial({ color: 0xffc857 })));

    // Arrowhead tangent to the arc at its leading end.
    //
    // d/dA (r sinA, r cosA) = (r cosA, -r sinA), which is the tangent for
    // INCREASING A. Reversing the spindle sweeps the arc the other way, so the
    // tangent has to be negated with it -- swapping the endpoints alone flipped
    // the arc but left the arrowhead pointing the original way.
    const aEnd = (to * Math.PI) / 180;
    const sweep = Math.sign(to - from) || 1;
    const tip = new THREE.Vector3(x, r * Math.sin(aEnd), r * Math.cos(aEnd));
    const tangent = new THREE.Vector3(0, sweep * Math.cos(aEnd), -sweep * Math.sin(aEnd)).normalize();
    group.add(new THREE.ArrowHelper(tangent, tip, r * 0.28, 0xffc857, r * 0.28, r * 0.16));

    // Park the label out along the arc, clear of the +X and +Y arrows.
    const labelA = (45 * Math.PI) / 180;
    const sign = this.spindleReversed ? "\u2212" : "+";
    label(
      `${sign}${this.axisLetters.spindle}`,
      "#ffd98a",
      new THREE.Vector3(x, r * 1.32 * Math.sin(labelA), r * 1.32 * Math.cos(labelA))
    );
    return group;
  }

  /** Point the camera at the whole mandrel and remember that as "home". */
  frame({ xmin, xmax, zmax, length }) {
    const cx = ((xmin ?? 0) + (xmax ?? length ?? 0)) / 2;
    const target = new THREE.Vector3(cx, 0, 0);

    // Distance that fits the scene's bounding sphere in the vertical FOV, with
    // a margin so it does not touch the edges.
    //
    // The axis arrows and their labels reach beyond the mandrel -- the radial
    // arrows extend to ~1.14x the axis length above centre -- so framing the
    // mandrel alone clips the topmost label out of view. Include that extent.
    const axisReach = Math.max((length ?? 100) * 0.28, (zmax ?? 25) * 1.5, 1) * 1.25;
    const radius = Math.max(
      Math.hypot((length ?? 100) / 2, Math.max(zmax ?? 25, axisReach)),
      1
    );
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
