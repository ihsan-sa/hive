// The frame every drawing sits in: one tool row, a stage that pans and zooms, the source one press away, and a quiet note if the
// drawing fails, when the source simply stays. A spec's draw() runs only once the figure nears the screen, one at a time, when idle.
// What the person set (zoom, source showing) and the height drawn live here by source, so a re-opened thread comes back the same.
import { h, copyText } from "../core/dom.js";
import { lazyKind } from "./lazy.js";
import { sigOf } from "./math.js";
import { askButton } from "./ask.js";
import { cleanSvg, naturalSize } from "./sanitize.js";

const ST = new Map();
const stateOf = sig => { let s = ST.get(sig); if (!s) { ST.set(sig, s = { zoom: null, src: false, h: 0 }); if (ST.size > 400) ST.delete(ST.keys().next().value); } return s; };

// ---- redraw when the theme changes, for drawings whose colours are fixed when drawn. Nothing watches until one such figure exists. ----
const themed = new Set(); let watching = false, lastInk = "";
const inkNow = () => getComputedStyle(document.documentElement).getPropertyValue("--ink").trim() + document.documentElement.dataset.theme;
function themeCheck() {
  const k = inkNow(); if (k === lastInk) return; lastInk = k;
  for (const lz of [...themed]) { if (lz.isConnected) drawFig(lz); else themed.delete(lz); }
}
function watchTheme(lz) {
  themed.add(lz);
  if (watching) return; watching = true; lastInk = inkNow();
  new MutationObserver(themeCheck).observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  matchMedia("(prefers-color-scheme: dark)").addEventListener("change", themeCheck);
}

// ---- pan and zoom ----
function setZoom(c, z) {
  c.zoom = z; c.st.zoom = z;
  c.node.setAttribute("width", Math.round(c.w * z)); c.node.setAttribute("height", Math.round(c.h * z));
  if (c.pct) c.pct.textContent = Math.round(z * 100) + "%";
}
const fitZoom = (c, stage) => {
  const full = stage.closest(".rc-fig")?.hasAttribute("data-full");
  const zw = (stage.clientWidth - 26) / c.w, zh = (stage.clientHeight - 26) / c.h;
  return Math.max(0.1, full ? Math.min(zw, zh, 4) : Math.min(zw, 1));
};
function pannable(stage) {
  let at = null;
  stage.addEventListener("pointerdown", e => {
    if (e.pointerType !== "mouse" || e.button) return;
    if (stage.scrollWidth <= stage.clientWidth && stage.scrollHeight <= stage.clientHeight) return;
    at = { x: e.clientX, y: e.clientY, l: stage.scrollLeft, t: stage.scrollTop }; stage.dataset.drag = ""; stage.setPointerCapture(e.pointerId);
  });
  stage.addEventListener("pointermove", e => { if (at) { stage.scrollLeft = at.l - e.clientX + at.x; stage.scrollTop = at.t - e.clientY + at.y; } });
  const end = () => { at = null; delete stage.dataset.drag; };
  stage.addEventListener("pointerup", end); stage.addEventListener("pointercancel", end);
}

// ---- drawing ----
async function drawFig(lz) {
  const box = lz.closest(".rc-fig"), spec = lz._spec; if (!box || !spec) return;
  const st = stateOf(box.dataset.sig), note = box.querySelector(":scope>.rc-fig-note");
  const keep = lz.querySelector(":scope>.rc-stage,:scope>.rc-own");
  if (spec.themed) watchTheme(lz);
  const fail = why => {
    lz.dataset.failed = ""; box.dataset.failed = "";
    const srcB = box.querySelector("[data-src]"); srcB?.setAttribute("hidden", "");
    box.querySelectorAll("[data-zoom]").forEach(b => b.setAttribute("hidden", ""));
    note.hidden = false; note.textContent = "Couldn't draw this, so this is the source." + (why ? " " + String(why).slice(0, 180) : "");
    if (keep) lz.replaceChildren(lz._source); lz.style.minHeight = "";
  };
  let out;
  try { out = await spec.draw({ box, lz, state: st }); }
  catch (e) { return fail(e?.message || e); }
  if (!lz.isConnected) return;
  if (!out) return fail("");
  delete lz.dataset.failed; delete box.dataset.failed; note.hidden = true;
  box.querySelector("[data-src]")?.removeAttribute("hidden");
  if (out.own) lz.replaceChildren(h("div", { class: "rc-own" }, out.own));  // draws and sizes itself (a table, a plot with sliders)
  else {
    const { w, h: ht } = naturalSize(out.svg);
    const stage = h("div", { class: "rc-stage", tabindex: "0", role: "img", "aria-label": spec.alt || spec.title || (spec.kind + " drawing") }, out.svg);
    const c = lz._c = { node: out.svg, w, h: ht, st, pct: box.querySelector("[data-pct]") };
    lz.replaceChildren(stage); pannable(stage);
    box.querySelectorAll("[data-zoom]").forEach(b => b.removeAttribute("hidden"));
    setZoom(c, st.zoom || fitZoom(c, stage));
  }
  if (st.src) showSource(box, lz, true);
  requestAnimationFrame(() => { lz.style.minHeight = ""; st.h = lz.offsetHeight || st.h; });
}
lazyKind("fig", drawFig);

