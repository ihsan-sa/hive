// de: Settings › Projects, export a project and bring one in (settings.js projectsBlock, projJob); product-test rung, 2026-11-27.
// Exact English keys; the patterns are the status lines with a project or file name in them.
const W = {
  // E207: the line in a conversation's reply place while it shows the copy kept on this device (views/comms.js readOnly)
  "Offline. This is the copy kept on this device, read only; replies need a connection.": "Offline. Das ist die auf diesem Gerät gespeicherte Kopie, nur zum Lesen; Antworten brauchen eine Verbindung.",
  "Showing the chats this device last read. Trying again every few seconds.": "Zeige die Chats, die dieses Gerät zuletzt gelesen hat. Neuer Versuch alle paar Sekunden.",
  "On, with the first line of each reply. It shows on your phone's lock screen and passes through ntfy.sh, a public service. Anyone who learns the topic below can read those lines, so keep it to yourself.": "Ein, mit der ersten Zeile jeder Antwort. Sie erscheint auf dem Sperrbildschirm deines Handys und läuft über ntfy.sh, einen öffentlichen Dienst. Wer das Thema unten kennt, kann diese Zeilen lesen, also behalte es für dich.",
  "Show the reply's first line in the alert": "Erste Zeile der Antwort in der Benachrichtigung zeigen",
  "Hide the reply's first line": "Erste Zeile der Antwort ausblenden",
  "The alerts now carry the reply's first line.": "Die Benachrichtigungen zeigen jetzt die erste Zeile der Antwort.",
  "The alerts no longer carry any words from a reply.": "Die Benachrichtigungen enthalten keine Wörter aus einer Antwort mehr.",
  "Off: the alert only says something is waiting, so nothing from a reply shows on the lock screen.": "Aus: Die Benachrichtigung sagt nur, dass etwas wartet, also erscheint nichts aus einer Antwort auf dem Sperrbildschirm.",
  "Anyone who can see your phone's lock screen can read that line.": "Wer den Sperrbildschirm deines Handys sieht, kann diese Zeile lesen.",
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
