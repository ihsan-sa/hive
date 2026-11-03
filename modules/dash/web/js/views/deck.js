// The Deck page: connect the DDJ-FLX4, arm full control, see what every control does and what the deck really sends.
import { h } from "../core/dom.js";
import { can } from "../core/store.js";
import { metrics } from "../templates.js";
import { button, pageHeader, section } from "../ui/kit.js";
import { table } from "../ui/table.js";
import { start as startTour } from "../deck/tour.js";
import { arm, canChange, connect, D, disarm, MAP, supported, watchLog } from "../deck/control.js";

let unwatch = null, queued = false;
const WORD = { off: "Not connected", asking: "Waiting for the browser's yes", waiting: "Waiting for the deck", on: "DDJ-FLX4 connected", refused: "Browser said no", none: "This browser can't" };
const reach = () => [["stop", "kill agents"], ["tier", "set tiers"], ["limits", "pause accounts"], ["post", "post"]].filter(([c]) => can(c)).map(x => x[1]).join(", ");

export default {
  id: "deck", label: "Deck", tab: false,
  enter(ctx) { unwatch = watchLog(() => { if (queued) return; queued = true; setTimeout(() => { queued = false; ctx.update(); }, 80); }); },
  leave() { if (unwatch) unwatch(); unwatch = null; },
  render() {
    const on = D.state === "on";
    return metrics({
      header: pageHeader({
        title: "Deck", lead: on ? "Move a control to try it." : "Run this page from a DDJ-FLX4 plugged into this laptop.",
        actions: on ? button("Start the tour", () => startTour(), { kind: "primary" }) : button(D.state === "off" ? "Connect the deck" : "Try again", () => connect(), { kind: "primary" }),
      }),
      sections: [
        section({ title: WORD[D.state] || "Deck", note: D.say || (on ? (D.sysex ? "" : "Without the browser's full MIDI permission the deck may drop into its demo lights. Controls still work.") : "Needs Chrome or Edge, and the browser asks once. Close rekordbox and Serato first: on Windows only one app can hold the deck.") },
          !supported() ? h("p", { class: "meta" }, "No Web MIDI in this browser. Safari has none; Firefox needs a site permission add-on.") : null,
          h("div", { class: "deck-arm", "data-armed": D.armed ? "1" : null },
            h("div", null, h("b", null, D.armed ? "Full control is on" : "Normal: look around, nothing can change"),
              h("p", { class: "meta" }, D.armed ? `The deck can now ${reach() || "do nothing more here"}, and press every button on the page. Each change still opens its confirm window.`
                : canChange() ? `Full control adds: ${reach()}. On the deck, hold SHIFT + BEAT FX ON/OFF for 1.5 s.` : "This connection is read-only, so full control has nothing to add.")),
            on && canChange() ? button(D.armed ? "Leave full control" : "Full control", () => (D.armed ? disarm() : arm()), { kind: D.armed ? "primary" : "quiet", tone: D.armed ? null : "bad" }) : null)),
        section({ title: "What each control does" },
          table({ columns: [{ label: "Control", render: r => h("b", { "data-hot": r[0] === D.touched ? "1" : null, class: "deck-ctl" }, r[1]) }, { label: "Always", wrap: true, render: r => r[2] }, { label: "Full control", wrap: true, render: r => r[3] }], rows: MAP, key: r => r[1] })),
        section({ title: "What the deck sends", note: "Newest first. If a control does nothing, send these lines to the thread: the map was built from Mixxx's published map, not on a real deck." },
          h("div", { class: "deck-page-raw mono" }, D.log.length ? D.log.map(l => h("div", { "data-miss": l.name === "not mapped" ? "1" : null }, l.hex, "  ", l.name)) : "Nothing yet.")),
      ],
    });
  },
};
