// id: Settings > Connectors > Add a connector by address (views/settings.js mcpUrlBlock, E120a) and the server's reasons for a refusal
// (dash.py mcp_url_check / mcp_url_do), shown as they come. Exact English keys; patterns for the lines with a name or a count.
const SELF = {
  "Add a connector by address": "Tambahkan konektor lewat alamatnya",
  "A remote MCP server, like a custom connector in Claude. Your agents can use its tools from their next run, so only add a server you trust.": "Server MCP jarak jauh, seperti konektor kustom di Claude. Agen Anda bisa memakai alatnya mulai run berikutnya, jadi tambahkan hanya server yang Anda percayai.",
  "Added. Agents can use it from their next run.": "Ditambahkan. Agen bisa memakainya mulai run berikutnya.",
  "Address": "Alamat",
  "Agents stop seeing its tools from their next run.": "Agen tidak lagi melihat alatnya mulai run berikutnya.",
  "has a token": "punya token",
  "Token (if the server needs one)": "Token (jika server memintanya)",
  "paste the server's address": "tempel alamat server",
  "that address has spaces in it or is too long": "alamat itu berisi spasi atau terlalu panjang",
  "that isn't a web address": "itu bukan alamat web",
  "leave the sign-in out of the address; paste a token in its own box": "jangan masukkan info masuk ke alamat; tempel token di kotaknya sendiri",
  "the address must start with https:// (plain http only for a server on this computer)": "alamat harus diawali https:// (http biasa hanya untuk server di komputer ini)",
  "no connector added by address has that name": "tidak ada konektor yang ditambahkan lewat alamat dengan nama itu",
  "give it a short name: letters, digits, - and _ (up to 40)": "beri nama pendek: huruf, angka, - dan _ (maks. 40)",
  "a connector with that name is already here": "sudah ada konektor dengan nama itu",
  "that token can't be used": "token itu tidak bisa dipakai",
  __patterns: [
    [/^Remove (.+)\?$/, "Hapus $1?"],
    [/^(\d+) is the most\. Remove one to add another\.$/, "Maksimal $1. Hapus satu untuk menambah yang lain."],
    [/^(\d+) is the most; remove one first$/, "maksimal $1; hapus satu dulu"],
    [/^a connector added by address already has this name: (.+)$/, "konektor yang ditambahkan lewat alamat sudah memakai nama ini: $1"],
  ],
};
export default SELF;
