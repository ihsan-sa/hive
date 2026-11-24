// French for the 23 words the Link buy card, Settings' repo line and the document viewer's carried note added after lang-fr-12.js
// (product-test keeper round 2026-11-24: drive_app_lang_fr failed on them in an early part-3 run; `node test/lang_left.mjs count` showed 23 in every
// language but Spanish). Same keys as lang-es-11.js's. Merged in lang.js, first in its language.
export default {
  "Bought": "Acheté",
  "Canceled": "Annulé",
  "Card": "Carte",
  "Card expired": "Carte expirée",
  "Charged": "Débité",
  "Code, issues and updates are on": "Le code, les tickets et les nouveautés sont sur",
  "Declined": "Refusé",
  "Expired": "Expiré",
  "It has no merchant or amount.": "Il n'a ni marchand ni montant.",
  "It isn't JSON.": "Ce n'est pas du JSON.",
  "It isn't a buy.": "Ce n'est pas un achat.",
  "Item": "Article",
  "Merchant": "Marchand",
  "One-time card from Link, made on approval": "Carte à usage unique de Link, créée à l'approbation",
  "Receipt": "Reçu",
  "Recording": "Enregistrement",
  "Shipping": "Livraison",
  "Should have lapsed": "Aurait dû expirer",
  "Still open from rev": "Toujours ouvert depuis la rév.",
  "Tax": "Taxe",
  "Waiting for Link": "En attente de Link",
  "Waiting in Link": "En attente dans Link",
  "test mode": "mode test",
  __patterns: [
    [/^Still open from rev (.+)$/, (_, r) => "Toujours ouvert depuis la rév." + " " + r],   // ui/docviewer.js: a carried item, the text is joined before h()
  ],
};
