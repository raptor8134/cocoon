// Application wiring: state, file handling, and the block <-> JSON toggle.

import { initCore, parseConfig, canonicalText, generate } from "./wasm.js";
import { defaultConfig, normalize, prune, quickCheck } from "./model.js";
import { renderMandrel, renderFilament, renderMachine, renderAxisSettings, renderLayers, enableReorder } from "./blocks.js";
import { Viewer } from "./viewer.js";
import { drawCoverage } from "./coverage.js";
import * as store from "./store.js";

const $ = (sel) => document.querySelector(sel);

const state = {
  cfg: defaultConfig(),
  name: "untitled.json",
  dirty: false,
  mode: "blocks",     // "blocks" | "json"
  handle: null,       // FileSystemFileHandle when the browser supports it
  viewer: null,
  lastGcode: "",
  lastCoverage: null,
};

// --- status ---------------------------------------------------------------

function setStatus(text, kind = "ok") {
  const bar = $("#status");
  bar.textContent = text;
  bar.dataset.state = kind;
}

function setStats(s) {
  $("#stats").textContent = s
    ? `${s.layers} ${s.layers === 1 ? "layer" : "layers"} · ${s.points.toLocaleString()} pts · ${s.vertices.toLocaleString()} verts · ${s.ms.toFixed(1)} ms`
    : "";
}

function setDirty(v) {
  state.dirty = v;
  $("#doc-dirty").hidden = !v;
}

function setName(n) {
  state.name = n;
  $("#doc-title").textContent = n;
}

// --- regeneration ---------------------------------------------------------

let regenTimer = null;

/**
 * Recompute paths, G-code, and the 3D view from the current config.
 *
 * Debounced because it runs on every keystroke in a number field. Unlike the
 * old Cogent Core version this is genuinely cheap to call -- the heavy work
 * is in wasm and the result is handed over as one typed array.
 */
function scheduleRegen(delay = 120) {
  clearTimeout(regenTimer);
  regenTimer = setTimeout(regenerate, delay);
}

function regenerate() {
  const issues = quickCheck(state.cfg);
  if (issues.length > 0) {
    setStatus(issues[0].message, "error");
    setStats(null);
    return;
  }

  let result;
  try {
    result = generate(prune(state.cfg));
  } catch (err) {
    // The Go core is authoritative on validity; its messages name the
    // parameter and the limit, so surface them verbatim.
    setStatus(err.message, "error");
    setStats(null);
    return;
  }

  state.lastCoverage = result.coverage;
  renderCoverage(result.coverage);
  state.lastGcode = result.gcode;
  $("#gcode-out").textContent = result.gcode;
  $("#gcode-hint").textContent = `${result.gcode.split("\n").length - 1} lines`;
  setStats(result.stats);
  const ax = state.cfg.machine?.axes;
  if (ax) state.viewer?.setAxisLetters(ax);
  state.viewer?.setSpindleReversed(state.cfg.machine?.spindle_direction === "reverse");
  state.viewer?.update(result);
  setStatus("no errors", "ok");
}

/** A value changed but the structure did not: regenerate without re-rendering. */
function onValueChange() {
  setDirty(true);
  saveDraft();
  if (state.mode === "json") syncJsonFromModel();
  scheduleRegen();
}

/** The structure changed: rebuild the blocks, then regenerate. */
function onStructureChange() {
  setDirty(true);
  saveDraft();
  renderAll();
  scheduleRegen(0);
}

function renderCoverage(cov) {
  const canvas = $("#cov-plot");
  if (canvas) drawCoverage(canvas, cov);

  const mm = (v) => `${v.toFixed(3)} mm`;
  $("#cov-summary").textContent =
    `peak ${mm(cov.maxThickness)} · fiber ${(cov.fiberLength / 1000).toFixed(1)} m`;

  const warn = $("#cov-warn");
  if (cov.gapStations > 0) {
    warn.hidden = false;
    warn.textContent =
      `${cov.gapStations} of ${cov.x.length} stations have at least one uncovered sector. ` +
      `Reduce stepover, add repeats, or widen the tow to close the gaps.`;
  } else {
    warn.hidden = true;
  }
}

// --- rendering ------------------------------------------------------------

function renderAll() {
  const hooks = { onChange: onValueChange, onStructure: onStructureChange };
  renderMandrel($("#mandrel-block"), state.cfg, hooks);
  renderFilament($("#filament-block"), state.cfg, hooks);
  renderMachine($("#machine-block"), state.cfg, hooks);
  renderAxisSettings($("#axes-block"), state.cfg, hooks);
  renderLayers($("#layer-list"), state.cfg, hooks);
  $("#layer-count").textContent =
    state.cfg.layers.length === 1 ? "1 layer" : `${state.cfg.layers.length} layers`;
  if (state.mode === "json") syncJsonFromModel();
}

