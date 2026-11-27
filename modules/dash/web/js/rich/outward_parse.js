// An outward fence (modules/outward, h2-out draft): one email waiting for the person's signed approval, read into what the card shows.
// Pure, so node tests it. The words are rebuilt here from the fields the card shows, never taken from the fence, so what the person
// reads is what the Approve button asks root's card to sign; root's job then sends only an item whose rebuilt words match that word.
// words() must stay the same as outward.py words() and root/outward_job.py words(): modules/outward/testdata/words.json holds all three.
const ID = /^out-[0-9a-f]{8}$/;
const ADDR = /^[a-z0-9._%+-]{1,64}@(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,24}$/;
const PLAIN = /^[\x20-\x7e\n‘’“”–—…]*$/;   // root's card takes these and nothing else (rootword.js cardWhy)
const MAX = 1500;   // root's writer.py MAX_ACTION
const STATE = { waiting: ["waiting", "Waiting for you"], sent: ["good", "Sent"], refused: ["bad", "Refused"], failed: ["bad", "Failed"],
  cancelled: ["off", "Cancelled"], expired: ["off", "Lapsed"] };
const CHANNEL = { email: "Email" };   // x.com is a slot in outward.py, not built: a fence for it draws as code
const str = (v, n) => typeof v === "string" ? v.slice(0, n) : "";

export const collapse = s => String(s).split(/[ \t\n\r\f\v]+/).filter(Boolean).join(" ");
/** The line after "yes: " that the person signs. */
export const words = it => collapse(`send email ${it.id} to ${it.to}, subject "${it.subject}", text: ${it.body}`);

/** The card's model, or throws when the fence isn't an outward item this card can show truthfully. */
export function parseOutward(code, now = Date.now()) {
  let j;
  try { j = JSON.parse(code); } catch { throw new Error("It isn't JSON."); }
  if (!j || typeof j !== "object" || Array.isArray(j)) throw new Error("It isn't an outward item.");
  const id = str(j.id, 20), to = str(j.to, 260), subject = str(j.subject, 400), body = str(j.body, 4000), channel = str(j.channel, 20);
  if (!ID.test(id) || !CHANNEL[channel] || !ADDR.test(to) || !subject || !body) throw new Error("It has no id, channel, address, subject or body.");
  if (/["\n]/.test(subject)) throw new Error("Its subject can't be quoted.");
  const w = words({ id, to, subject, body });
  const st = str(j.state, 20) || "waiting", [tone, label] = STATE[st] || ["off", "Unknown"];
  const exp = Date.parse(str(j.expires, 40)), lapsed = Number.isFinite(exp) && exp <= now;
  // Why the Approve button isn't offered, or "": the card would refuse it, or it would sign words that send nothing
  let why = "";
  if (!PLAIN.test(subject + body)) why = "It has characters root's card won't take, so it can't be approved as written.";
  else if (w.length > MAX) why = `It is ${w.length - MAX} characters over what one approval can hold.`;
  else if (typeof j.words === "string" && j.words !== "yes: " + w) why = "Its words don't match the email shown, so it isn't offered for approval.";
  else if (st !== "waiting") why = "";
  else if (lapsed) why = "It lapsed unapproved; ask for a fresh draft if it should still go.";
  return {
    id, channel: CHANNEL[channel], to, subject, body, purpose: str(j.purpose, 300), by: str(j.by, 40) || "An agent",
    state: st, tone: lapsed && st === "waiting" ? "off" : tone, label: lapsed && st === "waiting" ? "Lapsed" : label, why,
    note: str(j.why, 200), words: "yes: " + w, approvable: st === "waiting" && !lapsed && !why,
    expires: Number.isFinite(exp) ? exp : null,
  };
}
