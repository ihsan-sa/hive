// Indonesian for the 23 words the Link buy card, Settings' repo line and the document viewer's carried note added after lang-id-10.js
// (product-test keeper round 2026-11-24: drive_app_lang_fr failed on them in an early part-3 run; `node test/lang_left.mjs count` showed 23 in every
// language but Spanish). Same keys as lang-es-11.js's. Merged in lang.js, first in its language.
// E203n: the Stop and send receipt (ui/message.js); rcA names the agent inside it, rcC the same at the start of a line
const rcA = a => /^the agent$/i.test(a) ? "agen" : a.replace(/\bAgent ([A-Z]{1,2})\b/g, "Agen $1").replace(/ and /g, " dan ");
const rcC = a => { const w = rcA(a); return w.charAt(0).toUpperCase() + w.slice(1); };
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
  "Stop and send": "Hentikan dan kirim",
  "Send waits for the current step to end. Stop and send tries to cut that step short.": "Kirim menunggu langkah saat ini selesai. “Hentikan dan kirim” mencoba memotong langkah itu.",
  __patterns: [
    [/^Interrupt sent · asking (.+) to stop its step$/, (_, a) => `Interupsi terkirim · meminta ${rcA(a)} menghentikan langkahnya`],
    [/^Sent · asking (.+) to stop its step$/, (_, a) => `Terkirim · meminta ${rcA(a)} menghentikan langkahnya`],
    [/^(.+?) stopped its step · it reads this now$/, (_, a) => `${rcC(a)} menghentikan langkahnya · membaca ini sekarang`],
    [/^Sent · (.+) reads it when its current step ends$/, (_, a) => `Terkirim · ${rcA(a)} membacanya saat langkah saat ini selesai`],
    [/^Read · (.+?) stopped its step at (.+)$/, (_, a, t) => `Dibaca · ${rcA(a)} menghentikan langkahnya pukul ${t}`],
    [/^Read · (.+?) stopped its step$/, (_, a) => `Dibaca · ${rcA(a)} menghentikan langkahnya`],
    [/^Stop .+ now and send this, without waiting for its step to end$/, () => "Hentikan sekarang dan kirim ini, tanpa menunggu langkahnya selesai"],
    [/^Still open from rev (.+)$/, (_, r) => "Masih terbuka sejak rev." + " " + r],   // ui/docviewer.js: a carried item, the text is joined before h()
  ],
};
