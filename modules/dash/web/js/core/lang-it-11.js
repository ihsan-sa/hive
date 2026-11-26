// Italian for the 23 words the Link buy card, Settings' repo line and the document viewer's carried note added after lang-it-10.js
// (product-test keeper round 2026-11-24: drive_app_lang_fr failed on them in an early part-3 run; `node test/lang_left.mjs count` showed 23 in every
// language but Spanish). Same keys as lang-es-11.js's. Merged in lang.js, first in its language.
// E203n: the Stop and send receipt (ui/message.js); rcA names the agent inside it, rcC the same at the start of a line
const rcA = a => /^the agent$/i.test(a) ? "l'agente" : a.replace(/\bAgent ([A-Z]{1,2})\b/g, "Agente $1").replace(/ and /g, " e ");
const rcC = a => { const w = rcA(a); return w.charAt(0).toUpperCase() + w.slice(1); };
const to = w => w.startsWith("l'") ? "all'" + w.slice(2) : "a " + w;
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
  "Stop and send": "Ferma e invia",
  "Send waits for the current step to end. Stop and send tries to cut that step short.": "Invia aspetta la fine del passaggio in corso. «Ferma e invia» prova a interrompere quel passaggio.",
  __patterns: [
    [/^Interrupt sent · asking (.+) to stop its step$/, (_, a) => `Interruzione inviata · richiesta ${to(rcA(a))} di fermare il passo`],
    [/^Sent · asking (.+) to stop its step$/, (_, a) => `Inviato · richiesta ${to(rcA(a))} di fermare il passo`],
    [/^(.+?) stopped its step · it reads this now$/, (_, a) => `${rcC(a)} ha fermato il passo · lo legge ora`],
    [/^Sent · (.+) reads it when its current step ends$/, (_, a) => `Inviato · ${rcA(a)} lo legge quando termina il passo attuale`],
    [/^Read · (.+?) stopped its step at (.+)$/, (_, a, t) => `Letto · ${rcA(a)} ha fermato il passo alle ${t}`],
    [/^Read · (.+?) stopped its step$/, (_, a) => `Letto · ${rcA(a)} ha fermato il passo`],
    [/^Stop .+ now and send this, without waiting for its step to end$/, () => "Fermalo ora e invia questo, senza aspettare la fine del passaggio"],
    [/^Still open from rev (.+)$/, (_, r) => "Ancora aperto dalla rev." + " " + r],   // ui/docviewer.js: a carried item, the text is joined before h()
  ],
};
