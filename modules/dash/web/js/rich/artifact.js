// Navigation: the frame can't send itself to an outside URL because the app page's own CSP has frame-src 'self' (no navigate-to exists);
// drive_rich checks it, and fails if the page is served without that CSP (use test/serve_csp.py).
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

// ---- versions: an agent changing a page it posted posts the whole page again, with the same title in the same chat.
// The newest copy is the page, with ‹ n / m › back to the older ones; an older copy says a newer one is further down.
// The chat view builds the series from the pages it shows (artifactSeries), so a reply hidden behind Edit or Retry arrows is not a version.
const SERIES = new Map(), COPY = new Map(), LIVE = new Set();
let CUR = null, VIEW = null, COLLECT = null;
// A page's name: its fence title=, else its own <title> or first heading. A page with none is not part of a series.
const plain = s => s.replace(/<[^>]*>/g, " ").replace(/&amp;/g, "&").replace(/&[a-z#0-9]+;/gi, " ").replace(/\s+/g, " ").trim().toLowerCase();
// A page's names: the fence's title= and the page's own <title> or first heading, in one namespace, so a page first posted bare and then with title= of the same words chains.
const inner = code => { const m = code.match(/<title[^>]*>([\s\S]*?)<\/title>/i) || code.match(/<h[1-3][^>]*>([\s\S]*?)<\/h[1-3]>/i); return (m && plain(m[1])) || null; };
const namesOf = (title, code) => ({ t: (title && plain(title)) || null, n: inner(code) });
const copyKey = (id, code) => id + "\0" + code;
/** The html pages in posts' markdown, as [{ id, title, code }], found by the same parser that draws them. md: the markdown function; each item: [id, text]. */
export function collectPages(md, items) {
  const out = [];
  for (const [id, text] of items) {
    if (!/```|~~~/.test(text) || !/html/i.test(text)) continue;
    COLLECT = []; try { md(text); } finally { for (const c of COLLECT) out.push({ id, ...c }); COLLECT = null; }
  }
  return out;
}
/** Set chat th's pages (in the order shown), as versions by name. Drawn copies repaint their version bar. */
export function artifactSeries(th, pages) {
  // A series keeps the explicit titles and inner names it has seen. Two different explicit titles never chain; otherwise any shared name does.
  const all = [], by = new Map();
  const find = ({ t, n }) => {
    for (let i = all.length - 1; i >= 0; i--) {
      const s = all[i];
      if (t && s.titles.has(t)) return s;
      if (t && s.titles.size) continue;
      if ((t && s.inners.has(t)) || (n && (s.inners.has(n) || s.titles.has(n)))) return s;
    }
    return null;
  };
  for (const pg of pages) {
    const nm = namesOf(pg.title, pg.code); if (!nm.t && !nm.n) continue;
    let s = find(nm);
    if (!s) { s = { name: nm.t || nm.n, titles: new Set(), inners: new Set(), codes: [] }; all.push(s); }
    if (nm.t) s.titles.add(nm.t); if (nm.n) s.inners.add(nm.n);
    const l = s.codes; if (!l.length || l[l.length - 1] !== pg.code) l.push(pg.code);
    pg.key = th + "\0" + all.indexOf(s) + "\0" + s.name; pg.at = l.length - 1;
  }
  for (const [i, s] of all.entries()) by.set(i + "\0" + s.name, s.codes);
  for (const [k, c] of [...COPY]) if (c.th === th) COPY.delete(k);
  for (const k of [...SERIES.keys()]) if (k.startsWith(th + "\0")) SERIES.delete(k);
  for (const [n, l] of by) if (l.length > 1) SERIES.set(th + "\0" + n, l);
  for (const pg of pages) if (pg.key && SERIES.has(pg.key)) COPY.set(copyKey(pg.id, pg.code), { th, key: pg.key, at: pg.at });
  for (const f of [...LIVE]) { if (f.gone()) f.drop(); else if (f.th === th) f.paint(); }
}
/** Draw the chat view of th: posts drawn inside fn may carry version bars (elsewhere, such as search or an agent's page, they don't). */
export function chatView(th, fn) { const o = VIEW; VIEW = th; try { return fn(); } finally { VIEW = o; } }
/** Draw post id's markdown (fn) as part of chat th, so its pages know their versions. */
// A post the chat view drew and later redraws by itself (Show the middle, Back to the post) keeps its bar while that chat is open.
const DRAWN = new Set(), chatOpen = th => /^#comms\//.test(location.hash) && decodeURIComponent(location.hash).includes(th);
export function inChat(th, id, fn) {
  if (VIEW === th) { DRAWN.add(id); if (DRAWN.size > 2000) DRAWN.delete(DRAWN.values().next().value); }
  else if (VIEW || !DRAWN.has(id) || !chatOpen(th)) return fn();
  const o = CUR; CUR = { th, id }; try { return fn(); } finally { CUR = o; } }

function drawArtifact(code, title, st, box, ver) {
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
  if (ver) {
    const bar = h("div", { class: "rc-artv", hidden: true });
    const older = h("button", { type: "button", class: "msg-ver-b", "aria-label": "Older version of this page", onclick: () => go(-1) }, "‹");
    const newer = h("button", { type: "button", class: "msg-ver-b", "aria-label": "Newer version of this page", onclick: () => go(1) }, "›");
    const count = h("span", { class: "meta", "aria-live": "polite" }), say = h("span"), nav = h("span", { class: "msg-ver-n", role: "group" }, older, count, newer);
    bar.append(nav, say);
    let pick = null, cur = 0, n = 0;   // pick: which version this newest copy shows; null is the newest
    const live = { th: ver.th, gone: () => ver.drawn && !wrap.isConnected, paint, drop: () => { seen?.disconnect(); LIVE.delete(live); } };
    function go(d) { const j = cur + d; if (j < 0 || j >= n) return; pick = j === n - 1 ? null : j; paint(); }
    function paint() {
      const c = COPY.get(copyKey(ver.id, ver.code)), l = c ? SERIES.get(c.key) || [] : [];
      n = l.length;
      if (!c || n < 2) { bar.hidden = true; if (code !== ver.code) swap(ver.code); return; }
      bar.hidden = false;
      if (c.at < n - 1) {   // an older copy: the page changed later in this chat
        if (code !== ver.code) swap(ver.code);
        nav.hidden = true; say.textContent = `Version ${c.at + 1} of ${n}. This page was changed later in this chat; the newest version is further down.`;
        return;
      }
      cur = pick == null || pick >= n ? n - 1 : pick;
      if (l[cur] !== code) swap(l[cur]);
      nav.hidden = false; nav.setAttribute("aria-label", `Version ${cur + 1} of ${n} of this page`);
      older.disabled = cur === 0; newer.disabled = cur === n - 1; count.textContent = `${cur + 1} / ${n}`;
      say.textContent = cur === n - 1 ? "Newest version of this page." : `An older version (${cur + 1} of ${n}), as it was then.`;
    }
    function swap(c) {   // show another version in this same frame; Ask and View source follow it
      code = ver.shown = c; card.lastChild.textContent = `Interactive page, ${size(code.length / 1024)}. It runs sealed off from this dashboard and your login, with its network blocked.`;
      st.values = null; vals.hidden = true; vals.textContent = "";
      for (const s of box.querySelectorAll(".rc-fig-src")) s.textContent = c;
      if (frame) { destroy(); if (st.open) build(); }
    }
    wrap.prepend(bar); ver.drawn = true; LIVE.add(live);
    if (LIVE.size > 300) for (const f of LIVE) { if (LIVE.size <= 300) break; if (f.gone()) f.drop(); }
    paint();
  }
  if (st.open) requestAnimationFrame(open);
  return wrap;
}

export function artifactFigure(code, { lang, title }) {
  if (COLLECT) { COLLECT.push({ title: title || "", code }); return h("div"); }
  const st = stateOf(sigOf(lang + "\0" + code)), ver = CUR ? { th: CUR.th, id: CUR.id, code, shown: code } : null;
  return figure({
    kind: lang, label: lang, title, src: code, reserve: 60,
    ask: () => { const c = ver ? ver.shown : code; return "```" + lang + "\n" + c + "\n```" + (st.values ? "\n\nWith " + valuesText(st.values) : ""); },
    draw: ({ box }) => {
      if (code.length > MAX) throw new Error("It's over 1 MB.");
      return { own: drawArtifact(code, title, st, box, ver) };
    },
  });
}
