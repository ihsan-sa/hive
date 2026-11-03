// A drawing from a post or a library, made safe to adopt: parsed inert, DOMPurify's SVG profile, then our own stricter pass.
// What survives is shapes, text and internal references. No script, foreignObject, <style>, animation, link or outside address;
// ids get a prefix so two drawings can't collide with each other or with the page.
const url = new URL("../../vendor/dompurify/purify.min.js", import.meta.url).href;
let loading = null;
const purify = () => loading ||= import(url).then(() => self.DOMPurify);

const DROP = new Set(["script", "foreignobject", "style", "animate", "animatetransform", "animatemotion", "set", "mpath", "iframe", "object", "embed", "audio", "video", "canvas", "discard", "handler", "listener"]);
export const STYLE_PROPS = ["fill", "fill-opacity", "fill-rule", "stroke", "stroke-width", "stroke-opacity", "stroke-dasharray", "stroke-linecap", "stroke-linejoin", "opacity",
  "font-family", "font-size", "font-weight", "font-style", "text-anchor", "dominant-baseline", "alignment-baseline", "display", "visibility", "letter-spacing", "white-space"];
const KEEP_STYLE = new Set(STYLE_PROPS);
const badValue = v => /url\(\s*(?!['"]?#)|expression|javascript:|@import|\\/i.test(v);
const safeImage = /^data:image\/(?:png|jpe?g|webp|gif);base64,[a-z0-9+/=\s]+$/i;
let seq = 0;

function cleanStyle(v) {
  return v.split(";").map(d => {
    const i = d.indexOf(":"); if (i < 0) return null;
    const k = d.slice(0, i).trim().toLowerCase(), x = d.slice(i + 1).trim();
    return KEEP_STYLE.has(k) && !badValue(x) ? k + ":" + x : null;
  }).filter(Boolean).join(";");
}

/** The SVG text as a clean element in this document, or null. Throws nothing the caller has to know about. */
export async function cleanSvg(text) {
  const P = await purify();
  const frag = P.sanitize(text, { USE_PROFILES: { svg: true, svgFilters: true }, RETURN_DOM_FRAGMENT: true, FORBID_TAGS: [...DROP] });
  const svg = frag.querySelector("svg");
  if (!svg) return null;
  const all = [svg, ...svg.querySelectorAll("*")], ids = new Map(), pre = "rcg" + (++seq) + "-";
  for (const el of all) {
    if (!el.isConnected && el !== svg && !svg.contains(el)) continue;
    const n = el.localName.toLowerCase();
    if (DROP.has(n)) { el.remove(); continue; }
    if (n === "a") { el.replaceWith(...el.childNodes); continue; }
    for (const a of [...el.attributes]) {
      const k = a.name.toLowerCase(), v = a.value;
      if (k.startsWith("on") || k === "src" || k === "action" || k === "formaction" || k === "ping" || k === "target" || k === "begin" || k === "end") el.removeAttribute(a.name);
      else if (k === "href" || k === "xlink:href") { if (!(v.trim().startsWith("#") || (n === "image" && safeImage.test(v.trim())))) el.removeAttribute(a.name); }
      else if (k === "style") { const c = cleanStyle(v); if (c) el.setAttribute("style", c); else el.removeAttribute("style"); }
      else if (badValue(v)) el.removeAttribute(a.name);
      else if (k === "id") ids.set(v, pre + v);
    }
    if (n === "image" && !el.hasAttribute("href") && !el.hasAttribute("xlink:href")) el.remove();
  }
  const refs = v => v.replace(/url\(\s*['"]?#([^'")\s]+)['"]?\s*\)/g, (m, id) => (ids.has(id) ? `url(#${ids.get(id)})` : "none"));
  for (const el of [svg, ...svg.querySelectorAll("*")]) for (const a of [...el.attributes]) {
    const k = a.name.toLowerCase();
    if (k === "id") a.value = ids.get(a.value) || pre + a.value;
    else if (k === "href" || k === "xlink:href") { if (a.value.trim().startsWith("#")) { const id = a.value.trim().slice(1); if (ids.has(id)) a.value = "#" + ids.get(id); else el.removeAttribute(a.name); } }
    else if (k === "aria-labelledby" || k === "aria-describedby") a.value = a.value.split(/\s+/).map(i => ids.get(i) || "").filter(Boolean).join(" ");
    else if (a.value.includes("url(")) a.value = refs(a.value);
  }
  return document.importNode(svg, true);
}

/** Size a drawing from its own numbers: viewBox first, then width and height. */
export function naturalSize(svg) {
  const vb = (svg.getAttribute("viewBox") || "").trim().split(/[\s,]+/).map(Number);
  let w = vb.length === 4 ? vb[2] : parseFloat(svg.getAttribute("width")), h = vb.length === 4 ? vb[3] : parseFloat(svg.getAttribute("height"));
  if (!(w > 0) || !(h > 0)) { w = 600; h = 300; }
  if (vb.length !== 4) svg.setAttribute("viewBox", `0 0 ${w} ${h}`);
  svg.removeAttribute("style"); svg.removeAttribute("x"); svg.removeAttribute("y");
  svg.classList?.add("rc-svg");
  return { w, h };
}
