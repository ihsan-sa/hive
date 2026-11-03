// Every status the dashboard shows, in one table. Tone picks the colour (tokens.css), shape keeps it
// readable without colour, and the label is always shown or in the tooltip. Adding a status is one line here.
// Shapes: dot, square, ring, half, tri, diamond.

export const STATUS = {
  // agents
  working:  { label: "working",      tone: "good",  shape: "dot",     hint: "active in the last 3 minutes, or its job is running" },
  queue:    { label: "in job queue", tone: "warn",  shape: "square",  hint: "waiting for its job to get a turn on the machine" },
  quiet:    { label: "quiet",        tone: "quiet", shape: "ring",    hint: "running, active more than 3 minutes ago, no job in the queue: on a waiter, a job outside the queue, or thinking" },
  stuck:    { label: "stuck",        tone: "bad",   shape: "tri",     hint: "running, but hasn't seen posts for it, or its process is gone" },
  queued:   { label: "queued",       tone: "info",  shape: "ring",    hint: "a post is waiting to start it" },
  limited:  { label: "limited",      tone: "warn",  shape: "half",    hint: "stopped at a usage limit, resumes on reset" },
  failed:   { label: "failed",       tone: "bad",   shape: "diamond", hint: "its last run ended with an error" },
  killed:   { label: "killed",       tone: "quiet", shape: "diamond", hint: "killed from here" },
  ended:    { label: "ended",        tone: "ended", shape: "dot",     hint: "finished its turn; a post wakes it" },
  none:     { label: "hasn't run",   tone: "ended", shape: "dot",     hint: "" },
  // conversations, for the person
  asks:     { label: "waiting for your answer", tone: "info", shape: "square", hint: "an agent asked you something and is waiting" },
  foryou:   { label: "new reply for you", tone: "info", shape: "dot", hint: "an agent wrote to you since you last opened this" },
  done:     { label: "done", tone: "ended", shape: "dot", hint: "nobody is on it; a message wakes the last agent" },
  sending:  { label: "sending", tone: "quiet", shape: "ring", hint: "" },
  outside:  { label: "",             tone: "ink",   shape: "diamond", hint: "you, or someone outside this hive" },
  // logins and providers
  inuse:    { label: "in use",       tone: "good",  shape: "dot",     hint: "the starter can give it new runs" },
  paused:   { label: "paused",       tone: "warn",  shape: "half",    hint: "no new runs until resumed" },
  atstop:   { label: "at its stop line", tone: "bad", shape: "tri",   hint: "past the line where the starter stops new runs" },
  off:      { label: "not set up",   tone: "quiet", shape: "ring",    hint: "" },
  // job results
  ok:       { label: "ok",           tone: "good",  shape: "dot",     hint: "" },
  error:    { label: "failed",       tone: "bad",   shape: "tri",     hint: "" },
};
export const status = k => STATUS[k] || STATUS.none;

// Priority tiers (kb/priorities.md). Tone names map to tokens.
export const TIERS = [
  { key: "tier1", label: "Tier 1", tone: "tier1" },
  { key: "tier2", label: "Tier 2", tone: "tier2" },
  { key: "held",  label: "Held",   tone: "held" },
  { key: "other", label: "Everything else", tone: "other" },
];
export const tier = k => TIERS.find(t => t.key === k) || TIERS[3];
