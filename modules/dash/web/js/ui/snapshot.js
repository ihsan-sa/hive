// A picture of the page as it stands, with no browser prompt. The page is copied into an SVG image (foreignObject) with its own style sheets, so it
// keeps the same selectors, tokens and container queries, and the image is drawn on a canvas. A library like html2canvas re-implements CSS and can't read light-dark().
// Not in the picture: iframes, and WebGL canvases. A browser that can't do this (Safari taints or blanks it) throws, and the caller offers its other ways.
const LIMIT_MS = 4000;

function cssOf(sheet) {
  let out = "";
  for (const r of sheet.cssRules) out += (r instanceof CSSImportRule && r.styleSheet ? cssOf(r.styleSheet) : r.cssText) + "\n";
  return out;
}
function urlOf(src, w, hh) {   // a same-origin picture as a data: URL at the size it's shown: an SVG image can't load anything itself
  const c = document.createElement("canvas"); c.width = Math.max(1, Math.round(w)); c.height = Math.max(1, Math.round(hh));
  c.getContext("2d").drawImage(src, 0, 0, c.width, c.height);
  return c.toDataURL("image/png");
}
/** Show the element's scroll position in a copy that can't scroll. `translate` leaves any transform of its own alone and doesn't change layout. */
function keepScroll(from, to) {
  const x = from.scrollLeft, y = from.scrollTop; if (!x && !y) return;
  if ([...to.childNodes].some(n => n.nodeType === 3 && n.textContent.trim())) {   // bare text can't be moved, so it gets a box
    const box = document.createElement("div"); box.append(...to.childNodes); to.append(box);
    box.style.translate = `${-x}px ${-y}px`; return;
  }
  for (const c of to.children) c.style.translate = `${-x}px ${-y}px`;
}
/** A sticky element in a copy that can't scroll would sit where it started: put it where it's stuck now. It keeps its place in the flow and its stacking.
 *  All measured in one layout (a table can have hundreds of sticky cells), parents first, so a sticky inside a sticky moves only by its own part. */
function keepStuck(pairs) {
  const now = pairs.map(([el]) => el.getBoundingClientRect()), had = pairs.map(([el]) => el.getAttribute("style"));
  for (const [el] of pairs) { el.style.setProperty("position", "relative", "important"); el.style.setProperty("inset", "auto", "important"); }
  const from = pairs.map(([el]) => el.getBoundingClientRect());   // read and put back within this task, so it never paints
  pairs.forEach(([el], i) => { if (had[i] === null) el.removeAttribute("style"); else el.setAttribute("style", had[i]); });
  const total = new Map();
  pairs.forEach(([el, t], i) => {
    const x = now[i].left - from[i].left, y = now[i].top - from[i].top; total.set(el, [x, y]);
    let up = el.parentElement; while (up && !total.has(up)) up = up.parentElement;
    const [px, py] = up ? total.get(up) : [0, 0];
    t.style.position = "relative"; t.style.inset = "auto"; t.style.left = (x - px) + "px"; t.style.top = (y - py) + "px";
  });
}
/** The page's scroll moves the whole copy, which makes the copy's body what a fixed element is placed against: put it back where it is in the window. */
function keepFixed(cs, t, ox, oy) {
  t.style.inset = "auto"; t.style.width = cs.width; t.style.height = cs.height;
  t.style.left = (parseFloat(cs.left) + ox) + "px"; t.style.top = (parseFloat(cs.top) + oy) + "px";
}
function copyPage(scale) {
  const body = document.body, W = document.documentElement.clientWidth;
  const copy = body.cloneNode(true), all = [...body.querySelectorAll("*")], twins = [...copy.querySelectorAll("*")];
  const drop = [], stuck = [], b = body.getBoundingClientRect(), ox = -b.left - body.clientLeft, oy = -b.top - body.clientTop, moved = scrollX || scrollY;
  all.forEach((el, i) => {
    const t = twins[i];
    if (el.matches("script,noscript,dialog.fb,.fb-sent,.new-ver")) { drop.push(t); return; }
    if (el.scrollLeft || el.scrollTop) keepScroll(el, t);
    const cs = getComputedStyle(el);
    if (cs.position === "sticky") stuck.push([el, t]);
    else if (cs.position === "fixed" && moved) keepFixed(cs, t, ox, oy);
    if (el instanceof HTMLInputElement) {
      if (el.type === "password") t.removeAttribute("value");   // shown as dots on the page; not worth carrying into a picture at all
      else if (el.type === "checkbox" || el.type === "radio") { if (el.checked) t.setAttribute("checked", ""); else t.removeAttribute("checked"); }
      else t.setAttribute("value", el.value);
    } else if (el instanceof HTMLTextAreaElement) t.textContent = el.value;
    else if (el instanceof HTMLSelectElement) { const o = t.options[el.selectedIndex]; if (o) o.setAttribute("selected", ""); }
    else if (el instanceof HTMLImageElement) {
      t.removeAttribute("srcset"); t.removeAttribute("loading");
      const r = el.getBoundingClientRect(), seen = r.width && r.height && r.bottom > 0 && r.top < document.documentElement.clientHeight && r.right > 0 && r.left < W;
      try { if (seen && el.complete && el.naturalWidth) t.setAttribute("src", urlOf(el, r.width * scale, r.height * scale)); else t.removeAttribute("src"); }
      catch { t.removeAttribute("src"); }
    } else if (el instanceof HTMLCanvasElement) {
      const im = document.createElement("img"); for (const a of t.attributes) im.setAttribute(a.name, a.value);
      const r = el.getBoundingClientRect(); im.style.width = r.width + "px"; im.style.height = r.height + "px";
      try { if (el.width && el.height) im.src = el.toDataURL("image/png"); } catch {}
      t.replaceWith(im);
    }
  });
  if (stuck.length) keepStuck(stuck);
  for (const t of drop) t.remove();
  if (scrollX || scrollY) copy.style.translate = `${-scrollX}px ${-scrollY}px`;
  return copy;
}
const esc = s => s.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;");

