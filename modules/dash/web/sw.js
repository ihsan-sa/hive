// Installable page + offline app shell. Same-origin GETs are network-first: online the page is always the server's latest (no stale page after a deploy),
// and each good answer is kept in a versioned cache so that with no network the installed app still opens from the last copy. /api, login and the
// event streams are never cached. It also lets notifications work from a background tab and brings the app window forward when one is clicked.
const CACHE = "hive-shell-v1";
const SHELL = ["./", "index.html"];
// Writes never pass through this worker (static routing, Chrome 123+): a POST the page sends as it unloads, board.js's keepalive release of the
// serial bridge on a reload, never reached the Hive when a fetch handler stood in its way, so the reloaded tab found its own old hold for 20 s.
const WRITES = ["POST", "PUT", "PATCH", "DELETE"].map(m => ({ condition: { requestMethod: m }, source: "network" }));
// Except the share target (manifest share_target, E118): another app on the phone POSTs a shared text, link or file to share-in, and only this
// worker can take it (the Hive refuses a form without its own header). A browser without the "not" condition gets the plain list: shares then
// open an empty new conversation (dash.py's share-in redirect) and every write still skips the worker.
const SHARE_IN = "share-in", SHARED = "hive-shared";
// A condition with "not" or "or" takes no other key, so "a write and not share-in" is written not(not write, or share-in).
const WRITES_BUT_SHARE = WRITES.map(r => ({ ...r, condition: { not: { or: [{ not: r.condition }, { urlPattern: new URL(SHARE_IN, self.location.href).pathname }] } } }));
const addWrites = e => e.addRoutes(WRITES_BUT_SHARE).catch(() => e.addRoutes(WRITES)).catch(() => {});
self.addEventListener("install", e => {
  e.waitUntil(Promise.all([e.addRoutes ? addWrites(e) : null, caches.open(CACHE).then(c => c.addAll(SHELL)).catch(() => {})]));
  self.skipWaiting();
});
self.addEventListener("activate", e => e.waitUntil(
  caches.keys().then(ks => Promise.all(ks.filter(k => k !== CACHE && k !== SHARED).map(k => caches.delete(k)))).then(() => self.clients.claim())));
const cacheable = u => u.origin === self.location.origin && !/\/(api|login|logout|events|stream|files|share)(\/|$)/.test(u.pathname);
self.addEventListener("fetch", e => {
  const r = e.request;
  if (r.method === "POST" && new URL(r.url).pathname.endsWith("/" + SHARE_IN)) { e.respondWith(shareIn(r)); return; }
  if (r.method !== "GET") return;
  let u; try { u = new URL(r.url); } catch (err) { return; }
  if (!cacheable(u)) return;
  e.respondWith(fetch(r).then(res => {
    if (res.ok && res.type === "basic") { const copy = res.clone(); caches.open(CACHE).then(c => c.put(r, copy)).catch(() => {}); }
    return res;
  }).catch(() => caches.match(r, { ignoreSearch: r.mode !== "navigate" }).then(m => m || (r.mode === "navigate" ? caches.match("index.html") : undefined) || Response.error())));
});
// A share from another app: keep its words and files in a cache of their own, then open a new conversation with them in the box (composer.js
// takeShared reads ?shared=ID and deletes the entry). Nothing is sent: the person reads it, adds to it, and presses Send.
async function shareIn(r) {
  const id = Date.now().toString(36) + Math.random().toString(36).slice(2, 6);
  try {
    const f = await r.formData(), c = await caches.open(SHARED), one = k => String(f.get(k) || "").trim();
    const files = f.getAll("files").filter(x => x && typeof x === "object" && x.size >= 0);
    await Promise.all(files.map((x, i) => c.put(`${SHARE_IN}/${id}/${i}`, new Response(x, { headers: { "content-type": x.type || "application/octet-stream", "x-name": encodeURIComponent(x.name || "shared-" + (i + 1)) } }))));
    await c.put(`${SHARE_IN}/${id}/meta`, new Response(JSON.stringify({ title: one("title"), text: one("text"), url: one("url"), files: files.length, t: Date.now() }), { headers: { "content-type": "application/json" } }));
  } catch (err) { return Response.redirect("./#comms/~new", 303); }
  return Response.redirect(`./?shared=${id}#comms/~new`, 303);
}
// Web Push (module conn-webpush): one fixed line and a link, with the page closed. Nothing shows if the page is already in front.
const PUSH_LINE = "Your Hive has a message for you";
self.addEventListener("push", e => {
  let m = {};
  try { m = e.data.json() || {}; } catch (err) { /* the fixed line will do */ }
  e.waitUntil(self.clients.matchAll({type: "window", includeUncontrolled: true}).then(list => {
    if (list.some(c => c.focused)) return;
    return self.registration.showNotification(String(m.title || "Hive"), { body: String(m.body || PUSH_LINE), icon: "icons/icon-192.png", tag: "hive-waiting", data: { url: m.url } });
  }));
});
// Only ever this site: a click can't be sent anywhere else, whatever a message says.
const clickTarget = n => {
  try { const u = new URL(n.data.url || "./", self.location.href); if (u.origin === self.location.origin) return u.href; } catch (err) { /* no link */ }
  return "./";
};
self.addEventListener("notificationclick", e => {
  e.notification.close();
  e.waitUntil(self.clients.matchAll({type: "window", includeUncontrolled: true}).then(list => {
    const w = list.find(c => "focus" in c);
    return w ? w.focus() : self.clients.openWindow(clickTarget(e.notification));
  }));
});