function syncJsonFromModel() {
  const ta = $("#json-text");
  if (document.activeElement === ta) return; // never fight the user's caret
  try {
    ta.value = canonicalText(prune(state.cfg));
  } catch {
    ta.value = JSON.stringify(prune(state.cfg), null, 2);
  }
}

// --- mode toggle ----------------------------------------------------------

function setMode(mode) {
  state.mode = mode;
  $("#mode-blocks").classList.toggle("active", mode === "blocks");
  $("#mode-json").classList.toggle("active", mode === "json");
  $("#blocks-view").hidden = mode !== "blocks";
  $("#json-view").hidden = mode !== "json";
  if (mode === "json") syncJsonFromModel();
  else renderAll();
}

/**
 * Adopt edits made in the JSON view.
 *
 * Parsing goes through the Go core so the text view accepts the same JSON5 the
 * files on disk use -- comments, unquoted keys, trailing commas. Invalid text
 * only reports; it never clobbers the model, so a half-typed edit cannot
 * destroy the config behind the blocks view.
 */
function adoptJsonEdits() {
  const text = $("#json-text").value;
  let parsed;
  try {
    parsed = parseConfig(text);
  } catch (err) {
    setStatus(err.message, "error");
    return false;
  }
  state.cfg = normalize(parsed);
  setDirty(true);
  saveDraft();
  scheduleRegen();
  return true;
}

// --- persistence ----------------------------------------------------------

let draftTimer = null;

function saveDraft() {
  clearTimeout(draftTimer);
  draftTimer = setTimeout(() => {
    store.saveDraft({ name: state.name, cfg: state.cfg, dirty: state.dirty });
  }, 400);
}

async function restoreDraft() {
  const d = await store.loadDraft();
  if (!d) return false;
  state.cfg = normalize(d.cfg);
  setName(d.name || "untitled.json");
  setDirty(!!d.dirty);
  return true;
}

// --- file actions ---------------------------------------------------------

const hasFSA = typeof window.showSaveFilePicker === "function";

const PICKER_TYPES = [{
  description: "Wind configuration",
  accept: { "application/json": [".json"] },
}];

async function doOpen() {
  if (hasFSA) {
    let handle;
    try {
      [handle] = await window.showOpenFilePicker({ types: PICKER_TYPES, multiple: false });
    } catch (err) {
      if (err.name !== "AbortError") setStatus(`Open failed: ${err.message}`, "error");
      return;
    }
    const file = await handle.getFile();
    loadText(await file.text(), file.name);
    state.handle = handle; // remember it so Save writes back here
    return;
  }
  // Fallback for browsers without the File System Access API.
  $("#file-input").click();
}

function loadText(text, name) {
  let parsed;
  try {
    parsed = parseConfig(text);
  } catch (err) {
    setStatus(`Could not open ${name}: ${err.message}`, "error");
    return;
  }
  state.cfg = normalize(parsed);
  setName(name);
  setDirty(false);
  renderAll();
  scheduleRegen(0);
  saveDraft();
}

async function doSave(forceDialog) {
  const text = canonicalTextSafe();

  if (!hasFSA) {
    // ###################################################################
    // TEMPORARY FALLBACK -- this is a download, not a save.
    // ###################################################################
    // It cannot write back to the file the user opened; every save lands a
    // new copy in the downloads folder, and we get no confirmation that
    // anything happened. Present only because Firefox and Safari ship the
    // Origin Private File System but not the local-disk pickers. Delete
    // this branch when they do.
    downloadBlob(text, state.name, "application/json");
    setStatus("This browser cannot save to local files; downloaded a copy instead.", "error");
    return;
  }

  let handle = state.handle;
  if (forceDialog || !handle) {
    try {
      handle = await window.showSaveFilePicker({
        suggestedName: state.name,
        types: PICKER_TYPES,
      });
    } catch (err) {
      if (err.name !== "AbortError") setStatus(`Save failed: ${err.message}`, "error");
      return;
    }
  }

  try {
    const w = await handle.createWritable();
    await w.write(text);
    await w.close();
  } catch (err) {
    setStatus(`Save failed: ${err.message}`, "error");
    return;
  }

  state.handle = handle;
  setName(handle.name);
  setDirty(false);
  saveDraft();
  setStatus(`Saved ${handle.name}`, "ok");
}

