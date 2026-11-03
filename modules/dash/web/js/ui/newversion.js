// A tab left open keeps the code it loaded, so a fix that has landed doesn't reach it. /api/live says which version the server has; when that changes, say so.
// A note and not a reload of its own accord: the person may be in the middle of something, and a reload is theirs to choose. Drafts survive one.
import { S, subscribe } from "../core/store.js";
import { h } from "../core/dom.js";
import { button } from "./kit.js";

let mine = null, told = null;
export function watchVersion() {
  subscribe(() => {
    const now = S.live && S.live.web; if (!now) return;
    if (!mine) mine = now;
    if (now === mine || now === told) return;
    told = now;
    for (const old of document.querySelectorAll(".new-ver")) old.remove();
    const note = h("div", { class: "new-ver", role: "status" }, h("span", null, "A new version of the app is ready."),
      button("Reload", () => location.reload(), { size: "s", kind: "primary" }), button("Later", () => note.remove(), { size: "s", kind: "quiet" }));
    document.body.append(note);
  });
}
