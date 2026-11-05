// Signs what the person sends (thread signed-board). One P-256 key per browser, made here and kept in IndexedDB as a non-extractable CryptoKey: no script can read it out,
// though a page can ask it to sign, which is why core/signed.py calls this evidence and not a word. Signing never gets in the way of sending: anything wrong and the post goes unsigned.
const DB = "h2-signing", STORE = "keys", ME = "device", WAIT = 2000;
const b64 = buf => btoa(String.fromCharCode(...new Uint8Array(buf))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
const req = r => new Promise((ok, no) => { r.onsuccess = () => ok(r.result); r.onerror = () => no(r.error); });

const open = () => { const r = indexedDB.open(DB, 1); r.onupgradeneeded = () => r.result.createObjectStore(STORE); return req(r); };
let held = null;
const device = () => held ||= (async () => {
  const db = await open();
  const got = await req(db.transaction(STORE).objectStore(STORE).get(ME));
  if (got) return got;
  const pair = await crypto.subtle.generateKey({ name: "ECDSA", namedCurve: "P-256" }, false, ["sign"]);
  const key = b64(await crypto.subtle.exportKey("raw", pair.publicKey));
  const tx = db.transaction(STORE, "readwrite").objectStore(STORE);   // another tab may have just made one: first one stays
  try { await req(tx.add({ priv: pair.privateKey, key }, ME)); } catch (e) { return req(db.transaction(STORE).objectStore(STORE).get(ME)); }
  return { priv: pair.privateKey, key };
})().catch(e => { held = null; throw e; });

const hex = (buf, n) => [...new Uint8Array(buf)].slice(0, n).map(b => b.toString(16).padStart(2, "0")).join("");
const unb64 = s => Uint8Array.from(atob(s.replace(/-/g, "+").replace(/_/g, "/")), c => c.charCodeAt(0));

// The first 80 bits of the SHA-256 of the raw public key, as core/signed.py's fingerprint(). "" where this browser can't sign.
export async function deviceFingerprint() {
  try { return hex(await crypto.subtle.digest("SHA-256", unb64((await device()).key)), 10); }
  catch (e) { return ""; }
}

// The bytes must match core/signed.py's message().
async function sign(body) {
  const text = String(body.text || "").trim();
  if (!text) return body;
  const d = await device();
  const sig = { v: 1, key: d.key, at: new Date().toISOString(), nonce: hex(crypto.getRandomValues(new Uint8Array(12)), 12), thread: String(body.thread || ""), reply_to: String(body.reply_to || "") };
  const msg = ["h2-post-v1", sig.at, sig.nonce, sig.thread, sig.reply_to, text].join("\n");
  sig.s = b64(await crypto.subtle.sign({ name: "ECDSA", hash: "SHA-256" }, d.priv, new TextEncoder().encode(msg)));
  return { ...body, sig };
}

export async function signPost(body) {
  try { return await Promise.race([sign(body), new Promise(r => setTimeout(() => r(body), WAIT))]); }
  catch (e) { return body; }
}
