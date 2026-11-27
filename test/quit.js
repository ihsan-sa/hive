// Closes an app Playwright launched and waits for its process to end, not for its output pipes to close (Playwright's own
// close waits for those). On Windows a process the app starts can inherit the pipes and outlive the app by design: the
// hidden keep-alive that holds the Linux system up (lib.openCommand). Playwright's close would then never return, and
// the run sat there until its time limit (run 45929611376). The app's own process ending is what a person sees.
module.exports = async function quit(app, ms = 90000) {
  const p = app.process();
  const gone = new Promise(r => { if (p.exitCode !== null || p.signalCode !== null) r(true); else p.once('exit', () => r(true)); });
  app.close().catch(() => { });
  let t;
  const ok = await Promise.race([gone, new Promise(r => { t = setTimeout(() => r(false), ms); })]);
  clearTimeout(t);
  if (!ok) throw new Error(`the app (pid ${p.pid}) was still running ${ms / 1000} s after it was asked to quit`);
  // Let go of the pipes, so a process still holding their other end doesn't keep this test running either.
  for (const s of [p.stdin, p.stdout, p.stderr]) { try { s && s.destroy(); } catch { } }
};
