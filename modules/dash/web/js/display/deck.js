// What the deck does on a TV page. No control here changes the hive: at most it moves a tile or follows a link to the dashboard.
const LINKS = 'a[href*="#"]';
let here = null, herekey = null;
const links = () => [...document.querySelectorAll(LINKS)].filter(a => a.getClientRects().length);
const mark = a => { if (here) here.removeAttribute("data-deck-here"); here = a; herekey = a && a.getAttribute("href"); if (a) { a.setAttribute("data-deck-here", ""); a.scrollIntoView({ block: "nearest" }); } };
const current = list => (here && here.isConnected && list.includes(here) ? here : herekey && list.find(a => a.getAttribute("href") === herekey) || null);

/** A render rebuilds a tile's insides, so the mark goes back on after each one. */
export const deckMark = () => { if (!herekey) return; const list = links(), a = current(list); if (a) { if (a !== here) here = a; if (!a.hasAttribute("data-deck-here")) a.setAttribute("data-deck-here", ""); } };

/** Every tile that has this deck control. Timeline first: it's the one that's always on the single screen's big area. */
const hooks = (D, name) => D.TILES().filter(t => t.deck && t.deck[name]).sort((a, b) => (a.id === "timeline" ? -1 : b.id === "timeline" ? 1 : 0));
const each = (D, name, ...args) => { for (const t of hooks(D, name)) t.deck[name](...args); };

export function deckEvent(e, say, D) {
  switch (e.c) {
    case "tempo": if (e.deck === 2) return D.resize(e.v); return each(D, "window", e.v);
    case "browse": {
      const list = links(); if (!list.length) return;
      const i = list.indexOf(current(list)), step = e.d * (e.shift ? 5 : 1), n = list[((i < 0 ? (step > 0 ? -1 : 0) : i) + step + list.length * 8) % list.length];
      mark(n); return say((n.textContent || "").trim().slice(0, 60));
    }
    case "browsePress": {
      if (!e.on) return;
      const a = current(links());
      if (!a) return say("Turn BROWSE to pick something first");
      mark(null); return a.click();
    }
    case "jog": { const t = hooks(D, "jog")[0]; if (t) t.deck.jog(e.deck, e.d, e.shift); return; }
    case "crossfader": return each(D, "scrub", e.v);
    case "sync": if (e.on) each(D, "fit"); return;
    case "headCue": if (e.on) each(D, "toggle"); return;
    case "play": if (e.on) { const t = hooks(D, "play")[0]; if (t) t.deck.play(); } return;
    case "fxSelect": if (e.on) { const r = document.documentElement; r.dataset.theme = r.dataset.theme === "dark" ? "light" : "dark"; dispatchEvent(new Event("themechange")); D.redraw(); } return;
    case "cue": if (e.on) { if (here) return mark(null); D.toApp(""); } return;
    case "pad": case "load": case "callPrev": case "callNext": case "loopIn": case "loopOut": if (e.on) say("Press EXIT for the dashboard"); return;
  }
}
