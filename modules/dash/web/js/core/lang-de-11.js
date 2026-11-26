// German for the 23 words the Link buy card, Settings' repo line and the document viewer's carried note added after lang-de-10.js
// (product-test keeper round 2026-11-24: drive_app_lang_fr failed on them in an early part-3 run; `node test/lang_left.mjs count` showed 23 in every
// language but Spanish). Same keys as lang-es-11.js's. Merged in lang.js, first in its language.
// E203n: the Stop and send receipt (ui/message.js); rcA names the agent inside it, rcC the same at the start of a line
const rcA = a => /^the agent$/i.test(a) ? "der Agent" : a.replace(/\bAgent ([A-Z]{1,2})\b/g, "Agent $1").replace(/ and /g, " und ");
const rcC = a => { const w = rcA(a); return w.charAt(0).toUpperCase() + w.slice(1); };
export default {
  "Bought": "Gekauft",
  "Canceled": "Storniert",
  "Card": "Karte",
  "Card expired": "Karte abgelaufen",
  "Charged": "Belastet",
  "Code, issues and updates are on": "Code, Issues und Neuigkeiten gibt es auf",
  "Declined": "Abgelehnt",
  "Expired": "Abgelaufen",
  "It has no merchant or amount.": "Es hat weder Händler noch Betrag.",
  "It isn't JSON.": "Das ist kein JSON.",
  "It isn't a buy.": "Das ist kein Kauf.",
  "Item": "Artikel",
  "Merchant": "Händler",
  "One-time card from Link, made on approval": "Einmalkarte von Link, bei Freigabe erstellt",
  "Receipt": "Beleg",
  "Recording": "Aufzeichnung",
  "Shipping": "Versand",
  "Should have lapsed": "Hätte ablaufen sollen",
  "Still open from rev": "Noch offen seit Rev.",
  "Tax": "Steuer",
  "Waiting for Link": "Wartet auf Link",
  "Waiting in Link": "Wartet in Link",
  "test mode": "Testmodus",
  "Stop and send": "Stoppen und senden",
  "Send waits for the current step to end. Stop and send tries to cut that step short.": "Senden wartet, bis der laufende Schritt endet. „Stoppen und senden“ versucht, diesen Schritt abzubrechen.",
  __patterns: [
    [/^Interrupt sent · asking (.+) to stop its step$/, (_, a) => `Unterbrechung gesendet · ${rcA(a)} wird gebeten, den Schritt abzubrechen`],
    [/^Sent · asking (.+) to stop its step$/, (_, a) => `Gesendet · ${rcA(a)} wird gebeten, den Schritt abzubrechen`],
    [/^(.+?) stopped its step · it reads this now$/, (_, a) => `${rcC(a)} hat den Schritt abgebrochen · liest das jetzt`],
    [/^Sent · (.+) reads it when its current step ends$/, (_, a) => `Gesendet · ${rcA(a)} liest es, wenn der aktuelle Schritt endet`],
    [/^Read · (.+?) stopped its step at (.+)$/, (_, a, t) => `Gelesen · ${rcA(a)} hat den Schritt um ${t} abgebrochen`],
    [/^Read · (.+?) stopped its step$/, (_, a) => `Gelesen · ${rcA(a)} hat den Schritt abgebrochen`],
    [/^Stop .+ now and send this, without waiting for its step to end$/, () => "Jetzt stoppen und dies senden, ohne das Ende des Schritts abzuwarten"],
    [/^Still open from rev (.+)$/, (_, r) => "Noch offen seit Rev." + " " + r],   // ui/docviewer.js: a carried item, the text is joined before h()
  ],
};
