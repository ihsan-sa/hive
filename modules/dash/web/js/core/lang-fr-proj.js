// fr: Settings › Projects, export a project and bring one in (settings.js projectsBlock, projJob); product-test rung, 2026-11-27.
// Exact English keys; the patterns are the status lines with a project or file name in them.
const W = {
  // E207: the line in a conversation's reply place while it shows the copy kept on this device (views/comms.js readOnly)
  "Offline. This is the copy kept on this device, read only; replies need a connection.": "Hors ligne. C'est la copie gardée sur cet appareil, en lecture seule ; répondre demande une connexion.",
  "Showing the chats this device last read. Trying again every few seconds.": "Affichage des discussions lues en dernier sur cet appareil. Nouvel essai toutes les quelques secondes.",
  "On, with the first line of each reply. It shows on your phone's lock screen and passes through ntfy.sh, a public service. Anyone who learns the topic below can read those lines, so keep it to yourself.": "Activé, avec la première ligne de chaque réponse. Elle s’affiche sur l’écran verrouillé du téléphone et passe par ntfy.sh, un service public. Quiconque connaît le sujet ci-dessous peut lire ces lignes, alors gardez-le pour vous.",
  "Show the reply's first line in the alert": "Afficher la première ligne de la réponse dans l’alerte",
  "Hide the reply's first line": "Masquer la première ligne de la réponse",
  "The alerts now carry the reply's first line.": "Les alertes affichent maintenant la première ligne de la réponse.",
  "The alerts no longer carry any words from a reply.": "Les alertes ne contiennent plus aucun mot d’une réponse.",
  "Off: the alert only says something is waiting, so nothing from a reply shows on the lock screen.": "Désactivé : l’alerte dit seulement que quelque chose attend, donc rien d’une réponse ne s’affiche sur l’écran verrouillé.",
  "Anyone who can see your phone's lock screen can read that line.": "Toute personne qui voit l’écran verrouillé de votre téléphone peut lire cette ligne.",
  "it stopped without saying why": "il s'est arrêté sans dire pourquoi",
  "your Hive restarted while this ran; start it again": "votre Hive a redémarré pendant l'opération ; relancez-la",
  "this sign-in can't follow it any more (full access ended after a while idle); it goes on running on your Hive, so sign in again and look in Projects": "cette connexion ne peut plus la suivre (l'accès complet a pris fin après un moment d'inactivité) ; elle continue sur votre Hive, alors reconnectez-vous et regardez dans Projets",
  "your Hive stopped answering; it may still be running, so look in Projects in a while": "votre Hive ne répond plus ; l'opération continue peut-être, alors regardez dans Projets dans un moment",
  "Move a project to another Hive, with its history. Export saves one as a zip on this device; Bring in adds one from such a zip.": "Déplacez un projet vers un autre Hive, avec son historique. Exporter en enregistre un en zip sur cet appareil ; Importer en ajoute un depuis un tel zip.",
  "Project to export": "Projet à exporter",
  "No projects yet": "Aucun projet pour l'instant",
  "Couldn't read the projects": "Impossible de lire les projets",
  "Export": "Exporter",
  "Export the project as a zip": "Exporter le projet en zip",
  "its name here (optional)": "son nom ici (facultatif)",
  "Name for the project brought in": "Nom du projet importé",
  "Bring in a project…": "Importer un projet…",
  "Bring in a project from a zip": "Importer un projet depuis un zip",
  "What it did": "Ce qui a été fait",
  "Bring in a project": "Importer un projet",
  "Exporting": "Export de",
  "Bringing in": "Import de",
};
const er = e => W[e] || e;   // the job's own error words, when they are these
export default {
  ...W,
  __patterns: [
    [/^Exporting (.+?)(…?)$/, (_, n, d) => `Export de ${n}` + d],
    [/^Bringing in (.+?)(…?)$/, (_, n, d) => `Import de ${n}` + d],
    [/^(.+) is downloading\. Bring it in on the other Hive with "Bring in a project", here in its Settings\.$/, (_, f) => `${f} est en cours de téléchargement. Importez-le sur l'autre Hive avec « Importer un projet », dans ses Réglages.`],
    [/^(.+) wasn't exported: (.+)$/, (_, n, x) => { const e = er(x); return `${n} n'a pas été exporté : ${e}`; }],
    [/^Brought in as (.+): it's in Projects now\.$/, (_, n) => `Importé sous le nom ${n} : il est maintenant dans Projets.`],
    [/^(.+) wasn't brought in: (.+)$/, (_, n, x) => { const e = er(x); return `${n} n'a pas été importé : ${e}`; }],
  ],
};
