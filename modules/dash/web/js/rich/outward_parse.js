// An outward fence (modules/outward, h2-out draft): one email waiting for the person's signed approval, read into what the card shows.
// Pure, so node tests it. The words are rebuilt here from the fields the card shows, never taken from the fence, so what the person
// reads is what the Approve button asks root's card to sign; root's job then sends only an item whose rebuilt words match that word.
// words() must stay the same as outward.py words() and root/outward_job.py words(): modules/outward/testdata/words.json holds all three.
const ID = /^out-[0-9a-f]{8}$/;
const ADDR = /^[a-z0-9._%+-]{1,64}@(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,24}$/;
const FROM = /^(?:hello|quotes|parts|hardware|support|accounts)@hivelab\.ca$/;   // outward.py ROLES: role addresses only
const HEX = /^[0-9a-f]{64}$/;
const KIND = { quote: "Quote request", followup: "Follow-up", person: "To a person", commits: "Commits" };
const FTYPE = new Set(["png", "jpeg", "pdf", "dxf", "step", "zip"]);
const PLAIN = /^[\x20-\x7e\n‘’“”–—…]*$/;   // root's card takes these and nothing else (rootword.js cardWhy)
const MAX = 1500;   // root's writer.py MAX_ACTION
const STATE = { waiting: ["waiting", "Waiting for you"], sent: ["good", "Sent"], refused: ["bad", "Refused"], failed: ["bad", "Failed"],
  cancelled: ["off", "Cancelled"], expired: ["off", "Lapsed"] };
const CHANNEL = { email: "Email" };   // x.com is a slot in outward.py, not built: a fence for it draws as code
const str = (v, n) => typeof v === "string" ? v.slice(0, n) : "";

export const collapse = s => String(s).split(/[ \t\n\r\f\v]+/).filter(Boolean).join(" ");
/** The line after "yes: " that the person signs. */
export const words = it => collapse(`send email ${it.id} from ${it.from} to ${it.to}, subject "${it.subject}", ` +
  (it.files && it.files.length ? `files ${it.files.length} sha256 ${it.files_sha}, ` : "") + `text: ${it.body}`);

/** The card's model, or throws when the fence isn't an outward item this card can show truthfully. */
export function parseOutward(code, now = Date.now()) {
  let j;
  try { j = JSON.parse(code); } catch { throw new Error("It isn't JSON."); }
  if (!j || typeof j !== "object" || Array.isArray(j)) throw new Error("It isn't an outward item.");
  const id = str(j.id, 20), from = str(j.from, 80), to = str(j.to, 260), subject = str(j.subject, 400), body = str(j.body, 4000), channel = str(j.channel, 20);
  if (!ID.test(id) || !CHANNEL[channel] || !ADDR.test(to) || !FROM.test(from) || !subject || !body) throw new Error("It has no id, channel, addresses, subject or body.");
  if (/["\n]/.test(subject)) throw new Error("Its subject can't be quoted.");
  const raw = Array.isArray(j.files) ? j.files : [];
  if (raw.length > 5) throw new Error("It has too many files.");
  const files = raw.map(f => ({ name: str(f && f.name, 80), type: str(f && f.type, 10), size: f && Number.isSafeInteger(f.size) && f.size >= 0 ? f.size : -1, sha256: str(f && f.sha256, 64) }));
  if (files.some(f => !/^[A-Za-z0-9._-]{1,80}$/.test(f.name) || !FTYPE.has(f.type) || f.size < 0 || !HEX.test(f.sha256))) throw new Error("A file entry isn't one this card can show.");
  const files_sha = str(j.files_sha, 64);
  if (files.length && !HEX.test(files_sha)) throw new Error("Its files carry no sha.");
  const w = words({ id, from, to, subject, body, files, files_sha });
  const st = str(j.state, 20) || "waiting", [tone, label] = STATE[st] || ["off", "Unknown"];
  const exp = Date.parse(str(j.expires, 40)), lapsed = Number.isFinite(exp) && exp <= now;
  // Why the Approve button isn't offered, or "": the card would refuse it, or it would sign words that send nothing
  let why = "";
  if (!PLAIN.test(subject + body)) why = "It has characters root's card won't take, so it can't be approved as written.";
  else if (w.length > MAX) why = `It is ${w.length - MAX} characters over what one approval can hold.`;
  else if (typeof j.words === "string" && j.words !== "yes: " + w) why = "Its words don't match the email shown, so it isn't offered for approval.";
  else if (st !== "waiting") why = "";
  else if (lapsed) why = "It lapsed unapproved; ask for a fresh draft if it should still go.";
  else if (files.length && j.files_clean !== true) why = "Root hasn't cleaned its files yet; a fresh card comes when it has.";
  return {
    id, channel: CHANNEL[channel], from, to, subject, body, files, files_sha, kind: KIND[str(j.kind, 20)] || "Quote request",
    holds: (Array.isArray(j.holds) ? j.holds : []).slice(0, 5).map(x => str(x, 300)).filter(Boolean), vt: /^vt-[0-9a-f]{8}$/.test(str(j.vt, 20)) ? j.vt : "", purpose: str(j.purpose, 300), by: str(j.by, 40) || "An agent",
    state: st, tone: lapsed && st === "waiting" ? "off" : tone, label: lapsed && st === "waiting" ? "Lapsed" : label, why,
    note: str(j.why, 200), words: "yes: " + w, approvable: st === "waiting" && !lapsed && !why,
    expires: Number.isFinite(exp) ? exp : null,
  };
}
