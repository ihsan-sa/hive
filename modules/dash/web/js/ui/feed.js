// Activity feed: tool calls and what an agent said, newest first. Keeps its own scroll, which morph leaves alone.
import { h } from "../core/dom.js";
import { S } from "../core/store.js";
import { clock, short } from "../core/format.js";
import { button, empty, note } from "./kit.js";


/** The server blanks these below full rights (feed_for in dash.py), so a row with a tool and no words needs saying why. Null when nothing is hidden. */
export function feedHiddenNote(items) {
  if (!items || !items.length || items.some(e => e.k !== "tool" || e.x)) return null;
  const limited = S.app.mode === "limited";
  return note({ key: "feed-hidden", actions: limited ? null : button("Sign in again", null, { size: "s", href: "/cdn-cgi/access/logout", title: "This signs you out; open this page again and sign in with Google." }) },
    limited ? "Your sign-in is limited, so you see which tools this agent used, not its commands or what it said."
      : "Commands and what this agent said are hidden: full mode ends after 30 minutes without using the app. Sign in again to see them.");
}

/** items: [{t, k: "tool"|"said", tool, x}], oldest first as the server sends them. null: still loading. */
export function feed(items, { max = 700 } = {}) {
  if (items == null) return empty("Reading…", { kind: "loading" });
  if (!items.length) return empty("No session log left to read.");
  const seen = new Map();
  const rows = items.map(e => {
    const said = e.k !== "tool", base = `${e.t}|${e.tool || "said"}|${String(e.x).slice(0, 48)}`, c = seen.get(base) || 0; seen.set(base, c + 1);
    return h("li", { class: "feed-r", "data-key": base + "|" + c, "data-said": said ? "" : null },
      h("time", null, e.t ? clock(e.t * 1e3) : ""),
      h("span", { class: "feed-k", "data-said": said ? "" : null }, said ? "said" : e.tool),
      h("div", { class: "feed-x" }, short(said ? e.x : String(e.x).replace(/^Running: /, ""), max)));
  });
  return [feedHiddenNote(items), h("ol", { class: "feed", "data-key": "feed", tabindex: 0, "aria-label": "Latest activity" }, rows.reverse())];
}
