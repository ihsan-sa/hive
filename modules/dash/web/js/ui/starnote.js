// One quiet ask for a GitHub star (hive-stars): 45 install runs brought 6 stars, and people who use the Hive day to day never see the
// installer's last line or open Settings. So, once this browser has used the Hive on three different days (its own local days), and only on
// a Hive installed from a release (not a dev tree), a row under the bar asks. Either button ends it for good in this browser; left unanswered,
// it stops by itself after showing on STAR_SHOWN days. Reviewed by an independent Opus pass (projects/hive-stars/research.md).
import { h } from "../core/dom.js";
import { button } from "./kit.js";
import { banner } from "./banner.js";

export const STAR_KEY = "hive.star", STAR_DAYS = 3, STAR_SHOWN = 5, REPO = "https://github.com/ihsan-sa/hive";
let st = null, installed = null, asked = false;

/** The browser's own date, YYYY-MM-DD: a "day" is the person's day, not a UTC one. */
export const localDay = (d = new Date()) => [d.getFullYear(), String(d.getMonth() + 1).padStart(2, "0"), String(d.getDate()).padStart(2, "0")].join("-");
const save = () => { try { localStorage.setItem(STAR_KEY, JSON.stringify(st)); } catch (e) {} };
const note = (list, day) => { if (!st[list].includes(day)) { st[list].push(day); return true; } return false; };

function load() {
  if (!st) {
    try { st = JSON.parse(localStorage.getItem(STAR_KEY) || "{}"); } catch (e) { st = null; }
    if (!st || typeof st !== "object" || Array.isArray(st)) st = {};   // a stored value that isn't ours must not break the top bar
    for (const k of ["days", "shown"]) if (!Array.isArray(st[k])) st[k] = [];
  }
  // Counted on every draw, not only on load: a tab left open for weeks still counts the days it was used.
  if (!st.done && st.days.length < STAR_DAYS && note("days", localDay())) save();
  return st;
}
function finish(how, update) {
  st.done = how; save(); update();
  const main = document.querySelector("main,#main"); if (main) { if (!main.hasAttribute("tabindex")) main.tabIndex = -1; main.focus({ preventScroll: true }); }   // focus doesn't fall to <body>
}

/** The star row for the top bar, or null: too soon, already answered or lapsed, or not a release install. shell.js draws it only when no
 *  newer-version row is showing. */
export function starNote(update) {
  const s = load();
  if (s.done || s.days.length < STAR_DAYS) return null;
  if (installed === null) {
    // A plain fetch, not jget: a failed read here must not make app.js re-run the page (store.js ownFail). A sign-in without the "config"
    // capability gets 403: it isn't the owner, so it is never asked. Any other failure: not this load, ask again on the next.
    if (!asked) {
      asked = true;
      fetch("/api/update", { cache: "no-store" }).then(r => r.status === 403 ? (st.done = "no-access", save(), null) : r.ok ? r.json() : null)
        .then(u => { installed = !!(u && u.installed); if (installed) update(); }, () => { installed = false; });
    }
    return null;
  }
  if (!installed) return null;
  if (note("shown", localDay())) { if (s.shown.length > STAR_SHOWN) { s.done = "lapsed"; save(); return null; } save(); }
  const go = button("Star on GitHub", () => finish("starred", update), { size: "s", kind: "primary", href: REPO, cls: "star-go" });
  go.target = "_blank"; go.rel = "noreferrer noopener"; go.title = "Opens GitHub in a new tab";
  const row = banner({ tone: "info", cls: "star-note", key: "star-note",
    text: [h("b", null, "Finding Hive useful?"), h("span", { class: "star-more" }, " A star on GitHub helps other people find it.")],
    actions: [go, button("No thanks", () => finish("no", update), { size: "s", kind: "quiet" })] });
  row.removeAttribute("role");   // an ask, not a status: a live region drawn already filled is read out unevenly, and this needn't interrupt
  return row;
}
