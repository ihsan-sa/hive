// An html fence as a live page the person opens: a sandboxed frame with an opaque origin and no network. It can't see this page,
// its cookie or the board. All it can do is send the two small messages below, and those only ever become numbers and short text.
import { h } from "../core/dom.js";
import { figure } from "./figure.js";
import { sigOf } from "./math.js";

const MAX = 1000000, MAX_H = 1200, MIN_H = 80, MSG_MAX = 4000, ST = new Map();
const stateOf = k => { let s = ST.get(k); if (!s) { ST.set(k, s = { open: false, px: 0, values: null }); if (ST.size > 200) ST.delete(ST.keys().next().value); } return s; };

const CSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; form-action 'none'; base-uri 'none'";
const HELPER = `<script>(()=>{const up=m=>parent.postMessage(Object.assign({hive:1},m),"*");`
  + `const size=()=>up({type:"height",px:Math.ceil(document.documentElement.getBoundingClientRect().height)});`
  + `addEventListener("load",size);new ResizeObserver(size).observe(document.documentElement);`
  + `window.hive={params:values=>up({type:"params",values})};})();<\/script>`;
const isDark = () => {
  const c = getComputedStyle(document.documentElement).colorScheme;
  return c === "dark" || (c !== "light" && matchMedia("(prefers-color-scheme: dark)").matches);
};
const docOf = (code, dark) => `<!doctype html><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${CSP}">`
  + `<style>html{color-scheme:${dark ? "dark" : "light"}}body{margin:12px;font:14px/1.5 system-ui,sans-serif}</style>${HELPER}${code}`;

/** Numbers and short strings under short names; anything else is dropped. */
export function cleanValues(v) {
  if (!v || typeof v !== "object" || Array.isArray(v)) return null;
  const out = {};
  for (const k of Object.keys(v).slice(0, 20)) {
    const x = v[k];
    if (k.length > 40) continue;
    if ((typeof x === "number" && isFinite(x)) || (typeof x === "string" && x.length <= 100) || typeof x === "boolean") out[k] = x;
  }
  return Object.keys(out).length ? out : null;
}
const valuesText = v => Object.entries(v).map(([k, x]) => `${k} = ${x}`).join(", ");

// One listener for every frame. Only that frame's own window is believed, and only for these two shapes.
const FRAMES = new WeakMap(); let listening = false;
function onMessage(e) {
  const f = e.source && FRAMES.get(e.source), d = e.data;
  if (!f || e.origin !== "null" || !d || typeof d !== "object" || d.hive !== 1) return;
  try { if (JSON.stringify(d).length > MSG_MAX) return; } catch { return; }
  const now = performance.now(); if (now - f.t > 1000) { f.t = now; f.n = 0; } if (++f.n > 30) return;
  if (d.type === "height" && typeof d.px === "number" && isFinite(d.px)) f.height(d.px);
  else if (d.type === "params") { const v = cleanValues(d.values); if (v) f.params(v); }
}

function drawArtifact(code, title, st, box) {
  const wrap = h("div", { class: "rc-art" }), vals = h("div", { class: "rc-paramsn", hidden: !st.values }, st.values ? valuesText(st.values) : "");
  let frame = null, seen = null;
  const fsB = () => box.querySelector("[data-fs]");
  const size = kb => (kb < 1 ? "under 1" : Math.round(kb)) + " kB";
  const card = h("div", { class: "rc-artc" },
    h("button", { type: "button", class: "rc-artgo", onclick: () => open() }, "Open"),
    h("span", null, `Interactive page, ${size(code.length / 1024)}. It runs sealed off from this dashboard and your login, with its network blocked.`));
  const close = h("button", { type: "button", class: "rc-t", "data-artclose": "", onclick: () => shut() }, "Close page");

  function build() {
    if (frame) return;
    frame = document.createElement("iframe");
    // Set before srcdoc and before it joins the page, so it never runs unsandboxed.
    frame.setAttribute("sandbox", "allow-scripts");
    frame.setAttribute("referrerpolicy", "no-referrer");
    frame.setAttribute("allow", "");
    frame.title = title || "Interactive page";
    const dark = isDark();
    frame.style.colorScheme = dark ? "dark" : "light";
    frame.style.height = (st.px || 240) + "px";
    frame.srcdoc = docOf(code, dark);
    wrap.prepend(frame);
    if (!listening) { listening = true; addEventListener("message", onMessage); }
    FRAMES.set(frame.contentWindow, {
      t: 0, n: 0,
      height: px => { st.px = Math.max(MIN_H, Math.min(MAX_H, Math.ceil(px))); if (frame) frame.style.height = st.px + "px"; },
      params: v => { st.values = v; vals.textContent = valuesText(v); vals.hidden = false; },
    });
  }
  const destroy = () => { frame?.remove(); frame = null; };
  // A frame well off screen is taken down and comes back when it's near again, so a long thread doesn't keep pages running.
  function watch() {
    seen ||= new IntersectionObserver(es => {
      if (!st.open) return;
      if (es[es.length - 1].isIntersecting) build(); else destroy();
    }, { rootMargin: "1500px" });
    seen.observe(wrap);
  }
  function open() {
    st.open = true; card.hidden = true; close.hidden = false;
    const f = fsB(); if (f) f.hidden = false;
    build(); watch();
  }
  function shut() {
    st.open = false; st.px = 0; destroy(); seen?.disconnect();
    card.hidden = false; close.hidden = true;
    const f = fsB(); if (f) { if (box.hasAttribute("data-full")) f.click(); f.hidden = true; }
  }
  close.hidden = true;
  box.querySelector(".rc-tools")?.prepend(close);
  wrap.append(card, vals);
  if (st.open) requestAnimationFrame(open);
  return wrap;
}

export function artifactFigure(code, { lang, title }) {
  const st = stateOf(sigOf(lang + "\0" + code));
  return figure({
    kind: lang, label: lang, title, src: code, reserve: 60,
    ask: () => "```" + lang + "\n" + code + "\n```" + (st.values ? "\n\nWith " + valuesText(st.values) : ""),
    draw: ({ box }) => {
      if (code.length > MAX) throw new Error("It's over 1 MB.");
      return { own: drawArtifact(code, title, st, box) };
    },
  });
}
