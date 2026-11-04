// Simple mode: the same Hive, shown for someone who doesn't want to know how it works. One voice ("Your Hive"), a chat.
// Presentation only: what a sign-in may do is still decided by the server. Everything stays reachable, by "Show everything" or by address.
// Never on the developer route. This browser's choice wins; with none, the server's default for this sign-in (web.json "simple").
import { S } from "./store.js";

/** The one voice in simple view: the name from Settings, if it has one. */
export const hiveName = () => (S.app && S.app.brand && S.app.brand.name) || "Your Hive";
const KEY = "hive.simple";
let mem = null;   // no storage: this page only

export function simpleOn() {
  if (!S.app || !S.app.app) return false;
  let v = mem;
  try { const got = localStorage.getItem(KEY); if (got !== null) v = got; } catch (e) { /* no storage */ }
  return v === null ? !!S.app.simple : v === "1";
}
export function setSimple(on) {
  mem = on ? "1" : "0";
  try { localStorage.setItem(KEY, mem); } catch (e) { /* no storage: this page only */ }
}
/** An agent's name as simple mode shows it. */
export const plainName = n => (simpleOn() && n !== "person" ? hiveName() : n);
