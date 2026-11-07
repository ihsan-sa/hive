// The one line a project card shows for a result post: what was done, in words a person reads.
// An agent's result post has a first line (the gist, but often a label or code) and a last line "Done: …" (a verdict, sometimes
// "rung ticked"). Both already exist; this picks the plainer of the two. No agent writes anything extra for it.
const LABEL = /^(?:@?[a-z]\w{3,5}[:,]\s*|thanks?\b[^.]{0,30}[.:]\s*|checked and taken\b[^.]*\.\s*|(?:rungs?|hop|round|step)(?:\s+\d+)?(?:\s+(?:done|landed|ticked))?(?:\s*\([^)]*\))?\s*:\s*|done\s*(?:\([^)]*\))?\s*:\s*)/i;
const EMPTY = /^(?:rung|step|item)s?\b.{0,20}\b(?:ticked|done)\b|^(?:all )?(?:items )?(?:above|settled)\b|^[@\w]*\s*$|\bnothing (?:left|blocking|else|new|further)\b/i;
const NAMES = /\s*\(\s*a[0-9a-f]{4}\s*\)|\s*@a[0-9a-f]{4}\b/g;

/** Strip an agent's label and ids; what's left is the sentence. */
export function clean(s) {
  s = String(s || "").replace(NAMES, "").replace(/^(?:@\w+[\s,.:]*)+/, "").replace(/^[^\w"“(#]+/, "").replace(/\s+/g, " ").trim();
  for (let i = 0; i < 2; i++) s = s.replace(LABEL, "").trim();
  return s.replace(/[\s:;,–-]+$/, "");
}
/** How hard a line is to read: code words, ids, paths and hashes count against it; a thin line counts against it more. */
export function jargon(s) {
  const t = String(s || "");
  const code = (t.match(/`[^`]*`|\b[\w-]+\.(?:py|js|mjs|md|json|sh)\b|\b[0-9a-f]{7,40}\b|\b[a-z]+_[a-z_0-9]+\b|\b[ik]\d{3,4}\b|~\/|\//g) || []).length;
  const words = t.split(/\s+/).filter(Boolean).length;
  return code * 3 + (words < 4 ? 8 : 0) + (EMPTY.test(t.trim()) ? 12 : 0) + (words > 28 ? (words - 28) / 4 : 0);
}
const META = /^(?:result\b|where things stand|posting again|got (?:it|your)|noted|nothing (?:new|further)|thanks?\b|progress at|round update|answers? to|taking\b|here is|this run ended|\(this run)|\bfor relay\b|^(?:fable|opus|sonnet) view/i;
const sentence = s => { const m = s.match(/^.{30,}?[.!?](?=\s|$)/); return m ? m[0] : s; };
/** The one line for a result post. `lines` are the post's plain lines, the last one its ending ("Done: …", "Waiting: …"). Candidates: the first
 * line, the next line when the first only introduces, and the Done verdict. A Waiting or Blocked ending looks ahead, so it is never the pick.
 * The plainest wins; ties go to the first line. */
export function gist(lines, end = "") {
  end = String(end || ""); const done = /^done\s*:/i.test(end);
  const c = [lines[0], META.test(clean(lines[0])) ? lines[1] : null, done ? end.replace(/^done\s*:\s*/i, "") : null].filter(x => x != null)
    .map((t, i) => { t = clean(t); return { t: sentence(t), j: jargon(t) + (META.test(t) ? 15 : 0) + (i === 2 ? 2 : 0) }; }).filter(x => x.t && /[a-z]{3}/i.test(x.t));
  const best = c.reduce((a, b) => (b.j < a.j ? b : a), c[0] || { t: "", j: 0 });
  return best.t.replace(/^./, ch => ch.toUpperCase());
}
