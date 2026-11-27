// id: Settings › Projects, export a project and bring one in (settings.js projectsBlock, projJob); product-test rung, 2026-11-27.
// Exact English keys; the patterns are the status lines with a project or file name in them.
const W = {
  // E207: the line in a conversation's reply place while it shows the copy kept on this device (views/comms.js readOnly)
  "Offline. This is the copy kept on this device, read only; replies need a connection.": "Offline. Ini salinan yang disimpan di perangkat ini, hanya baca; membalas perlu koneksi.",
  "Showing the chats this device last read. Trying again every few seconds.": "Menampilkan chat yang terakhir dibaca di perangkat ini. Mencoba lagi setiap beberapa detik.",
  "On, with the first line of each reply. It shows on your phone's lock screen and passes through ntfy.sh, a public service. Anyone who learns the topic below can read those lines, so keep it to yourself.": "Aktif, dengan baris pertama setiap balasan. Baris itu tampil di layar kunci ponsel dan lewat ntfy.sh, layanan publik. Siapa pun yang tahu topik di bawah bisa membaca baris itu, jadi simpan sendiri.",
  "Show the reply's first line in the alert": "Tampilkan baris pertama balasan di peringatan",
  "Hide the reply's first line": "Sembunyikan baris pertama balasan",
  "The alerts now carry the reply's first line.": "Peringatan sekarang memuat baris pertama balasan.",
  "The alerts no longer carry any words from a reply.": "Peringatan tidak lagi memuat kata apa pun dari balasan.",
  "Off: the alert only says something is waiting, so nothing from a reply shows on the lock screen.": "Nonaktif: peringatan hanya bilang ada yang menunggu, jadi tidak ada isi balasan di layar kunci.",
  "Anyone who can see your phone's lock screen can read that line.": "Siapa pun yang bisa melihat layar kunci ponselmu bisa membaca baris itu.",
  "it stopped without saying why": "berhenti tanpa menyebutkan alasannya",
  "your Hive restarted while this ran; start it again": "Hive Anda dimulai ulang saat ini berjalan; mulai lagi",
  "this sign-in can't follow it any more (full access ended after a while idle); it goes on running on your Hive, so sign in again and look in Projects": "sesi masuk ini tidak bisa mengikutinya lagi (akses penuh berakhir setelah lama tidak aktif); prosesnya tetap berjalan di Hive Anda, jadi masuk lagi dan lihat di Proyek",
  "your Hive stopped answering; it may still be running, so look in Projects in a while": "Hive Anda berhenti merespons; mungkin masih berjalan, jadi lihat di Proyek sebentar lagi",
  "Move a project to another Hive, with its history. Export saves one as a zip on this device; Bring in adds one from such a zip.": "Pindahkan proyek ke Hive lain, beserta riwayatnya. Ekspor menyimpannya sebagai zip di perangkat ini; Bawa masuk menambahkannya dari zip seperti itu.",
  "Project to export": "Proyek yang diekspor",
  "No projects yet": "Belum ada proyek",
  "Couldn't read the projects": "Tidak bisa membaca proyek",
  "Export": "Ekspor",
  "Export the project as a zip": "Ekspor proyek sebagai zip",
  "its name here (optional)": "namanya di sini (opsional)",
  "Name for the project brought in": "Nama untuk proyek yang dibawa masuk",
  "Bring in a project…": "Bawa masuk proyek…",
  "Bring in a project from a zip": "Bawa masuk proyek dari zip",
  "What it did": "Yang dilakukan",
  "Bring in a project": "Bawa masuk proyek",
  "Exporting": "Mengekspor",
  "Bringing in": "Membawa masuk",
};
const er = e => W[e] || e;   // the job's own error words, when they are these
export default {
  ...W,
  __patterns: [
    [/^Exporting (.+?)(…?)$/, (_, n, d) => `Mengekspor ${n}` + d],
    [/^Bringing in (.+?)(…?)$/, (_, n, d) => `Membawa masuk ${n}` + d],
    [/^(.+) is downloading\. Bring it in on the other Hive with "Bring in a project", here in its Settings\.$/, (_, f) => `${f} sedang diunduh. Bawa masuk di Hive lain dengan "Bawa masuk proyek", di Pengaturannya.`],
    [/^(.+) wasn't exported: (.+)$/, (_, n, x) => { const e = er(x); return `${n} tidak diekspor: ${e}`; }],
    [/^Brought in as (.+): it's in Projects now\.$/, (_, n) => `Dibawa masuk sebagai ${n}: sekarang ada di Proyek.`],
    [/^(.+) wasn't brought in: (.+)$/, (_, n, x) => { const e = er(x); return `${n} tidak dibawa masuk: ${e}`; }],
  ],
};
