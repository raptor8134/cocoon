// Block editor: the visual, no-JSON way to build a wind.
//
// Design notes:
//   - Every block edits the shared config object in place and calls onChange.
//     There is no separate widget state to keep in sync.
//   - Blocks are re-rendered wholesale on structural changes (add, delete,
//     reorder, type switch) but NOT on plain value edits. Re-rendering while
//     someone is typing in a number field would steal focus and the caret,
//     which is exactly the class of bug we just left behind.
//   - Layer blocks carry a colour bar matching their path colour in the 3D
//     view, so the two panes refer to the same thing.

import { LAYER_TYPES, MANDREL_TYPES, BUILTIN_PROFILES, SPINDLE_DIRECTIONS, AXIS_FIELDS, DEFAULT_AXES, newLayer } from "./model.js";

/** Build a labelled numeric input bound to obj[key]. */
function numberField(label, obj, key, opts = {}) {
  const wrap = el("div", "field");
  if (label) wrap.append(el("label", null, label));

  const input = document.createElement("input");
  input.type = "number";
  input.value = obj[key];
  if (opts.min !== undefined) input.min = opts.min;
  if (opts.max !== undefined) input.max = opts.max;
  input.step = opts.step ?? "any";
  input.dataset.path = opts.path ?? "";

  input.addEventListener("input", () => {
    const v = parseFloat(input.value);
    // Keep the last good value rather than writing NaN into the config: a
    // half-typed "-" or "." is a transient state, not an error worth
    // propagating into the generator on every keystroke.
    if (Number.isFinite(v)) {
      obj[key] = v;
      input.classList.remove("invalid");
      opts.onChange?.();
    } else {
      input.classList.add("invalid");
    }
  });

  wrap.append(input);
  if (opts.unit) wrap.append(el("span", "unit", opts.unit));
  return wrap;
}

function selectField(label, value, options, onPick) {
  const wrap = el("div", "field");
  if (label) wrap.append(el("label", null, label));
  const sel = document.createElement("select");
  for (const o of options) {
    const opt = document.createElement("option");
    opt.value = typeof o === "string" ? o : o.value;
    opt.textContent = typeof o === "string" ? titleCase(o) : o.label;
    if (opt.value === value) opt.selected = true;
    sel.append(opt);
  }
  sel.addEventListener("change", () => onPick(sel.value));
  wrap.append(sel);
  return wrap;
}

function el(tag, cls, text) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
}

function titleCase(s) {
  return s.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}

// --- mandrel --------------------------------------------------------------

export function renderMandrel(host, cfg, { onChange, onStructure }) {
  host.replaceChildren();
  const m = cfg.mandrel;

  host.append(
    selectField("Type", m.type, MANDREL_TYPES, (v) => {
      m.type = v;
      // Populate the fields the newly chosen type needs, so switching never
      // lands in a half-configured state.
      if (v === "cylindrical" && !m.dimensions) m.dimensions = { length: 100, diameter: 50 };
      if (v === "arbitrary_axial" && !m.profile) m.profile = BUILTIN_PROFILES[0];
      onStructure();
    })
  );

  if (m.type === "cylindrical") {
    host.append(
      numberField("Length", m.dimensions, "length", { unit: "mm", min: 0, onChange, path: "mandrel.length" }),
      numberField("Diameter", m.dimensions, "diameter", { unit: "mm", min: 0, onChange, path: "mandrel.diameter" })
    );
  } else {
    const known = BUILTIN_PROFILES.includes(m.profile);
    host.append(
      selectField(
        "Profile",
        known ? m.profile : "__custom",
        [...BUILTIN_PROFILES, { value: "__custom", label: "Custom…" }],
        (v) => {
          m.profile = v === "__custom" ? "" : v;
          onStructure();
        }
      )
    );
    if (!known) {
      const wrap = el("div", "field grow");
      wrap.append(el("label", null, "File"));
      const input = document.createElement("input");
      input.type = "text";
      input.className = "grow";
      input.placeholder = "profile.csv";
      input.value = typeof m.profile === "string" ? m.profile : "";
      input.addEventListener("input", () => {
        m.profile = input.value;
        onChange();
      });
      wrap.append(input);
      host.append(wrap);
    }
    host.append(el("span", "unit", "CSV profiles load from the app's profiles/ folder"));
  }
}

