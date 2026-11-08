// "Ask about this": quote a piece of the post above the composer of its thread (a chip with ×, composer.js quoteRef).
// The box stays the person's own; the quote goes in front of their words when they press Send.
import { h } from "../core/dom.js";
import { S, can } from "../core/store.js";
import { clock } from "../core/format.js";
import { quoteRef } from "../ui/composer.js";

const postOf = el => { const m = el.closest("article.msg"); return m ? S.byId[m.dataset.key] : null; };

/** Quote `text` from the post holding el. `where` is a short label, such as "line 12". */
export function askQuote(el, text, where, prose = false) {
  const p = postOf(el);
  if (!p || !can("post")) return false;
  quoteRef(p.th, { who: p.from === "person" ? "you" : p.from, where, text, code: !prose && !text.startsWith("$$") });
  return true;
}

/** A small Ask button; hidden by CSS outside a chat message, where there is no thread to ask in. */
export const askButton = (getText, label, where) => h("button", {
  type: "button", class: "rc-t rc-ask", "aria-label": label, onclick: (e, b) => askQuote(b, getText(b), where),
}, "Ask");

// ---- Ask on selected text: a small button beside a selection inside one chat message ----
const BLOCK = /^(P|LI|PRE|TR|DIV|H[1-6]|BLOCKQUOTE|TABLE|UL|OL)$/;
/** What a selection says: maths as its LaTeX, and without the tool rows the selection ran across. */
function textOf(n) {
  let out = "";
  for (const c of n.childNodes) {
    if (c.nodeType === 3) { out += c.nodeValue; continue; }
    if (c.nodeType !== 1 || c.matches("button,input,.rc-bar,.rc-tools,.rc-fig-note,svg")) continue;
    if (c.matches(".katex")) {
      const tex = c.querySelector('annotation[encoding="application/x-tex"]')?.textContent;
      out += tex ? (c.closest(".katex-display") ? `$$${tex}$$` : `$${tex}$`) : c.textContent;
      continue;
    }
    out += textOf(c);
    if (BLOCK.test(c.tagName) && !out.endsWith("\n")) out += "\n";
  }
  return out;
}

let pop = null, pointerDown = false, timer = 0;
const selEl = n => (n?.nodeType === 1 ? n : n?.parentElement);
function selected() {
  const s = getSelection();
  if (!s || s.isCollapsed || !s.rangeCount) return null;
  const a = selEl(s.anchorNode), z = selEl(s.focusNode), m = a?.closest("article.msg");
  if (!m || z?.closest("article.msg") !== m || !a.closest(".md") || !z.closest(".md")) return null;
  const r = s.getRangeAt(0), text = textOf(r.cloneContents()).replace(/\n{3,}/g, "\n\n").trim();
  return text ? { r, m, text } : null;
}
function place() {
  const sel = pop && selected();
  if (!sel) return hide();
  const rs = sel.r.getClientRects(), first = rs[0], last = rs[rs.length - 1];
  if (!first) return hide();
  // Above the start of the selection; below its end on touch, where the phone's own menu sits above.
  const below = matchMedia("(pointer:coarse)").matches || first.top < 60;
  const y = below ? last.bottom + 6 : first.top - pop.offsetHeight - 6;
  if (y < 0 || y > innerHeight) { pop.style.visibility = "hidden"; return; }
  pop.style.visibility = "";
  pop.style.top = y + "px";
  pop.style.left = Math.max(8, Math.min(innerWidth - pop.offsetWidth - 8, below ? last.right : first.left)) + "px";
}
function hide() {
  if (!pop) return;
  pop.remove(); pop = null;
  removeEventListener("scroll", place, true); removeEventListener("resize", place); removeEventListener("keydown", onKey);
}
const onKey = e => { if (e.key === "Escape") hide(); };
function show() {
  const sel = selected(), p = sel && S.byId[sel.m.dataset.key];
  if (!p || !can("post")) return hide();
  if (!pop) {
    const b = h("button", { type: "button" }, "Ask about this");
    // A press here must not clear the selection it is about.
    b.addEventListener("pointerdown", e => e.preventDefault());
    b.addEventListener("mousedown", e => e.preventDefault());
    b.addEventListener("click", () => {
      const now = selected(); if (!now) return hide();
      const post = S.byId[now.m.dataset.key];
      askQuote(now.m, now.text, post?.at ? clock(post.at) : "", true);
      getSelection().removeAllRanges(); hide();
    });
    pop = h("div", { class: "rc-sel" }, b);
    document.body.append(pop);
    addEventListener("scroll", place, { capture: true, passive: true }); addEventListener("resize", place); addEventListener("keydown", onKey);
  }
  place();
}
const later = () => { clearTimeout(timer); timer = setTimeout(() => { if (!pointerDown) show(); }, 260); };
document.addEventListener("pointerdown", e => { if (!e.target.closest?.(".rc-sel")) pointerDown = true; });
document.addEventListener("pointerup", () => { pointerDown = false; later(); });
document.addEventListener("pointercancel", () => { pointerDown = false; });
document.addEventListener("selectionchange", later);
