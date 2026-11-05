// "Brought up for you": threads an agent raised with `hive show THREAD --why ...`, pinned in the Chat side list, over Conversations, until their time is up or the person dismisses them.
import { h } from "../core/dom.js";
import { S, emit, loadShown, post } from "../core/store.js";
import { threadTitle } from "../core/model.js";
import { threadHref } from "../core/router.js";
import { now } from "../core/format.js";
import { button } from "./kit.js";

// Gone at once; the server's note makes it stick on every page. If that note fails, the next poll brings it back.
function dismiss(th) {
  S.shown = S.shown.filter(r => r.thread !== th); S.ver++; emit({ names: true });
  post("/api/shown", { thread: th }).catch(() => {}).finally(loadShown);
}

export function brought() {
  const live = (S.shown || []).filter(r => r.until * 1000 > now());
  if (!live.length) return null;
  return h("div", { class: "brought", role: "region", "aria-label": "Brought up for you", "data-key": "brought" },
    h("b", { class: "brought-h" }, "Brought up for you"),
    live.map(r => h("div", { class: "brought-r", "data-key": r.thread },
      h("a", { class: "brought-i", href: threadHref(r.thread), title: r.why || null },
        h("span", { class: "brought-t" }, threadTitle(r.thread)),
        h("span", { class: "meta brought-w" }, r.from + (r.why ? ": " + r.why : ""))),
      button(`Dismiss ${threadTitle(r.thread)}`, () => dismiss(r.thread), { kind: "icon", icon: "close", size: "s", cls: "brought-x" }))));
}
