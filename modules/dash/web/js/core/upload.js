// Upload one file to the dash: {ref} for /api/post. A 502 or 503 from the tunnel, or a dropped connection, means the file didn't land, so it's tried again for a while
// before the person is told. Anything else the dash says (too big, signed out) is an answer and goes straight back.
import { said } from "./store.js";
const WAIT_MS = [1000, 2000, 4000, 8000, 15000];
const pause = ms => new Promise(r => setTimeout(r, ms));

const PIECE = 32 * 1048576;   // the tunnel in front of the dash refuses one request over 100 MB
export async function uploadFile(f) {
  if (f.size > PIECE) return uploadBig(f);
  for (let i = 0; ; i++) {
    let r = null;
    try { r = await fetch("/api/upload?name=" + encodeURIComponent(f.name), { method: "POST", headers: { "X-Hive-Dash": "1", "Content-Type": "application/octet-stream" }, body: f }); }
    catch (e) {
      if (navigator.onLine === false) throw Error("your Hive couldn't be reached to upload " + f.name + ": this device is offline");   // no network at all: say so now, not after half a minute of "Uploading…"
      if (i >= WAIT_MS.length) throw Error("the connection to your Hive dropped while uploading " + f.name);
    }
    if (r && ![502, 503, 504].includes(r.status)) {
      const j = await r.json().catch(() => ({}));
      if (!r.ok) throw Error(j.error || "the upload of " + f.name + " failed: " + said(r.status));
      return j.ref;
    }
    if (i >= WAIT_MS.length) throw Error("your Hive couldn't be reached to upload " + f.name);
    await pause(WAIT_MS[i]);
  }
}

// A big file goes in pieces of 32 MB, each retried like a small file. The first reply names the upload; the last gives the ref.
async function uploadBig(f) {
  let id = "", off = 0;
  while (off < f.size) {
    const end = Math.min(off + PIECE, f.size);
    const url = "/api/upload?name=" + encodeURIComponent(f.name) + "&total=" + f.size + "&off=" + off + (id ? "&id=" + id : "");
    for (let i = 0; ; i++) {
      let r = null;
      try { r = await fetch(url, { method: "POST", headers: { "X-Hive-Dash": "1", "Content-Type": "application/octet-stream" }, body: f.slice(off, end) }); }
      catch (e) {
        if (navigator.onLine === false) throw Error("your Hive couldn't be reached to upload " + f.name + ": this device is offline");
        if (i >= WAIT_MS.length) throw Error("the connection to your Hive dropped while uploading " + f.name);
      }
      if (r && ![502, 503, 504].includes(r.status)) {
        const j = await r.json().catch(() => ({}));
        if (!r.ok) throw Error(j.error || "the upload of " + f.name + " failed: " + said(r.status));
        if (end >= f.size) return j.ref;
        id = j.id; break;
      }
      if (i >= WAIT_MS.length) throw Error("your Hive couldn't be reached to upload " + f.name);
      await pause(WAIT_MS[i]);
    }
    off = end;
  }
}
