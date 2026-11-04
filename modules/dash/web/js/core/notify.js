// Opt-in browser notifications: a new post to the person (an agent asking something, or finishing a run) shows one when this page isn't in front.
// Kept in this browser, like Simple view. The service worker (sw.js) is also what lets the browser offer to install the page as an app; it caches nothing.
import { plainName } from "./simple.js";

const KEY = "hive.notify";
let known = null;   // ids already seen; null until the first poll, so what was waiting before doesn't fire a burst

export const notifySupported = () => typeof Notification !== "undefined" && "serviceWorker" in navigator;
export const notifyBlocked = () => notifySupported() && Notification.permission === "denied";
export function notifyOn() {
  if (!notifySupported() || Notification.permission !== "granted") return false;
  try { return localStorage.getItem(KEY) === "1"; } catch (e) { return false; }
}
const keep = on => { try { localStorage.setItem(KEY, on ? "1" : "0"); } catch (e) { /* no storage */ } };

/** Ask the browser's permission (it must come from a click), then turn them on. Returns whether they're on. */
export async function setNotify(on) {
  if (!on) { keep(false); return false; }
  if (!notifySupported()) return false;
  const p = Notification.permission === "granted" ? "granted" : await Notification.requestPermission();
  keep(p === "granted");
  return p === "granted";
}

export async function notifyTest() {
  const reg = await navigator.serviceWorker.ready;
  await reg.showNotification("Hive", { body: "This is how a notification looks.", icon: "icons/icon-192.png", tag: "hive-test" });
}

/** Called with each fresh inbox. Shows one notification per new unread post, up to three, then one line for the rest. */
export function notifyNew(items) {
  const fresh = known ? items.filter(x => !known.has(x.id) && !x.read) : [];
  known = new Set([...(known || []), ...items.map(x => x.id)]);
  if (!fresh.length || !notifyOn() || (!document.hidden && document.hasFocus())) return fresh;
  navigator.serviceWorker.ready.then(reg => {
    for (const x of fresh.slice(0, 3)) reg.showNotification(plainName(x.from), { body: String(x.text || "").replace(/\s+/g, " ").slice(0, 160), icon: "icons/icon-192.png", tag: x.id });
    if (fresh.length > 3) reg.showNotification("Hive", { body: `${fresh.length - 3} more waiting for you.`, icon: "icons/icon-192.png", tag: "hive-more" });
  }).catch(() => {});
  return fresh;
}

if (notifySupported()) navigator.serviceWorker.register("sw.js").catch(() => {});