function canonicalTextSafe() {
  try {
    return canonicalText(prune(state.cfg));
  } catch {
    return JSON.stringify(prune(state.cfg), null, 2);
  }
}

function downloadBlob(text, name, type) {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.append(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

// --- boot -----------------------------------------------------------------

async function main() {
  try {
    await initCore();
  } catch (err) {
    $("#boot").classList.add("done");
    setStatus(`Failed to load the winding core: ${err.message}`, "error");
    return;
  }

  state.viewer = new Viewer($("#viewport"));

  const restored = await restoreDraft();
  if (!restored) setName("untitled.json");

  renderAll();
  regenerate();

  enableReorder($("#layer-list"), state.cfg, onStructureChange);

  // tabs
  document.querySelectorAll(".tab").forEach((tab) => {
    tab.addEventListener("click", () => {
      document.querySelectorAll(".tab").forEach((t) => t.classList.toggle("active", t === tab));
      document.querySelectorAll(".tabpanel").forEach((p) => {
        p.classList.toggle("active", p.dataset.panel === tab.dataset.tab);
      });
      // A canvas in a display:none panel has zero size, so anything drawn
      // while it was hidden was drawn at 0x0. Redraw now that it has one.
      if (tab.dataset.tab === "coverage" && state.lastCoverage) {
        requestAnimationFrame(() => renderCoverage(state.lastCoverage));
      }
    });
  });

  // block / json toggle
  $("#mode-blocks").addEventListener("click", () => {
    if (state.mode === "json" && !adoptJsonEdits()) return; // keep bad text on screen
    setMode("blocks");
  });
  $("#mode-json").addEventListener("click", () => setMode("json"));

  // JSON view edits apply on input, debounced
  let jsonTimer = null;
  $("#json-text").addEventListener("input", () => {
    clearTimeout(jsonTimer);
    jsonTimer = setTimeout(adoptJsonEdits, 300);
  });

  // add-layer buttons
  document.querySelectorAll("[data-add]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const { newLayer } = await import("./model.js");
      state.cfg.layers.push(newLayer(btn.dataset.add));
      onStructureChange();
    });
  });

  // file actions
  $("#btn-new").addEventListener("click", () => {
    state.cfg = defaultConfig();
    state.handle = null;
    setName("untitled.json");
    setDirty(false);
    renderAll();
    scheduleRegen(0);
  });
  $("#btn-open").addEventListener("click", doOpen);
  $("#btn-save").addEventListener("click", () => doSave(false));
  $("#btn-saveas").addEventListener("click", () => doSave(true));

  $("#file-input").addEventListener("change", async (e) => {
    const file = e.target.files?.[0];
    if (file) loadText(await file.text(), file.name);
    e.target.value = "";
  });

  $("#btn-download-gcode").addEventListener("click", () => {
    const base = state.name.replace(/\.json$/i, "");
    downloadBlob(state.lastGcode, `${base}.gcode`, "text/plain");
  });

  // viewport controls
  $("#btn-home").addEventListener("click", () => state.viewer.goHome());
  $("#show-mandrel").addEventListener("change", (e) => state.viewer.setMandrelVisible(e.target.checked));
  $("#show-axes").addEventListener("change", (e) => state.viewer.setAxesVisible(e.target.checked));

  // Ctrl/Cmd+S saves rather than invoking the browser's page-save dialog.
  window.addEventListener("keydown", (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
      e.preventDefault();
      doSave(e.shiftKey);
    }
  });

  window.addEventListener("beforeunload", (e) => {
    if (!state.dirty) return;
    // The draft autosave means nothing is truly lost, but a surprise tab
    // close on unsaved work still deserves a confirmation.
    e.preventDefault();
    e.returnValue = "";
  });

  $("#boot").classList.add("done");
  setTimeout(() => $("#boot").remove(), 400);

  if (!hasFSA) {
    const note = $("#viewport-note");
    note.hidden = false;
    note.textContent =
      "This browser cannot save directly to local files (Chromium only). Save will download a copy instead.";
  }

  registerServiceWorker();
}

function registerServiceWorker() {
  if (!("serviceWorker" in navigator)) return;
  navigator.serviceWorker.register("sw.js").then((reg) => {
    const hadController = !!navigator.serviceWorker.controller;
    let reloading = false;
    navigator.serviceWorker.addEventListener("controllerchange", () => {
      // Only reload for an actual update, never on first registration.
      if (!hadController || reloading) return;
      reloading = true;
      location.reload();
    });
    setInterval(() => reg.update(), 60_000);
  }).catch((err) => console.warn("service worker registration failed", err));
}

main();
