// Installable page + offline app shell. Same-origin GETs are network-first: online the page is always the server's latest (no stale page after a deploy),
// and each good answer is kept in a versioned cache so that with no network the installed app still opens from the last copy. /api, login and the
// event streams are never cached. It also lets notifications work from a background tab and brings the app window forward when one is clicked.
const CACHE = "hive-shell-v1";
const SHELL = ["./", "index.html"];
// Writes never pass through this worker (static routing, Chrome 123+): a POST the page sends as it unloads, board.js's keepalive release of the
// serial bridge on a reload, never reached the Hive when a fetch handler stood in its way, so the reloaded tab found its own old hold for 20 s.
const WRITES = ["POST", "PUT", "PATCH", "DELETE"].map(m => ({ condition: { requestMethod: m }, source: "network" }));
self.addEventListener("install", e => {
  e.waitUntil(Promise.all([e.addRoutes ? e.addRoutes(WRITES).catch(() => {}) : null, caches.open(CACHE).then(c => c.addAll(SHELL)).catch(() => {})]));
  self.skipWaiting();
});
self.addEventListener("activate", e => e.waitUntil(
  caches.keys().then(ks => Promise.all(ks.filter(k => k !== CACHE).map(k => caches.delete(k)))).then(() => self.clients.claim())));
const cacheable = u => u.origin === self.location.origin && !/\/(api|login|logout|events|stream|files|share)(\/|$)/.test(u.pathname);
self.addEventListener("fetch", e => {
  const r = e.request;
  if (r.method !== "GET") return;
  let u; try { u = new URL(r.url); } catch (err) { return; }
  if (!cacheable(u)) return;
  e.respondWith(fetch(r).then(res => {
    if (res.ok && res.type === "basic") { const copy = res.clone(); caches.open(CACHE).then(c => c.put(r, copy)).catch(() => {}); }
    return res;
  }).catch(() => caches.match(r, { ignoreSearch: r.mode !== "navigate" }).then(m => m || (r.mode === "navigate" ? caches.match("index.html") : undefined) || Response.error())));
});
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
