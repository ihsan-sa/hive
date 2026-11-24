// German for the 23 words the Link buy card, Settings' repo line and the document viewer's carried note added after lang-de-10.js
// (product-test keeper round 2026-11-24: drive_app_lang_fr failed on them in an early part-3 run; `node test/lang_left.mjs count` showed 23 in every
// language but Spanish). Same keys as lang-es-11.js's. Merged in lang.js, first in its language.
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
  __patterns: [
    [/^Still open from rev (.+)$/, (_, r) => "Noch offen seit Rev." + " " + r],   // ui/docviewer.js: a carried item, the text is joined before h()
  ],
};
