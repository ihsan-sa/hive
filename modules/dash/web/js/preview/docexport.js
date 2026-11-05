// Save a shown document as Markdown, Word or PDF, all in the browser: nothing is uploaded and the server gets no new route.
// Word gets the document as HTML in a .doc file (Word, Pages and Google Docs open it); PDF is the browser's print dialog on a
// clean copy of the document ("Save as PDF"), so it follows the phone's and the desktop's own PDF writer.
const CSS = "body{font:11pt/1.5 Georgia,'Times New Roman',serif;color:#111;max-width:46em;margin:2em auto;padding:0 1em}h1,h2,h3,h4{font-family:Helvetica,Arial,sans-serif;line-height:1.25;margin:1.3em 0 .4em}h1{font-size:20pt}h2{font-size:15pt;border-bottom:1px solid #ccc;padding-bottom:.15em}h3{font-size:12.5pt}table{border-collapse:collapse;margin:.8em 0;font:10pt Helvetica,Arial,sans-serif}th,td{border:1px solid #bbb;padding:3px 8px;text-align:left;vertical-align:top}th{background:#eee}pre,code{font-family:Consolas,Menlo,monospace;font-size:9.5pt}pre{background:#f3f3f3;padding:8px 10px;white-space:pre-wrap;border-radius:4px}code{background:#f3f3f3;padding:0 3px}blockquote{margin:.8em 0;padding-left:12px;border-left:3px solid #bbb;color:#444}a{color:#124}img{max-width:100%}";
const esc = s => String(s).replace(/[&<>"]/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
const base = name => name.replace(/\.[^.]+$/, "");
function page(title, body) {
  return `<!doctype html><html><head><meta charset="utf-8"><title>${esc(title)}</title><style>${CSS}</style></head><body>${body}</body></html>`;
}
function save(blob, name) {
  const a = document.createElement("a"); a.href = URL.createObjectURL(blob); a.download = name; document.body.append(a); a.click();
  setTimeout(() => { a.remove(); URL.revokeObjectURL(a.href); }, 4000);
}
/** The rendered document as plain HTML: buttons and the rich blocks' controls dropped, links kept. */
function cleanHtml(doc) {
  const c = doc.cloneNode(true);
  c.querySelectorAll("button,script,style,[hidden]").forEach(x => x.remove());
  c.querySelectorAll("[class],[data-key],[style]").forEach(x => { x.removeAttribute("class"); x.removeAttribute("style"); });
  return c.innerHTML;
}
export function saveMarkdown(name, text) { save(new Blob([text], { type: "text/markdown;charset=utf-8" }), base(name) + ".md"); }
export function saveWord(name, doc) { save(new Blob(["﻿", page(name, cleanHtml(doc))], { type: "application/msword" }), base(name) + ".doc"); }
export function savePdf(name, doc) {
  const f = document.createElement("iframe");
  f.setAttribute("aria-hidden", "true"); f.style.cssText = "position:fixed;right:0;bottom:0;width:0;height:0;border:0";
  f.onload = () => { try { f.contentWindow.focus(); f.contentWindow.print(); } finally { setTimeout(() => f.remove(), 60000); } };
  f.srcdoc = page(base(name), cleanHtml(doc));
  document.body.append(f);
}
