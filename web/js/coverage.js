// Coverage plot: deposited thickness against axial position.
//
// The question this answers is "is the layup even?" -- bulges show up where
// passes converge (typically turnarounds) and dips where the pattern leaves
// the surface under-covered. Both are invisible in the 3D path view, which
// shows where the tow went but not how much piled up.
//
// Drawn to a canvas rather than SVG: 200 stations x a few series is more
// elements than SVG wants to keep live, and this redraws on every edit.

// Margins follow matplotlib's convention: each element gets its own band, so
// nothing overlaps. Left = y-axis title + tick labels; bottom = x tick labels
// + x-axis title; top = plot title.
const PAD = { top: 26, right: 14, bottom: 48, left: 62 };
const TICK_GAP = 7;   // tick label to axis line
const TITLE_GAP = 15; // axis title to tick labels

function cssVar(name, fallback) {
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return v || fallback;
}

export function drawCoverage(canvas, cov) {
  const dpr = Math.min(window.devicePixelRatio || 1, 2);
  const w = canvas.clientWidth || 400;
  const h = canvas.clientHeight || 200;
  canvas.width = Math.round(w * dpr);
  canvas.height = Math.round(h * dpr);

  const ctx = canvas.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);

  if (!cov || !cov.x || cov.x.length === 0) return;

  const fg = cssVar("--fg-dim", "#97a1b0");
  const faint = cssVar("--fg-faint", "#6b7482");
  const border = cssVar("--border", "#2a313c");
  const accent = cssVar("--accent", "#5b9cff");
  const err = cssVar("--err", "#ff6b6b");

  const x0 = PAD.left, y0 = PAD.top;
  const pw = Math.max(w - PAD.left - PAD.right, 1);
  const ph = Math.max(h - PAD.top - PAD.bottom, 1);

  const xs = cov.x;
  const ys = cov.thickness;
  const xMin = xs[0], xMax = xs[xs.length - 1];
  let yMax = 0;
  for (const v of ys) if (v > yMax) yMax = v;
  if (yMax <= 0) yMax = 1;
  yMax *= 1.15; // headroom so the peak is not flush with the frame

  const px = (x) => x0 + ((x - xMin) / Math.max(xMax - xMin, 1e-9)) * pw;
  const py = (y) => y0 + ph - (y / yMax) * ph;

  // frame + gridlines
  ctx.strokeStyle = border;
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(x0, y0); ctx.lineTo(x0, y0 + ph); ctx.lineTo(x0 + pw, y0 + ph);
  ctx.stroke();

  ctx.font = "10px ui-monospace, monospace"; // tick labels
  ctx.fillStyle = faint;
  ctx.textAlign = "right";
  ctx.textBaseline = "middle";
  for (let i = 0; i <= 3; i++) {
    const v = (yMax * i) / 3;
    const y = py(v);
    ctx.fillText(v.toFixed(2), x0 - TICK_GAP, y);
    if (i > 0) {
      ctx.strokeStyle = border;
      ctx.globalAlpha = 0.5;
      ctx.beginPath(); ctx.moveTo(x0, y); ctx.lineTo(x0 + pw, y); ctx.stroke();
      ctx.globalAlpha = 1;
    }
  }
  ctx.textAlign = "center";
  ctx.textBaseline = "top";
  for (let i = 0; i <= 4; i++) {
    const v = xMin + ((xMax - xMin) * i) / 4;
    ctx.fillText(v.toFixed(0), px(v), y0 + ph + TICK_GAP);
  }

  // mean thickness reference line, so bulges and dips read against it
  let mean = 0;
  for (const v of ys) mean += v;
  mean /= ys.length;
  ctx.strokeStyle = faint;
  ctx.setLineDash([3, 3]);
  ctx.beginPath(); ctx.moveTo(x0, py(mean)); ctx.lineTo(x0 + pw, py(mean)); ctx.stroke();
  ctx.setLineDash([]);

  // filled thickness profile
  ctx.beginPath();
  ctx.moveTo(px(xs[0]), py(0));
  for (let i = 0; i < xs.length; i++) ctx.lineTo(px(xs[i]), py(ys[i]));
  ctx.lineTo(px(xs[xs.length - 1]), py(0));
  ctx.closePath();
  ctx.fillStyle = accent;
  ctx.globalAlpha = 0.18;
  ctx.fill();
  ctx.globalAlpha = 1;

  ctx.beginPath();
  for (let i = 0; i < xs.length; i++) {
    const X = px(xs[i]), Y = py(ys[i]);
    i === 0 ? ctx.moveTo(X, Y) : ctx.lineTo(X, Y);
  }
  ctx.strokeStyle = accent;
  ctx.lineWidth = 1.5;
  ctx.stroke();

  // mark stations with a bare sector -- a gap in the tiling, not just a thin spot
  ctx.fillStyle = err;
  for (let i = 0; i < xs.length; i++) {
    if (cov.minFraction[i] === 0) {
      ctx.fillRect(px(xs[i]) - 0.5, y0 + ph - 4, 1.5, 4);
    }
  }

  // --- axis titles ---------------------------------------------------------
  // Placed outside the plot area entirely: the y title is rotated and sits
  // left of its tick labels, the x title is centred below its own.
  ctx.fillStyle = fg;
  ctx.font = "11px system-ui, sans-serif";

  ctx.textAlign = "center";
  ctx.textBaseline = "top";
  ctx.fillText("axial position (mm)", x0 + pw / 2, y0 + ph + TICK_GAP + TITLE_GAP);

  ctx.save();
  ctx.translate(14, y0 + ph / 2);
  ctx.rotate(-Math.PI / 2);
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  ctx.fillText("deposited thickness (mm)", 0, 0);
  ctx.restore();

  // Plot title sits in the top margin, above the frame.
  ctx.textAlign = "left";
  ctx.textBaseline = "bottom";
  ctx.fillStyle = faint;
  ctx.fillText("layup thickness along the mandrel", x0, y0 - 8);
}
