// Bridge to the Go winding core compiled to WebAssembly.
//
// The Go side (cmd/wasm) exposes window.cocoonCore with parse/canonical/
// generate. This module wraps it so the rest of the UI never touches raw
// js-interop shapes: results come back as plain objects with typed arrays
// already unpacked, and failures come back as thrown Errors rather than
// {error: "..."} objects that are easy to forget to check.

let ready = null;

/** Load and start the wasm core. Safe to call repeatedly; resolves once. */
export function initCore(wasmUrl = "cocoon.wasm") {
  if (ready) return ready;

  ready = new Promise((resolve, reject) => {
    if (typeof Go === "undefined") {
      reject(new Error("wasm_exec.js did not load; the Go runtime shim is missing"));
      return;
    }
    const go = new Go();

    // cocoonCoreReady is invoked from Go's main() once the API is installed.
    // Waiting on it (rather than on instantiate) guarantees the functions
    // exist before anything calls them.
    window.cocoonCoreReady = () => resolve();

    const run = (result) => {
      // go.run resolves only when the Go program exits, which for us means it
      // crashed -- main() blocks forever on purpose.
      go.run(result.instance).then(() => {
        console.error("cocoon: wasm core exited unexpectedly");
      });
    };

    if (WebAssembly.instantiateStreaming) {
      WebAssembly.instantiateStreaming(fetch(wasmUrl), go.importObject)
        .then(run)
        .catch(reject);
    } else {
      // Safari historically lacked instantiateStreaming, and it also fails if
      // the server sends the wrong MIME type for .wasm.
      fetch(wasmUrl)
        .then((r) => r.arrayBuffer())
        .then((b) => WebAssembly.instantiate(b, go.importObject))
        .then(run)
        .catch(reject);
    }
  });

  return ready;
}

function call(name, ...args) {
  const core = window.cocoonCore;
  if (!core) throw new Error("winding core is not loaded yet");
  const res = core[name](...args);
  if (res && res.error) throw new Error(res.error);
  return res;
}

/** Parse JSON5/JSON source into a plain object. Throws on invalid input. */
export function parseConfig(text) {
  return JSON.parse(call("parse", text).json);
}

/** Pretty-print a config object as canonical JSON text. */
export function canonicalText(obj) {
  return call("canonical", JSON.stringify(obj)).json;
}

/**
 * Generate G-code and render geometry for a config object.
 *
 * Returns { gcode, positions: Float32Array, layers, mandrel, stats }.
 * positions is a flat XYZ triple buffer covering every layer; each entry in
 * layers gives the {start, count} vertex range for one layer, so the viewer
 * can draw them as separately coloured line segments without copying.
 */
export function generate(obj, sourceName) {
  const res = call("generate", JSON.stringify(obj), sourceName ?? "(unsaved)");
  const cov = res.coverage;
  return {
    gcode: res.gcode,
    layers: res.layers,
    stats: res.stats,
    metrics: res.metrics,
    coverage: {
      x: asFloat64(cov.x),
      thickness: asFloat64(cov.thickness),
      fraction: asFloat64(cov.fraction),
      minFraction: asFloat64(cov.minFraction),
      gapStations: cov.gapStations,
      maxThickness: cov.maxThickness,
      worstFraction: cov.worstFraction,
      fiberLength: cov.fiberLength,
    },
    positions: asFloat32(res.positions),
    mandrel: {
      x: asFloat64(res.mandrel.x),
      r: asFloat64(res.mandrel.r),
      length: res.mandrel.length,
      zmax: res.mandrel.zmax,
      xmin: res.mandrel.xmin,
      xmax: res.mandrel.xmax,
    },
  };
}

// The Go side hands over Uint8Array views; reinterpret them without copying
// element by element.
function asFloat32(u8) {
  return new Float32Array(u8.buffer, u8.byteOffset, u8.byteLength / 4);
}
function asFloat64(u8) {
  return new Float64Array(u8.buffer, u8.byteOffset, u8.byteLength / 8);
}
