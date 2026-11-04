// The smallest service worker an installable page needs. It caches nothing: every request goes to the network, so the page never shows old data.
// It also lets notifications work from a background tab and brings the app window forward when one is clicked.
self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", e => e.waitUntil(self.clients.claim()));
self.addEventListener("fetch", () => {});
self.addEventListener("notificationclick", e => {
  e.notification.close();
  e.waitUntil(self.clients.matchAll({type: "window", includeUncontrolled: true}).then(list => {
    const w = list.find(c => "focus" in c);
    return w ? w.focus() : self.clients.openWindow("./");
  }));
});
