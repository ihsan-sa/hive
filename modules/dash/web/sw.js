// The smallest service worker an installable page needs. It caches nothing: every request goes to the network, so the page never shows old data.
// It also lets notifications work from a background tab and brings the app window forward when one is clicked.
self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", e => e.waitUntil(self.clients.claim()));
self.addEventListener("fetch", () => {});
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
