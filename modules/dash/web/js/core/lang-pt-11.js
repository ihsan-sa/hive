// Portuguese for the 23 words the Link buy card, Settings' repo line and the document viewer's carried note added after lang-pt-10.js
// (product-test keeper round 2026-11-24: drive_app_lang_fr failed on them in an early part-3 run; `node test/lang_left.mjs count` showed 23 in every
// language but Spanish). Same keys as lang-es-11.js's. Merged in lang.js, first in its language.
// E203n: the Stop and send receipt (ui/message.js); rcA names the agent inside it, rcC the same at the start of a line
const rcA = a => /^the agent$/i.test(a) ? "o agente" : a.replace(/\bAgent ([A-Z]{1,2})\b/g, "Agente $1").replace(/ and /g, " e ");
const rcC = a => { const w = rcA(a); return w.charAt(0).toUpperCase() + w.slice(1); };
const to = w => w.startsWith("o ") ? "ao " + w.slice(2) : "a " + w;
export default {
  "Bought": "Comprado",
  "Canceled": "Cancelado",
  "Card": "Cartão",
  "Card expired": "Cartão expirado",
  "Charged": "Cobrado",
  "Code, issues and updates are on": "Código, issues e novidades estão no",
  "Declined": "Recusado",
  "Expired": "Expirado",
  "It has no merchant or amount.": "Não tem vendedor nem valor.",
  "It isn't JSON.": "Não é JSON.",
  "It isn't a buy.": "Não é uma compra.",
  "Item": "Item",
  "Merchant": "Vendedor",
  "One-time card from Link, made on approval": "Cartão de uso único do Link, criado na aprovação",
  "Receipt": "Recibo",
  "Recording": "Gravação",
  "Shipping": "Frete",
  "Should have lapsed": "Deveria ter expirado",
  "Still open from rev": "Ainda aberto desde a rev.",
  "Tax": "Imposto",
  "Waiting for Link": "Aguardando o Link",
  "Waiting in Link": "Aguardando no Link",
  "test mode": "modo de teste",
  "Stop and send": "Parar e enviar",
  "Send waits for the current step to end. Stop and send tries to cut that step short.": "Enviar espera o passo atual terminar. “Parar e enviar” tenta encurtar esse passo.",
  __patterns: [
    [/^Interrupt sent · asking (.+) to stop its step$/, (_, a) => `Interrupção enviada · pedindo ${to(rcA(a))} que pare o passo`],
    [/^Sent · asking (.+) to stop its step$/, (_, a) => `Enviado · pedindo ${to(rcA(a))} que pare o passo`],
    [/^(.+?) stopped its step · it reads this now$/, (_, a) => `${rcC(a)} parou o passo · lê isto agora`],
    [/^Sent · (.+) reads it when its current step ends$/, (_, a) => `Enviado · ${rcA(a)} lê quando o passo atual terminar`],
    [/^Read · (.+?) stopped its step at (.+)$/, (_, a, t) => `Lido · ${rcA(a)} parou o passo às ${t}`],
    [/^Read · (.+?) stopped its step$/, (_, a) => `Lido · ${rcA(a)} parou o passo`],
    [/^Stop .+ now and send this, without waiting for its step to end$/, () => "Parar agora e enviar isto, sem esperar o fim da etapa"],
    [/^Still open from rev (.+)$/, (_, r) => "Ainda aberto desde a rev." + " " + r],   // ui/docviewer.js: a carried item, the text is joined before h()
  ],
};
