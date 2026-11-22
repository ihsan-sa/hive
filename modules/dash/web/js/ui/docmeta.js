// What the chat's document cards show (ui/message.js docCards): a document's title, revision and note counts, from GET /api/docs.
// docMeta(id) is synchronous: it returns what is cached (or null) and starts a read when the cache is over 30 s old; when the read
// lands the chat redraws, so a card that first said "Document" fills in its revision and count. docMetaRefresh() reads at once
// (the side panel calls it after each note change, so the card follows the panel).
import { S, jget, emit } from "../core/store.js";

const M = new Map();
let at = 0, busy = null;

export function docMetaRefresh() {
  if (busy) return busy;
  at = Date.now();
  busy = jget("/api/docs").then(r => {
    let changed = false;
    for (const d of (r && r.docs) || []) {
      const was = M.get(d.id), now = { title: d.title, rev: d.rev === "next" ? (d.revs || []).filter(x => x !== "next").pop() : d.rev, drafts: d.drafts || 0, open: d.open || 0, pages: d.pages || 0 };
      if (!was || JSON.stringify(was) !== JSON.stringify(now)) { M.set(d.id, now); changed = true; }
    }
    if (changed) { S.ver++; emit({ names: true }); }
  }, () => {}).finally(() => { busy = null; });
  return busy;
}

export function docMeta(id) {
  if (Date.now() - at > 30e3) docMetaRefresh();
  return M.get(id) || null;
}
