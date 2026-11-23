// pt: Settings > Connectors > Add a connector by address (views/settings.js mcpUrlBlock, E120a) and the server's reasons for a refusal
// (dash.py mcp_url_check / mcp_url_do), shown as they come. Exact English keys; patterns for the lines with a name or a count.
const SELF = {
  "Add a connector by address": "Adicionar um conector pelo endereço",
  "A remote MCP server, like a custom connector in Claude. Your agents can use its tools from their next run, so only add a server you trust.": "Um servidor MCP remoto, como um conector personalizado no Claude. Seus agentes podem usar as ferramentas dele a partir da próxima execução, então adicione só um servidor em que você confia.",
  "Added. Agents can use it from their next run.": "Adicionado. Os agentes podem usá-lo a partir da próxima execução.",
  "Address": "Endereço",
  "Agents stop seeing its tools from their next run.": "Os agentes deixam de ver as ferramentas dele a partir da próxima execução.",
  "has a token": "tem um token",
  "Token (if the server needs one)": "Token (se o servidor pedir um)",
  "paste the server's address": "cole o endereço do servidor",
  "that address has spaces in it or is too long": "esse endereço tem espaços ou é longo demais",
  "that isn't a web address": "isso não é um endereço web",
  "leave the sign-in out of the address; paste a token in its own box": "deixe o login fora do endereço; cole o token na caixa dele",
  "the address must start with https:// (plain http only for a server on this computer)": "o endereço precisa começar com https:// (http simples só para um servidor neste computador)",
  "no connector added by address has that name": "nenhum conector adicionado por endereço tem esse nome",
  "give it a short name: letters, digits, - and _ (up to 40)": "dê um nome curto: letras, números, - e _ (até 40)",
  "a connector with that name is already here": "já existe um conector com esse nome",
  "that token can't be used": "esse token não pode ser usado",
  __patterns: [
    [/^Remove (.+)\?$/, "Remover $1?"],
    [/^(\d+) is the most\. Remove one to add another\.$/, "$1 é o máximo. Remova um para adicionar outro."],
    [/^(\d+) is the most; remove one first$/, "$1 é o máximo; remova um primeiro"],
    [/^a connector added by address already has this name: (.+)$/, "um conector adicionado por endereço já tem este nome: $1"],
  ],
};
export default SELF;
