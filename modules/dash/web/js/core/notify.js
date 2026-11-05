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

// Web Push: reaches this browser with the page closed. The browser's subscription is the on/off state; the server keeps a copy to send to.
export const pushSupported = () => notifySupported() && typeof PushManager !== "undefined";
export async function pushOn() {
  if (!pushSupported() || Notification.permission !== "granted") return false;
  const reg = await navigator.serviceWorker.getRegistration();
  return !!(reg && await reg.pushManager.getSubscription());
}
const keyBytes = k => Uint8Array.from(atob(k.replace(/-/g, "+").replace(/_/g, "/")), c => c.charCodeAt(0));
/** Must come from a click. `send` is the app's post(), and a browser the server refused doesn't stay subscribed. */
export async function setPush(on, publicKey, send) {
  const reg = await navigator.serviceWorker.ready;
  const old = await reg.pushManager.getSubscription();
  if (!on) {
    if (!old) return;
    try { await send("/api/webpush", { do: "unsubscribe", endpoint: old.endpoint }); } finally { await old.unsubscribe(); }
    return;
  }
  if (await Notification.requestPermission() !== "granted") throw new Error("Your browser is blocking notifications for this page. Allow them in the browser's site settings, then come back here.");
  if (old) await old.unsubscribe();  // it may belong to an older key, and a browser won't swap keys in place
  const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(publicKey) });
  try { await send("/api/webpush", { do: "subscribe", subscription: sub.toJSON() }); }
  catch (e) { await sub.unsubscribe(); throw e; }
}

if (notifySupported()) navigator.serviceWorker.register("sw.js").catch(() => {});
