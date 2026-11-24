// Italian for the 23 words the Link buy card, Settings' repo line and the document viewer's carried note added after lang-it-10.js
// (product-test keeper round 2026-11-24: drive_app_lang_fr failed on them in an early part-3 run; `node test/lang_left.mjs count` showed 23 in every
// language but Spanish). Same keys as lang-es-11.js's. Merged in lang.js, first in its language.
export default {
  "Bought": "Acquistato",
  "Canceled": "Annullato",
  "Card": "Carta",
  "Card expired": "Carta scaduta",
  "Charged": "Addebitato",
  "Code, issues and updates are on": "Codice, segnalazioni e novità sono su",
  "Declined": "Rifiutato",
  "Expired": "Scaduto",
  "It has no merchant or amount.": "Non ha né venditore né importo.",
  "It isn't JSON.": "Non è JSON.",
  "It isn't a buy.": "Non è un acquisto.",
  "Item": "Articolo",
  "Merchant": "Venditore",
  "One-time card from Link, made on approval": "Carta monouso di Link, creata all'approvazione",
  "Receipt": "Ricevuta",
  "Recording": "Registrazione",
  "Shipping": "Spedizione",
  "Should have lapsed": "Sarebbe dovuto scadere",
  "Still open from rev": "Ancora aperto dalla rev.",
  "Tax": "Imposta",
  "Waiting for Link": "In attesa di Link",
  "Waiting in Link": "In attesa in Link",
  "test mode": "modalità di test",
  __patterns: [
    [/^Still open from rev (.+)$/, (_, r) => "Ancora aperto dalla rev." + " " + r],   // ui/docviewer.js: a carried item, the text is joined before h()
  ],
};
