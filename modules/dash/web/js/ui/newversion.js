// A tab left open keeps the code it loaded, so a fix that has landed doesn't reach it. /api/live says which version the server has; when that changes, say so.
// A note and not a reload of its own accord: the person may be in the middle of something, and a reload is theirs to choose. Drafts survive one.
// The note is a row of the top bar (shell.js topBar draws it under the band), so the page moves down to make room instead of being covered.
import { S, subscribe } from "../core/store.js";
import { h } from "../core/dom.js";
import { button } from "./kit.js";
import { banner } from "./banner.js";

let mine = null, told = null;
/** Remembers the version this tab loaded with, from the first /api/live answer. */
export function watchVersion() {
  subscribe(() => { const now = S.live && S.live.web; if (now && !mine) mine = now; });
}
/** The "newer version" row for the top bar, or null while this tab is current or the person said Later. */
export function newVersionNote(update) {
  const now = S.live && S.live.web; if (!now) return null;
  if (!mine) mine = now;
  if (now === mine || now === told) return null;
  return banner({ tone: "info", cls: "new-ver", key: "new-ver", text: [h("b", null, "A newer version of Hive is ready."), " Reload when you're ready; what you've typed is kept."],
    actions: [button("Reload", () => location.reload(), { size: "s", kind: "primary" }), button("Later", () => { told = now; update(); }, { size: "s", kind: "quiet" })] });
}
