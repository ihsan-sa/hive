// de: Settings › Projects, export a project and bring one in (settings.js projectsBlock, projJob); product-test rung, 2026-11-27.
// Exact English keys; the patterns are the status lines with a project or file name in them.
const W = {
  "it stopped without saying why": "es hat ohne Angabe von Gründen aufgehört",
  "your Hive restarted while this ran; start it again": "dein Hive wurde währenddessen neu gestartet; starte es noch einmal",
  "this sign-in can't follow it any more (full access ended after a while idle); it goes on running on your Hive, so sign in again and look in Projects": "diese Anmeldung kann es nicht mehr verfolgen (der volle Zugriff endete nach einer Weile ohne Aktivität); es läuft auf deinem Hive weiter, also melde dich erneut an und sieh unter Projekte nach",
  "your Hive stopped answering; it may still be running, so look in Projects in a while": "dein Hive antwortet nicht mehr; es läuft vielleicht noch, also sieh in einer Weile unter Projekte nach",
  "Move a project to another Hive, with its history. Export saves one as a zip on this device; Bring in adds one from such a zip.": "Ein Projekt samt Verlauf auf einen anderen Hive bringen. Exportieren speichert eines als ZIP auf diesem Gerät; Übernehmen fügt eines aus so einer ZIP hinzu.",
  "Project to export": "Zu exportierendes Projekt",
  "No projects yet": "Noch keine Projekte",
  "Couldn't read the projects": "Die Projekte konnten nicht gelesen werden",
  "Export": "Exportieren",
  "Export the project as a zip": "Das Projekt als ZIP exportieren",
  "its name here (optional)": "sein Name hier (optional)",
  "Name for the project brought in": "Name für das übernommene Projekt",
  "Bring in a project…": "Projekt übernehmen…",
  "Bring in a project from a zip": "Ein Projekt aus einer ZIP übernehmen",
  "What it did": "Was es getan hat",
  "Bring in a project": "Projekt übernehmen",
  "Exporting": "Exportiere",
  "Bringing in": "Übernehme",
};
const er = e => W[e] || e;   // the job's own error words, when they are these
export default {
  ...W,
  __patterns: [
    [/^Exporting (.+?)(…?)$/, (_, n, d) => `Exportiere ${n}` + d],
    [/^Bringing in (.+?)(…?)$/, (_, n, d) => `Übernehme ${n}` + d],
    [/^(.+) is downloading\. Bring it in on the other Hive with "Bring in a project", here in its Settings\.$/, (_, f) => `${f} wird heruntergeladen. Übernimm es auf dem anderen Hive mit „Projekt übernehmen“ in dessen Einstellungen.`],
    [/^(.+) wasn't exported: (.+)$/, (_, n, x) => { const e = er(x); return `${n} wurde nicht exportiert: ${e}`; }],
    [/^Brought in as (.+): it's in Projects now\.$/, (_, n) => `Übernommen als ${n}: es ist jetzt unter Projekte.`],
    [/^(.+) wasn't brought in: (.+)$/, (_, n, x) => { const e = er(x); return `${n} wurde nicht übernommen: ${e}`; }],
  ],
};
