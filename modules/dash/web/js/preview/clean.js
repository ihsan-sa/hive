// HTML a notebook printed (a pandas table, say), made inert with the vendored DOMPurify: no script, style, frame, form or
// outside address. Images keep only data: sources. What's left is plain markup adopted into the page.
const url = new URL("../../vendor/dompurify/purify.min.js", import.meta.url).href;
let loading = null;
const purify = () => loading ||= import(url).then(() => self.DOMPurify);

export async function cleanHtml(text) {
  const P = await purify();
  const frag = P.sanitize(text, { RETURN_DOM_FRAGMENT: true, FORBID_TAGS: ["style", "script", "iframe", "object", "embed", "form", "input", "button", "link", "meta", "base", "svg", "math"], FORBID_ATTR: ["style", "class", "id"] });
  for (const el of frag.querySelectorAll("[src],[href],[srcset],[action],[poster]")) {
    for (const a of ["srcset", "action", "poster"]) el.removeAttribute(a);
    if (el.hasAttribute("href")) el.removeAttribute("href");
    if (el.hasAttribute("src") && !/^data:image\/(png|jpe?g|gif|webp);base64,/i.test(el.getAttribute("src"))) el.removeAttribute("src");
  }
  const box = document.createElement("div"); box.className = "pv-html"; box.append(frag);
  return box;
}
