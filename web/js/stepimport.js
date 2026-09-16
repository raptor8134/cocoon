// STEP import review.
//
// The parser produces an interpretation, not an answer: which axis it chose,
// which surfaces it thinks form the mandrel, and what it rejected. This module
// puts that interpretation on screen -- as a list AND as pickable geometry in
// the 3D view -- and requires confirmation before any of it becomes the
// mandrel.
//
// That is deliberate. A wrong profile does not look wrong; it produces a
// plausible wind and a ruined part. Making the interpretation inspectable is
// cheaper than making the parser infallible.

import { drawCandidatePreview, clearCandidatePreview, highlightCandidate } from "./viewer.js";

const $ = (s) => document.querySelector(s);

/** Format bytes for the provenance panel. */
function humanSize(n) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

function el(tag, cls, text) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
}

/**
 * Show the review panel for a parsed STEP file.
 *
 * onAccept receives { segments, source } ready to become cfg.mandrel.
 */
export function showImportReview(result, viewer, onAccept) {
  const panel = $("#import-review");
  const body = $("#import-body");
  body.replaceChildren();
  panel.hidden = false;

  // Selection state starts as the parser's own verdict, so the common case is
  // "glance at it and press Use".
  const selected = new Set(
    result.candidates.filter((c) => !c.rejected).map((c) => c.id)
  );

  // --- provenance -------------------------------------------------------
  const h = result.header;
  const prov = el("div", "prov");
  const rows = [
    ["File", result.filename || "(unnamed)"],
    ["Size", humanSize(result.sizeBytes)],
    ["Modified", result.modified || "unknown"],
    ["Written by", h.originatingSystem || "unknown"],
    ["Authored", [h.author, h.organization].filter(Boolean).join(", ") || "unknown"],
    ["File's own date", h.timestamp || "unknown"],
    ["Schema", h.schema || "unknown"],
    ["Units", h.units],
    ["Axis found", result.axis],
  ];
  for (const [k, v] of rows) {
    const r = el("div", "prov-row");
    r.append(el("span", "prov-key", k), el("span", "prov-val", v));
    prov.append(r);
  }
  // The hash is machine identity, not something to read. It verifies that a
  // file is the same one; it is shown truncated with the full value on hover.
  const hashRow = el("div", "prov-row");
  const hv = el("span", "prov-val mono", result.hash.slice(0, 16) + "…");
  hv.title = result.hash;
  hashRow.append(el("span", "prov-key", "Content hash"), hv);
  prov.append(hashRow);
  body.append(prov);

  // --- candidate list ---------------------------------------------------
  body.append(el("h3", "import-h", "Surfaces"));
  const list = el("div", "cand-list");

  const redraw = () => {
    drawCandidatePreview(viewer, result.candidates, selected);
    const n = selected.size;
    $("#import-accept").disabled = n === 0;
    $("#import-count").textContent =
      n === 0 ? "nothing selected" : `${n} surface${n === 1 ? "" : "s"} selected`;
  };

  for (const c of result.candidates) {
    const row = el("label", "cand" + (c.rejected ? " cand-rejected" : ""));
    const box = document.createElement("input");
    box.type = "checkbox";
    box.dataset.id = String(c.id); // lets a viewport click find this row
    box.checked = selected.has(c.id);
    box.addEventListener("change", () => {
      box.checked ? selected.add(c.id) : selected.delete(c.id);
      row.classList.toggle("cand-on", box.checked);
      redraw();
    });
    // Hovering a row highlights the surface in 3D, which is what makes the
    // list and the geometry refer to each other.
    row.addEventListener("pointerenter", () => highlightCandidate(viewer, c.id));
    row.addEventListener("pointerleave", () => highlightCandidate(viewer, null));

    const main = el("div", "cand-main");
    main.append(
      el("span", "cand-name", c.name || c.type.toLowerCase().replace(/_/g, " ")),
      el("span", "cand-dims",
        `x ${c.axialMin.toFixed(1)}–${c.axialMax.toFixed(1)} mm · ` +
        `r ${c.radiusMin.toFixed(1)}–${c.radiusMax.toFixed(1)} mm`)
    );
    if (c.rejected) {
      main.append(el("span", "cand-why", c.rejected));
    }
    row.append(box, main);
    row.classList.toggle("cand-on", box.checked);
    list.append(row);
  }
  body.append(list);

  body.append(el("p", "cov-note",
    "Rejected surfaces are listed rather than hidden: seeing a bore or a divot " +
    "correctly identified is how you know the right surface was found. Tick one " +
    "to include it anyway."));

  redraw();

  const cleanup = () => {
    panel.hidden = true;
    clearCandidatePreview(viewer);
  };

  $("#import-cancel").onclick = cleanup;
  $("#import-accept").onclick = () => {
    const segments = result.candidates
      .filter((c) => selected.has(c.id))
      .sort((a, b) => a.axialMin - b.axialMin)
      .flatMap((c) => c.segments);

    onAccept({
      segments,
      source: {
        filename: result.filename,
        sha256: result.hash,
        modified: result.modified,
        schema: h.schema,
        originating_system: h.originatingSystem,
        units: h.units,
        imported: new Date().toISOString(),
        axis: result.axis,
        surfaces: result.candidates
          .filter((c) => selected.has(c.id))
          .map((c) => `#${c.id} ${c.name || c.type}`),
      },
    });
    cleanup();
  };
}
