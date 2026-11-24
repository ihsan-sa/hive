// Portuguese for the 23 words the Link buy card, Settings' repo line and the document viewer's carried note added after lang-pt-10.js
// (product-test keeper round 2026-11-24: drive_app_lang_fr failed on them in an early part-3 run; `node test/lang_left.mjs count` showed 23 in every
// language but Spanish). Same keys as lang-es-11.js's. Merged in lang.js, first in its language.
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
  __patterns: [
    [/^Still open from rev (.+)$/, (_, r) => "Ainda aberto desde a rev." + " " + r],   // ui/docviewer.js: a carried item, the text is joined before h()
  ],
};
