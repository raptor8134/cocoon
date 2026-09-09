// The wind configuration model.
//
// This is the single source of truth the UI edits. Both the block editor and
// the JSON view read and write this same object, which is what keeps the two
// views from drifting apart: switching modes is just a re-render, never a
// conversion between two rival representations.
//
// The shape matches what internal/wind's JSON parser expects, so it can be
// handed to the core verbatim.

export const LAYER_TYPES = ["hoop", "helical"];
export const MANDREL_TYPES = ["cylindrical", "arbitrary_axial"];

/** Profiles shipped with the app, offered in the mandrel dropdown. */
export const BUILTIN_PROFILES = [
  "4in_3to1_tanogive.csv",
  "roundends.csv",
  "zigzag.csv",
];

export function newLayer(type) {
  // Defaults chosen to produce a valid, visible wind immediately -- adding a
  // layer should never drop you into an error state you have to fix first.
  if (type === "helical") {
    return { type: "helical", angle: 45, nrepeat: 1 };
  }
  return { type: "hoop", stepover: 3, nrepeat: 1 };
}

export const SPINDLE_DIRECTIONS = [
  { value: "forward", label: "Forward (+C)" },
  { value: "reverse", label: "Reverse (−C)" },
];

/** Lathe convention (ISO 841); see internal/wind/types.go for the derivation. */
export const DEFAULT_AXES = { carriage: "Z", eye: "A", radial: "X", spindle: "C" };

export const AXIS_FIELDS = [
  { key: "carriage", label: "Carriage", hint: "along the mandrel axis" },
  { key: "radial", label: "Radial", hint: "cross-feed / mandrel radius" },
  { key: "eye", label: "Payout eye", hint: "eye rotation" },
  { key: "spindle", label: "Spindle", hint: "mandrel rotation" },
];

export function defaultConfig() {
  return {
    comment: "New wind",
    machine: { spindle_direction: "forward", axes: { ...DEFAULT_AXES } },
    filament: { width: 20, thickness: 0.25, feedrate: 100 },
    mandrel: { type: "cylindrical", dimensions: { length: 100, diameter: 50 } },
    layers: [newLayer("hoop"), newLayer("helical")],
  };
}

/**
 * Fill in anything a hand-written or imported config left out.
 *
 * The block editor needs every field it renders to exist, but wind files are
 * written by hand and legitimately omit things (a preset instead of explicit
 * filament values, for instance). Rather than have every widget cope with
 * undefined, normalise once on load. Values that are present are never
 * changed -- this only adds.
 */
export function normalize(cfg) {
  const out = cfg && typeof cfg === "object" ? structuredClone(cfg) : {};

  if (typeof out.comment !== "string") out.comment = "";

  const f = (out.filament = out.filament && typeof out.filament === "object" ? out.filament : {});
  if (typeof f.width !== "number") f.width = 20;
  if (typeof f.thickness !== "number") f.thickness = 0.25;
  if (typeof f.feedrate !== "number") f.feedrate = 100;

  const m = (out.mandrel = out.mandrel && typeof out.mandrel === "object" ? out.mandrel : {});
  if (!MANDREL_TYPES.includes(m.type)) m.type = "cylindrical";
  if (m.type === "cylindrical") {
    const d = (m.dimensions = m.dimensions && typeof m.dimensions === "object" ? m.dimensions : {});
    if (typeof d.length !== "number") d.length = 100;
    if (typeof d.diameter !== "number") d.diameter = 50;
  } else if (typeof m.profile !== "string" && !Array.isArray(m.profile)) {
    m.profile = BUILTIN_PROFILES[0];
  }

  const mach = (out.machine = out.machine && typeof out.machine === "object" ? out.machine : {});
  if (mach.spindle_direction !== "reverse") mach.spindle_direction = "forward";
  const ax = (mach.axes = mach.axes && typeof mach.axes === "object" ? mach.axes : {});
  for (const [k, v] of Object.entries(DEFAULT_AXES)) {
    if (typeof ax[k] !== "string" || !/^[A-Za-z]$/.test(ax[k])) ax[k] = v;
    else ax[k] = ax[k].toUpperCase();
  }

  if (!Array.isArray(out.layers)) out.layers = [];
  out.layers = out.layers.map((raw) => {
    const l = raw && typeof raw === "object" ? { ...raw } : {};
    if (!LAYER_TYPES.includes(l.type)) l.type = "hoop";
    if (typeof l.nrepeat !== "number") l.nrepeat = 1;
    if (l.type === "hoop" && typeof l.stepover !== "number") l.stepover = 3;
    if (l.type === "helical" && typeof l.angle !== "number") l.angle = 45;
    return l;
  });

  return out;
}

/**
 * Strip fields that do not apply to the current type selections.
 *
 * Switching a layer from hoop to helical leaves a stale `stepover` behind;
 * harmless to the generator, but it shows up in the JSON view as clutter and
 * makes diffs noisy. Called just before serialising.
 */
export function prune(cfg) {
  const out = structuredClone(cfg);

  if (out.mandrel.type === "cylindrical") {
    delete out.mandrel.profile;
  } else {
    delete out.mandrel.dimensions;
  }

  out.layers = out.layers.map((l) => {
    const c = { ...l };
    if (c.type === "hoop") delete c.angle;
    if (c.type === "helical") delete c.stepover;
    return c;
  });

  if (!out.comment) delete out.comment;
  return out;
}

/**
 * Client-side sanity checks, run before calling the core.
 *
 * The Go side validates authoritatively and produces the messages that
 * matter; this exists only to catch the obvious cases without a round trip,
 * and to let the block editor mark the specific offending input.
 *
 * Returns [{path, message}], empty when nothing is obviously wrong.
 */
export function quickCheck(cfg) {
  const issues = [];
  const num = (v) => typeof v === "number" && Number.isFinite(v);

  if (!num(cfg.filament.width) || cfg.filament.width <= 0) {
    issues.push({ path: "filament.width", message: "Filament width must be greater than 0." });
  }
  if (cfg.mandrel.type === "cylindrical") {
    const d = cfg.mandrel.dimensions;
    if (!num(d.length) || d.length <= 0) {
      issues.push({ path: "mandrel.length", message: "Mandrel length must be greater than 0." });
    }
    if (!num(d.diameter) || d.diameter <= 0) {
      issues.push({ path: "mandrel.diameter", message: "Mandrel diameter must be greater than 0." });
    }
  }
  if (cfg.layers.length === 0) {
    issues.push({ path: "layers", message: "Add at least one layer to generate a wind." });
  }

  cfg.layers.forEach((l, i) => {
    if (l.type === "hoop" && (!num(l.stepover) || l.stepover <= 0)) {
      issues.push({ path: `layers.${i}.stepover`, message: `Layer ${i + 1}: stepover must be greater than 0.` });
    }
    if (l.type === "helical" && (!num(l.angle) || l.angle <= 0 || l.angle >= 180)) {
      issues.push({ path: `layers.${i}.angle`, message: `Layer ${i + 1}: angle must be between 0 and 180 degrees.` });
    }
  });

  return issues;
}
