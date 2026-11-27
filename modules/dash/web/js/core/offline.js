// E207: the chats this phone last read, kept so it can open them with no connection. Each time a conversation is open and its messages
// change, its last posts (with its name and status bar) go into localStorage on this device only; the 5 most recently read are kept.
// With no answer from the Hive and nothing read yet, store.js loads them in place of the board and the app turns read only (can() is false
// for every write) until a read works again. sw.js keeps the page itself (E85); this keeps what was on it.
const KEY = "hive.lastRead", THREADS = 5, POSTS = 80, TEXT = 20000, BYTES = 1_500_000, MAX_AGE = 14 * 864e5;   // kept no longer than 14 days
const KEEP = ["id", "at", "from", "to", "thread", "text", "tags", "end", "reason", "files", "model", "effort", "via", "reply_to", "parent", "relay_of", "relay_by", "relay_why", "stop"];   // the board's own fields; not what the pages add to a post

function readAll() { try { const v = JSON.parse(localStorage.getItem(KEY) || "null"); return v && Array.isArray(v.threads) ? v : null; } catch (e) { return null; } }
// A kept row is used only when it has the shape remember() writes; anything else (an older version, a hand edit, a cut-off write) is skipped.
const goodPost = p => p && typeof p === "object" && typeof p.id === "string" && typeof p.at === "string" && !isNaN(Date.parse(p.at))
  && (p.text == null || typeof p.text === "string") && (p.from == null || typeof p.from === "string") && (p.tags == null || Array.isArray(p.tags)) && (p.files == null || Array.isArray(p.files));
const goodRow = r => r && typeof r === "object" && typeof r.th === "string" && r.th && Array.isArray(r.posts) && (r.meta == null || typeof r.meta === "object");
// Who signed in when the copy was kept (/api/app "who": a hash of the route and the sign-in). A different one reading this device drops it.
let owner = null;
export function setOwner(w) { if (!w) return; owner = w; const v = readAll(); if (v && v.who !== w) forget(); }   // a copy with no owner goes too
const slim = p => { const o = {}; for (const k of KEEP) if (p[k] != null) o[k] = k === "text" ? String(p[k]).slice(0, TEXT) : p[k]; return o; };

let lastSig = "";
/** Keep thread th as it reads now. d: the store's thread entry (S.threads.get(th)); meta: its name, the person's title and its status bar. */
export function remember(th, d, meta) {
  if (!owner || !th || !d || !d.posts || !d.posts.length) return;   // nothing is kept until /api/app has said who is signed in
  const last = d.posts[d.posts.length - 1], sig = th + "\n" + last.id + "\n" + d.posts.length + "\n" + JSON.stringify(meta || {});
  if (sig === lastSig) return; lastSig = sig;
  const was = readAll(), all = was && was.who === owner ? was : { threads: [] };
  const row = { th, at: Date.now(), meta: meta || {}, posts: d.posts.slice(-POSTS).map(slim) };
  let list = [row, ...all.threads.filter(r => r && r.th !== th)].slice(0, THREADS);
  // Over the size cap (long chats): drop the oldest kept chats, then trim the oldest posts, so the write still fits.
  for (;;) {
    const s = JSON.stringify({ v: 1, saved: Date.now(), who: owner, threads: list });
    if (s.length <= BYTES || !list.length) { try { localStorage.setItem(KEY, s); } catch (e) { if (list.length > 1) { list = list.slice(0, -1); continue; } } return; }
    if (list.length > 1) list = list.slice(0, -1); else { const p = list[0].posts; if (p.length <= 5) return; list[0] = { ...list[0], posts: p.slice(Math.ceil(p.length / 4)) }; }
  }
}
/** The kept chats, newest read first, or null when none are kept. */
export function keptCopy() {
  const v = readAll(); if (!v) return null;
  if (!(Date.now() - Number(v.saved) < MAX_AGE)) { forget(); return null; }
  const threads = v.threads.filter(goodRow).map(r => ({ ...r, meta: r.meta || {}, posts: r.posts.filter(goodPost) })).filter(r => r.posts.length);
  return threads.length ? { ...v, threads } : null;
}
/** Forget them (sign-out, or the person's own clear). */
export function forget() { lastSig = ""; try { localStorage.removeItem(KEY); } catch (e) {} }
