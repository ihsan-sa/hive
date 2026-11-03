// Upload one file to the dash: {ref} for /api/post. A 502 or 503 from the tunnel, or a dropped connection, means the file didn't land, so it's tried again for a while
// before the person is told. Anything else the dash says (too big, signed out) is an answer and goes straight back.
const WAIT_MS = [1000, 2000, 4000, 8000, 15000];
const pause = ms => new Promise(r => setTimeout(r, ms));

export async function uploadFile(f) {
  for (let i = 0; ; i++) {
    let r = null;
    try { r = await fetch("/api/upload?name=" + encodeURIComponent(f.name), { method: "POST", headers: { "X-Hive-Dash": "1", "Content-Type": "application/octet-stream" }, body: f }); }
    catch (e) { if (i >= WAIT_MS.length) throw Error("the connection to your Hive dropped while uploading " + f.name); }
    if (r && ![502, 503, 504].includes(r.status)) {
      const j = await r.json().catch(() => ({}));
      if (!r.ok) throw Error(j.error || "the upload of " + f.name + " failed (" + r.status + ")");
      return j.ref;
    }
    if (i >= WAIT_MS.length) throw Error("your Hive couldn't be reached to upload " + f.name + " (" + r.status + ")");
    await pause(WAIT_MS[i]);
  }
}
