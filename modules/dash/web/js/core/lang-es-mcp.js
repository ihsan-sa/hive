// es: Settings > Connectors > Add a connector by address (views/settings.js mcpUrlBlock, E120a) and the server's reasons for a refusal
// (dash.py mcp_url_check / mcp_url_do), shown as they come. Exact English keys; patterns for the lines with a name or a count.
const SELF = {
  "Add a connector by address": "Añadir un conector por su dirección",
  "A remote MCP server, like a custom connector in Claude. Your agents can use its tools from their next run, so only add a server you trust.": "Un servidor MCP remoto, como un conector personalizado en Claude. Tus agentes pueden usar sus herramientas desde su próxima ejecución, así que añade solo un servidor en el que confíes.",
  "Added. Agents can use it from their next run.": "Añadido. Los agentes pueden usarlo desde su próxima ejecución.",
  "Address": "Dirección",
  "Agents stop seeing its tools from their next run.": "Los agentes dejan de ver sus herramientas desde su próxima ejecución.",
  "has a token": "tiene un token",
  "Token (if the server needs one)": "Token (si el servidor lo pide)",
  "paste the server's address": "pega la dirección del servidor",
  "that address has spaces in it or is too long": "esa dirección tiene espacios o es demasiado larga",
  "that isn't a web address": "eso no es una dirección web",
  "leave the sign-in out of the address; paste a token in its own box": "deja el inicio de sesión fuera de la dirección; pega el token en su propia casilla",
  "the address must start with https:// (plain http only for a server on this computer)": "la dirección debe empezar por https:// (http sin cifrar solo para un servidor en este ordenador)",
  "no connector added by address has that name": "ningún conector añadido por dirección tiene ese nombre",
  "give it a short name: letters, digits, - and _ (up to 40)": "ponle un nombre corto: letras, dígitos, - y _ (hasta 40)",
  "a connector with that name is already here": "ya hay un conector con ese nombre",
  "that token can't be used": "ese token no se puede usar",
  __patterns: [
    [/^Remove (.+)\?$/, "¿Quitar $1?"],
    [/^(\d+) is the most\. Remove one to add another\.$/, "$1 es el máximo. Quita uno para añadir otro."],
    [/^(\d+) is the most; remove one first$/, "$1 es el máximo; quita uno primero"],
    [/^a connector added by address already has this name: (.+)$/, "un conector añadido por dirección ya tiene este nombre: $1"],
  ],
};
export default SELF;
