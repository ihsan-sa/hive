// Yours: one serial stream of every exchange the person had, built from the board alone with the same classification Chat's For you uses:
// what they said, everything addressed to them, and each finished run's closing word in a conversation they are part of (model.js closingPosts).
// Newest first. The Yours page (views/yours.js) draws it with a day heading and a thin rule at each change of conversation; Chat's @ question
// looks here before it asks an agent. Pure: no fetch, no DOM.
import { S } from "./store.js";
import { SHOW, SIDE_THREAD, postKind, closingPosts, displayText, chatText, threadTitle, isSystemThread } from "./model.js";
import { dayName, mdPlain } from "./format.js";

const Y = { sig: "", rows: [] };
const ended = () => { let n = 0; for (const s of S.sessions) if (s.end) n++; return n; };

/** The person's exchanges, newest first. Cached until the board or a run's end changes it. */
export function yoursPosts() {
  const sig = S.posts.length + ":" + ended() + ":" + (S.posts.length ? S.posts[S.posts.length - 1].id : "");
  if (sig === Y.sig) return Y.rows;
  const closing = closingPosts(), rows = [];
  for (const p of S.posts) {
    if (p.th === SIDE_THREAD || isSystemThread(S.threads.get(p.th))) continue;
    if (postKind(p) === SHOW.you || closing.has(p.id)) rows.push(p);
  }
  rows.sort((a, b) => b.t - a.t);
  Y.sig = sig; Y.rows = rows;
  return rows;
}

/** Plain text of an exchange, as the stream and the search see it. */
export const yoursText = p => mdPlain(chatText(p) || "");

/** The exchanges that hold every word of q (in the text, the author or the conversation's name). */
export function yoursSearch(q, rows = yoursPosts()) {
  const ws = String(q || "").toLowerCase().split(/\s+/).filter(Boolean);
  if (!ws.length) return rows;
  return rows.filter(p => { const t = (mdPlain(displayText(p) || "") + " " + p.from + " " + threadTitle(p.th)).toLowerCase(); return ws.every(w => t.includes(w)); });
}

/** The stream as items to draw: {day} at each new day, {thread, name} at each change of conversation within it, {post} for each exchange. */
export function yoursItems(rows) {
  const out = []; let day = null, th = null;
  for (const p of rows) {
    const d = dayName(p.t);
    if (d !== day) { out.push({ day: d, key: "day:" + d }); day = d; th = null; }
    if (p.th !== th) { out.push({ thread: p.th, name: threadTitle(p.th), key: "th:" + d + ":" + p.th }); th = p.th; }
    out.push({ post: p, key: "y:" + p.id });
  }
  return out;
}
