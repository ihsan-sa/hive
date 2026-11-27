// it: Settings › Projects, export a project and bring one in (settings.js projectsBlock, projJob); product-test rung, 2026-11-27.
// Exact English keys; the patterns are the status lines with a project or file name in them.
const W = {
  "it stopped without saying why": "si è fermato senza dire perché",
  "your Hive restarted while this ran; start it again": "il tuo Hive si è riavviato durante l'operazione; riavviala",
  "this sign-in can't follow it any more (full access ended after a while idle); it goes on running on your Hive, so sign in again and look in Projects": "questo accesso non può più seguirla (l'accesso completo è scaduto dopo un po' di inattività); continua sul tuo Hive, quindi accedi di nuovo e guarda in Progetti",
  "your Hive stopped answering; it may still be running, so look in Projects in a while": "il tuo Hive ha smesso di rispondere; forse è ancora in corso, quindi guarda in Progetti tra un po'",
  "Move a project to another Hive, with its history. Export saves one as a zip on this device; Bring in adds one from such a zip.": "Sposta un progetto su un altro Hive, con la sua cronologia. Esporta ne salva uno come zip su questo dispositivo; Importa ne aggiunge uno da uno zip così.",
  "Project to export": "Progetto da esportare",
  "No projects yet": "Ancora nessun progetto",
  "Couldn't read the projects": "Impossibile leggere i progetti",
  "Export": "Esporta",
  "Export the project as a zip": "Esporta il progetto come zip",
  "its name here (optional)": "il suo nome qui (facoltativo)",
  "Name for the project brought in": "Nome del progetto importato",
  "Bring in a project…": "Importa un progetto…",
  "Bring in a project from a zip": "Importa un progetto da uno zip",
  "What it did": "Cosa ha fatto",
  "Bring in a project": "Importa un progetto",
  "Exporting": "Esportazione di",
  "Bringing in": "Importazione di",
};
const er = e => W[e] || e;   // the job's own error words, when they are these
export default {
  ...W,
  __patterns: [
    [/^Exporting (.+?)(…?)$/, (_, n, d) => `Esportazione di ${n}` + d],
    [/^Bringing in (.+?)(…?)$/, (_, n, d) => `Importazione di ${n}` + d],
    [/^(.+) is downloading\. Bring it in on the other Hive with "Bring in a project", here in its Settings\.$/, (_, f) => `${f} è in download. Importalo sull'altro Hive con «Importa un progetto», nelle sue Impostazioni.`],
    [/^(.+) wasn't exported: (.+)$/, (_, n, x) => { const e = er(x); return `${n} non è stato esportato: ${e}`; }],
    [/^Brought in as (.+): it's in Projects now\.$/, (_, n) => `Importato come ${n}: ora è in Progetti.`],
    [/^(.+) wasn't brought in: (.+)$/, (_, n, x) => { const e = er(x); return `${n} non è stato importato: ${e}`; }],
  ],
};