// --- filament -------------------------------------------------------------

export function renderFilament(host, cfg, { onChange }) {
  host.replaceChildren();
  const f = cfg.filament;
  host.append(
    numberField("Width", f, "width", { unit: "mm", min: 0, onChange, path: "filament.width" }),
    numberField("Thickness", f, "thickness", { unit: "mm", min: 0, onChange, path: "filament.thickness" }),
    numberField("Feedrate", f, "feedrate", { unit: "mm/s", min: 0, onChange, path: "filament.feedrate" })
  );

  // Be honest about what the generator currently does with these, rather
  // than implying they take effect. See TODO(feedrate) in internal/wind.
  const note = el("span", "unit", "thickness and feedrate are not yet applied to output");
  note.style.flexBasis = "100%";
  host.append(note);
}

// --- machine --------------------------------------------------------------

export function renderMachine(host, cfg, { onStructure }) {
  host.replaceChildren();
  const m = cfg.machine;

  host.append(
    selectField("Spindle", m.spindle_direction, SPINDLE_DIRECTIONS, (v) => {
      m.spindle_direction = v;
      onStructure(); // the viewer's rotation arrow has to redraw
    })
  );

  const note = el("span", "unit",
    "Which way the mandrel turns. Depends on which side of the carriage the " +
    "payout eye sits on, so it varies between machines.");
  note.style.flexBasis = "100%";
  host.append(note);
}

// --- settings: axis letter aliases ----------------------------------------

/**
 * Let each functional axis be spelled with a different G-code letter.
 *
 * Filament winders have no standard lettering, so the generator emits
 * functional quantities and this decides how they are written. Defaults are
 * the lathe convention; aliasing is the escape hatch for a machine that
 * disagrees, until the fuller machine-profile work lands.
 */
export function renderAxisSettings(host, cfg, { onStructure }) {
  host.replaceChildren();
  const axes = cfg.machine.axes;

  for (const f of AXIS_FIELDS) {
    const wrap = el("div", "field");
    wrap.append(el("label", null, f.label));

    const input = document.createElement("input");
    input.type = "text";
    input.maxLength = 1;
    input.value = axes[f.key];
    input.style.width = "3.2em";
    input.style.textAlign = "center";
    input.style.fontFamily = "var(--mono)";
    input.setAttribute("aria-label", `${f.label} axis letter`);

    input.addEventListener("input", () => {
      const v = input.value.toUpperCase();
      // A G-code word is one letter; reject anything else rather than emitting
      // output no controller can parse.
      if (/^[A-Z]$/.test(v)) {
        input.classList.remove("invalid");
        axes[f.key] = v;
        onStructure(); // viewer labels track these
      } else {
        input.classList.add("invalid");
      }
    });

    wrap.append(input, el("span", "unit", f.hint));
    host.append(wrap);
  }

  const reset = el("button", "btn", "Reset to lathe convention");
  reset.type = "button";
  reset.addEventListener("click", () => {
    Object.assign(axes, DEFAULT_AXES);
    onStructure();
  });
  const row = el("div", "field");
  row.style.flexBasis = "100%";
  row.append(reset);
  host.append(row);
}

// --- layers ---------------------------------------------------------------

export function renderLayers(host, cfg, { onChange, onStructure }) {
  host.replaceChildren();

  if (cfg.layers.length === 0) {
    const empty = el("div", "block");
    empty.style.justifyContent = "center";
    empty.append(el("span", "unit", "No layers yet — add one below."));
    host.append(empty);
    return;
  }

  cfg.layers.forEach((layer, i) => {
    host.append(layerBlock(cfg, layer, i, { onChange, onStructure }));
  });
}