/** A canvas of the window as it looks now, in device pixels (at most 2x). Throws if this browser can't. */
export async function snapshot() {
  const root = document.documentElement, W = root.clientWidth, H = root.clientHeight, scale = Math.min(devicePixelRatio || 1, 2);
  const css = [...document.styleSheets].map(cssOf).join("\n");
  const body = new XMLSerializer().serializeToString(copyPage(scale));
  // Root rules (:root, its data-theme and so on) land on the svg, which is the root in there, so it gets the page root's attributes and the resolved colour scheme.
  const attrs = [...root.attributes].filter(a => !/^(xmlns|style|lang)/.test(a.name)).map(a => ` ${a.name}="${esc(a.value)}"`).join("");
  const dark = matchMedia("(prefers-color-scheme: dark)").matches;
  const own = getComputedStyle(root).colorScheme, scheme = /^(light|dark)$/.test(own) ? own : dark ? "dark" : "light";
  const bg = getComputedStyle(document.body).backgroundColor;
  const still = "*,*::before,*::after{animation:none!important;transition:none!important;caret-color:transparent!important}";   // an image shows an animation's first frame: often invisible
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}"${attrs} style="color-scheme:${scheme}"><foreignObject x="0" y="0" width="${W}" height="${H}">`
    + `<html xmlns="http://www.w3.org/1999/xhtml" style="display:block;width:${W}px;height:${H}px;overflow:hidden;background:${bg}"><style>${esc(css + still)}</style>${body}</html></foreignObject></svg>`;
  const img = new Image(); img.src = "data:image/svg+xml;charset=utf-8," + encodeURIComponent(svg);
  await Promise.race([img.decode(), new Promise((_, no) => setTimeout(() => no(Error("it took too long")), LIMIT_MS))]);
  const c = document.createElement("canvas"); c.width = Math.round(W * scale); c.height = Math.round(H * scale);
  const x = c.getContext("2d"); x.drawImage(img, 0, 0, c.width, c.height);
  // Reading a pixel throws on a tainted canvas; a blank one (WebKit's first draw) has nothing there.
  if (!x.getImageData(c.width >> 1, c.height >> 1, 1, 1).data[3]) throw Error("the picture came back blank");
  return c;
}