function showSource(box, lz, on) {
  const pre = lz._sourceBox ||= h("div", { class: "rc-fig-srcb" }, h("pre", { class: "rc-fig-src", tabindex: "0" }, lz._src));
  const drawn = lz.querySelector(":scope>.rc-stage,:scope>.rc-own");
  if (on) { if (drawn) drawn.hidden = true; if (!pre.isConnected) lz.append(pre); }
  else { pre.remove(); if (drawn) drawn.hidden = false; }
  box.querySelector("[data-src]")?.setAttribute("aria-pressed", String(on));
}

function escClose(e) {
  if (e.key !== "Escape") return;
  const f = document.querySelector(".rc-fig[data-full]"); if (f) f.querySelector("[data-fs]")?.click();
}

/** A figure block. spec: { kind, label, title, src, themed, alt, tools, ask, draw({box,lz,state}) -> {svg} or {own} or null }. */
export function figure(spec) {
  const sig = sigOf(spec.kind + "\0" + (spec.title || "") + "\0" + spec.src), st = stateOf(sig);
  const source = h("pre", { class: "rc-fig-src", tabindex: "0" }, spec.src);
  const lz = h("rc-lazy", { "data-kind": "fig", class: "rc-figl", style: `min-height:${st.h || spec.reserve || 120}px` }, source);
  lz._spec = spec; lz._src = spec.src; lz._source = source;
  const zb = (txt, label, f) => h("button", { type: "button", class: "rc-t", "data-zoom": "", hidden: true, "aria-label": label, onclick: f }, txt);
  const stageOf = () => lz.querySelector(":scope>.rc-stage");
  const step = k => () => { if (lz._c) setZoom(lz._c, Math.max(0.1, Math.min(8, lz._c.zoom * k))); };
  return h("div", { class: "rc-block rc-fig", "data-block": spec.kind, "data-own": "", "data-sig": sig },
    h("div", { class: "rc-bar" },
      h("span", { class: "rc-lang" }, spec.label || spec.kind),
      spec.title ? h("span", { class: "rc-ttl" }, spec.title) : null,
      h("span", { class: "rc-tools" },
        zb("−", "Zoom out", step(1 / 1.25)),
        h("button", { type: "button", class: "rc-t", "data-zoom": "", "data-pct": "", hidden: true, title: "Fit", "aria-label": "Fit to the frame", onclick: () => { if (lz._c && stageOf()) setZoom(lz._c, fitZoom(lz._c, stageOf())); } }, "Fit"),
        zb("+", "Zoom in", step(1.25)),
        h("button", { type: "button", class: "rc-t", "data-zoom": "", "data-fs": "", hidden: true, onclick: (e, b) => {
          const f = b.closest(".rc-fig"), on = !f.hasAttribute("data-full");
          f.toggleAttribute("data-full", on); b.textContent = on ? "Close" : "Full screen";
          if (on) document.addEventListener("keydown", escClose); else document.removeEventListener("keydown", escClose);
          if (lz._c && stageOf()) setZoom(lz._c, fitZoom(lz._c, stageOf()));
        } }, "Full screen"),
        ...(spec.tools || []),
        h("button", { type: "button", class: "rc-t", "data-src": "", "aria-pressed": String(st.src), onclick: (e, b) => {
          st.src = b.getAttribute("aria-pressed") !== "true"; showSource(b.closest(".rc-fig"), lz, st.src);
        } }, "Source"),
        h("button", { type: "button", class: "rc-t", "aria-label": "Copy this drawing's source", onclick: (e, b) => {
          copyText(spec.src).then(() => { b.dataset.ok = ""; b.textContent = "Copied"; setTimeout(() => { delete b.dataset.ok; b.textContent = "Copy"; }, 1400); }, () => {});
        } }, "Copy"),
        askButton(() => spec.ask?.() || "```" + spec.kind + "\n" + spec.src + "\n```", "Ask about this drawing", spec.title || spec.kind))),
    lz,
    h("div", { class: "rc-fig-note", hidden: true }));
}

// ---- the svg fence: over 200 kB it is only an image, which can't run or reach anything ----
const IMG_OVER = 200000, MAX = 2000000;
export const svgFigure = (code, { title }) => figure({
  kind: "svg", label: "svg", title, src: code, reserve: 160,
  draw: async () => {
    if (code.length > MAX) throw new Error("It's over 2 MB.");
    if (code.length > IMG_OVER) {
      const u = URL.createObjectURL(new Blob([code], { type: "image/svg+xml" }));
      return { own: h("img", { class: "rc-svgimg", src: u, alt: title || "Drawing" }) };
    }
    const svg = await cleanSvg(code);
    if (!svg) throw new Error("It has no drawing in it.");
    return { svg };
  },
});
