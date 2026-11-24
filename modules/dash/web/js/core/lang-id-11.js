// Indonesian for the 23 words the Link buy card, Settings' repo line and the document viewer's carried note added after lang-id-10.js
// (product-test keeper round 2026-11-24: drive_app_lang_fr failed on them in an early part-3 run; `node test/lang_left.mjs count` showed 23 in every
// language but Spanish). Same keys as lang-es-11.js's. Merged in lang.js, first in its language.
export default {
  "Bought": "Dibeli",
  "Canceled": "Dibatalkan",
  "Card": "Kartu",
  "Card expired": "Kartu kedaluwarsa",
  "Charged": "Ditagih",
  "Code, issues and updates are on": "Kode, issue, dan pembaruan ada di",
  "Declined": "Ditolak",
  "Expired": "Kedaluwarsa",
  "It has no merchant or amount.": "Tidak ada penjual atau jumlahnya.",
  "It isn't JSON.": "Ini bukan JSON.",
  "It isn't a buy.": "Ini bukan pembelian.",
  "Item": "Barang",
  "Merchant": "Penjual",
  "One-time card from Link, made on approval": "Kartu sekali pakai dari Link, dibuat saat disetujui",
  "Receipt": "Struk",
  "Recording": "Rekaman",
  "Shipping": "Pengiriman",
  "Should have lapsed": "Seharusnya sudah kedaluwarsa",
  "Still open from rev": "Masih terbuka sejak rev.",
  "Tax": "Pajak",
  "Waiting for Link": "Menunggu Link",
  "Waiting in Link": "Menunggu di Link",
  "test mode": "mode uji",
  __patterns: [
    [/^Still open from rev (.+)$/, (_, r) => "Masih terbuka sejak rev." + " " + r],   // ui/docviewer.js: a carried item, the text is joined before h()
  ],
};
