// A file dropped on any text box. The composer takes it as an attachment and the feedback box as a picture, by their own handlers, which run first.
// What's left: a big text box gets the file's text at the cursor, and a one-line box gets its name, since a line can't hold a file.
// A drop anywhere else does nothing, where the browser would otherwise leave this page to show the file and lose what was typed.
const MAX_TEXT = 1048576;
const LINE = /^(text|search|url|email|tel)$/;
const hasFiles = e => e.dataTransfer && [...(e.dataTransfer.types || [])].includes("Files");
function box(e) {
  const n = e.target;
  if (!n || n.disabled || n.readOnly) return null;
  return n.tagName === "TEXTAREA" || (n.tagName === "INPUT" && LINE.test(n.type)) ? n : null;
}
let over = null;
const mark = n => { if (over === n) return; if (over) delete over.dataset.fileOver; over = n; if (n) n.dataset.fileOver = ""; };
function tell(n, text) {
  const home = n.closest("dialog") || document.body, say = document.createElement("p");
  say.className = "drop-say"; say.setAttribute("role", "alert"); say.textContent = text;
  home.append(say); setTimeout(() => say.remove(), 6000);
}
/** The file's text, or null with the reason said. A NUL in the first part means it isn't text. */
async function textOf(n, f) {
  if (f.size > MAX_TEXT) { tell(n, `${f.name} is over 1 MB, so it wasn't put in this box.`); return null; }
  try {
    const t = await f.text();
    if (t.slice(0, 8192).includes("\0")) { tell(n, `${f.name} isn't a text file, so it can't go in this box.`); return null; }
    return t;
  } catch { tell(n, `${f.name} couldn't be read.`); return null; }
}
async function put(n, files) {
  const parts = [];
  if (n.tagName === "TEXTAREA") for (const f of files) { const t = await textOf(n, f); if (t !== null) parts.push(t); }
  else parts.push(files.map(f => f.name).join(" "));
  if (!parts.length || !n.isConnected) return;
  n.focus();
  n.setRangeText(parts.join("\n"), n.selectionStart ?? n.value.length, n.selectionEnd ?? n.value.length, "end");
  n.dispatchEvent(new Event("input", { bubbles: true }));  // so the page's own copy of the text has it
}
export function dropFiles(root = document) {
  root.addEventListener("dragover", e => {
    if (!hasFiles(e)) return;
    if (e.defaultPrevented) { mark(null); return; }  // something on the way up took it
    e.preventDefault();
    const n = box(e); mark(n); e.dataTransfer.dropEffect = n ? "copy" : "none";
  });
  root.addEventListener("dragleave", e => { if (!e.relatedTarget) mark(null); });
  root.addEventListener("drop", e => {
    mark(null);
    if (!hasFiles(e) || e.defaultPrevented) return;
    e.preventDefault();
    const n = box(e); if (n) put(n, [...e.dataTransfer.files]);
  });
}
