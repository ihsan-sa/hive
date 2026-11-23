// fr: Settings > Connectors > Add a connector by address (views/settings.js mcpUrlBlock, E120a) and the server's reasons for a refusal
// (dash.py mcp_url_check / mcp_url_do), shown as they come. Exact English keys; patterns for the lines with a name or a count.
const SELF = {
  "Add a connector by address": "Ajouter un connecteur par son adresse",
  "A remote MCP server, like a custom connector in Claude. Your agents can use its tools from their next run, so only add a server you trust.": "Un serveur MCP distant, comme un connecteur personnalisé dans Claude. Vos agents peuvent utiliser ses outils dès leur prochaine exécution : n'ajoutez qu'un serveur de confiance.",
  "Added. Agents can use it from their next run.": "Ajouté. Les agents peuvent l'utiliser dès leur prochaine exécution.",
  "Address": "Adresse",
  "Agents stop seeing its tools from their next run.": "Les agents ne voient plus ses outils dès leur prochaine exécution.",
  "has a token": "a un jeton",
  "Token (if the server needs one)": "Jeton (si le serveur en demande un)",
  "paste the server's address": "collez l'adresse du serveur",
  "that address has spaces in it or is too long": "cette adresse contient des espaces ou est trop longue",
  "that isn't a web address": "ce n'est pas une adresse web",
  "leave the sign-in out of the address; paste a token in its own box": "laissez l'identifiant hors de l'adresse ; collez le jeton dans sa propre case",
  "the address must start with https:// (plain http only for a server on this computer)": "l'adresse doit commencer par https:// (http simple seulement pour un serveur sur cet ordinateur)",
  "no connector added by address has that name": "aucun connecteur ajouté par adresse ne porte ce nom",
  "give it a short name: letters, digits, - and _ (up to 40)": "donnez-lui un nom court : lettres, chiffres, - et _ (40 au plus)",
  "a connector with that name is already here": "un connecteur porte déjà ce nom",
  "that token can't be used": "ce jeton ne peut pas être utilisé",
  __patterns: [
    [/^Remove (.+)\?$/, "Retirer $1 ?"],
    [/^(\d+) is the most\. Remove one to add another\.$/, "$1 au maximum. Retirez-en un pour en ajouter un autre."],
    [/^(\d+) is the most; remove one first$/, "$1 au maximum ; retirez-en un d'abord"],
    [/^a connector added by address already has this name: (.+)$/, "un connecteur ajouté par adresse porte déjà ce nom : $1"],
  ],
};
export default SELF;
