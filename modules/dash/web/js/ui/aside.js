// The /btw aside: a small window right above the composer, so the person stays in their conversation.
// It shows the side thread (question, answer as it arrives), takes a follow-up, and closes without losing anything:
// the side thread stays, and so does the "Side questions:" link in the header.
import { h } from "../core/dom.js";
import { S, post, poll } from "../core/store.js";
import { threadHref } from "../core/router.js";
import { displayText } from "../core/model.js";
import { markdown } from "./markdown.js";

const A = { main: null, th: null, pending: "", err: "", busy: false, update: null };
const refresh = () => A.update && A.update();

/** Open (or move to) the aside for a side thread of `main`. `question` shows until the board has it. */
export function openAside(main, th, question) {
  Object.assign(A, { main, th, pending: question || "", err: "" }); refresh();
}
export const asideOpen = () => !!A.th;
export const closeAside = () => { A.th = null; A.main = null; A.pending = ""; refresh(); };

async function follow(el) {
  const text = el.value.trim(); if (!text || A.busy) return;
  A.busy = true; A.err = ""; refresh();
  try { await post("/api/post", { text, thread: A.th }); el.value = ""; poll(); }
  catch (e) { A.err = e.message; }
  A.busy = false; refresh();
}

/** The panel for the conversation `main`, or null when no aside is open for it. */
export function asidePanel(main, update) {
  A.update = update;
  if (!A.th || A.main !== main) return null;
  const d = S.threads.get(A.th), posts = d ? d.posts : [];
  if (posts.length) A.pending = "";
  const link = (t, f, title) => h("a", { href: f ? threadHref(A.th) : "#", class: "ba-l", title, onclick: f ? null : e => { e.preventDefault(); closeAside(); } }, t);
  const who = p => p.from === "person" ? "You" : p.from;
  return h("section", { class: "ba", "data-key": "ba", "aria-label": "Side question" },
    h("header", { class: "ba-top" }, h("span", { class: "ba-h" }, "Side question"), h("span", { class: "meta" }, "answers here, not in this conversation"),
      h("span", { class: "ba-sp" }), link("Full thread", true, "Open the side thread"), link("Close", false, "Close (Esc). The side thread stays")),
    h("div", { class: "ba-m", "data-key": "m:" + A.th, role: "log", "aria-live": "polite" },
      posts.map(p => h("div", { class: "ba-p", "data-key": p.id, "data-me": p.from === "person" ? "" : null }, h("b", null, who(p)), h("div", { class: "ba-b" }, markdown(displayText(p))))),
      !posts.length && A.pending ? h("div", { class: "ba-p", "data-me": "" }, h("b", null, "You"), h("div", { class: "ba-b" }, h("p", null, A.pending))) : null,
      posts.length && posts.at(-1).from === "person" ? h("p", { class: "meta ba-wait" }, "Waiting for the answer…") : null),
    A.err ? h("p", { class: "cp-err", role: "alert" }, A.err) : null,
    h("input", { class: "input ba-in", "data-key": "in", type: "text", placeholder: "Follow up on the side question…", "aria-label": "Follow up on the side question", enterkeyhint: "send",
      onkeydown: (e, n) => { if (e.key === "Enter" && !e.isComposing) { e.preventDefault(); follow(n); } else if (e.key === "Escape") { e.stopPropagation(); closeAside(); } } }));
}