function layerBlock(cfg, layer, index, { onChange, onStructure }) {
  const block = el("div", "block block-layer");
  block.dataset.type = layer.type;
  // nrepeat 0 means the layer is muted: kept, but contributing nothing. Grey
  // it out so that reads as a deliberate state rather than a broken value.
  if (!(layer.nrepeat > 0)) block.classList.add("disabled");
  block.dataset.index = String(index);
  block.draggable = true;

  const grip = el("span", "grip", "⠿");
  grip.title = "Drag to reorder";
  block.append(grip);

  block.append(el("span", "unit", `${index + 1}`));
  if (!(layer.nrepeat > 0)) {
    const off = el("span", "off-badge", "off");
    off.title = "Repeat is 0, so this layer is skipped. Set repeat to 1 or more to enable it.";
    block.append(off);
  }

  block.append(
    selectField(null, layer.type, LAYER_TYPES, (v) => {
      if (v === layer.type) return;
      // Preserve nrepeat across the switch; replace the type-specific params
      // with that type's defaults.
      const fresh = newLayer(v);
      fresh.nrepeat = layer.nrepeat;
      cfg.layers[index] = fresh;
      onStructure();
    })
  );

  if (layer.type === "hoop") {
    block.append(
      numberField("Stepover", layer, "stepover", {
        unit: "mm", min: 0, onChange, path: `layers.${index}.stepover`,
      })
    );
  } else {
    block.append(
      numberField("Angle", layer, "angle", {
        unit: "°", min: 0, max: 180, onChange, path: `layers.${index}.angle`,
      })
    );
  }

  block.append(
    numberField("Repeat", layer, "nrepeat", {
      min: 0, step: 1, path: `layers.${index}.nrepeat`,
      // Crossing the 0 boundary changes whether the layer is muted, which is a
      // visual state change, so re-render rather than just recompute.
      onChange: () => {
        const nowOff = !(layer.nrepeat > 0);
        const wasOff = block.classList.contains("disabled");
        if (nowOff !== wasOff) onStructure();
        else onChange();
      },
    })
  );

  block.append(el("span", "spacer-x"));

  const dup = el("button", "btn btn-icon", "⧉");
  dup.type = "button";
  dup.title = "Duplicate layer";
  dup.addEventListener("click", () => {
    cfg.layers.splice(index + 1, 0, structuredClone(layer));
    onStructure();
  });

  const del = el("button", "btn btn-icon btn-danger", "×");
  del.type = "button";
  del.title = "Delete layer";
  del.addEventListener("click", () => {
    cfg.layers.splice(index, 1);
    onStructure();
  });

  block.append(dup, del);
  return block;
}

/**
 * Wire drag-to-reorder on the layer list.
 *
 * Uses HTML5 drag events with the list container as the delegate, so it keeps
 * working after blocks are re-rendered. Called once at startup.
 */
export function enableReorder(listHost, cfg, onStructure) {
  let fromIndex = null;

  listHost.addEventListener("dragstart", (e) => {
    const block = e.target.closest(".block-layer");
    if (!block) return;
    fromIndex = Number(block.dataset.index);
    block.classList.add("dragging");
    e.dataTransfer.effectAllowed = "move";
    // Firefox requires data to be set or the drag never starts.
    e.dataTransfer.setData("text/plain", String(fromIndex));
  });

  listHost.addEventListener("dragend", () => {
    fromIndex = null;
    listHost.querySelectorAll(".block-layer").forEach((b) => {
      b.classList.remove("dragging", "drop-target");
    });
  });

  listHost.addEventListener("dragover", (e) => {
    if (fromIndex === null) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    const over = e.target.closest(".block-layer");
    listHost.querySelectorAll(".block-layer").forEach((b) => b.classList.remove("drop-target"));
    if (over) over.classList.add("drop-target");
  });

  listHost.addEventListener("drop", (e) => {
    if (fromIndex === null) return;
    e.preventDefault();
    const over = e.target.closest(".block-layer");
    if (!over) return;
    const toIndex = Number(over.dataset.index);
    if (toIndex === fromIndex) return;
    const [moved] = cfg.layers.splice(fromIndex, 1);
    cfg.layers.splice(toIndex, 0, moved);
    fromIndex = null;
    onStructure();
  });
}
