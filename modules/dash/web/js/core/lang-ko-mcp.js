// ko: Settings > Connectors > Add a connector by address (views/settings.js mcpUrlBlock, E120a) and the server's reasons for a refusal
// (dash.py mcp_url_check / mcp_url_do), shown as they come. Exact English keys; patterns for the lines with a name or a count.
const SELF = {
  "Add a connector by address": "주소로 커넥터 추가",
  "A remote MCP server, like a custom connector in Claude. Your agents can use its tools from their next run, so only add a server you trust.": "원격 MCP 서버로, Claude의 사용자 지정 커넥터와 같습니다. 에이전트는 다음 실행부터 그 도구를 쓸 수 있으니 믿을 수 있는 서버만 추가하세요.",
  "Added. Agents can use it from their next run.": "추가했습니다. 에이전트는 다음 실행부터 쓸 수 있습니다.",
  "Address": "주소",
  "Agents stop seeing its tools from their next run.": "에이전트는 다음 실행부터 그 도구를 보지 않습니다.",
  "has a token": "토큰 있음",
  "Token (if the server needs one)": "토큰 (서버가 요구할 때)",
  "paste the server's address": "서버 주소를 붙여 넣으세요",
  "that address has spaces in it or is too long": "그 주소에 공백이 있거나 너무 깁니다",
  "that isn't a web address": "웹 주소가 아닙니다",
  "leave the sign-in out of the address; paste a token in its own box": "주소에 로그인 정보를 넣지 마세요. 토큰은 따로 있는 칸에 붙여 넣으세요",
  "the address must start with https:// (plain http only for a server on this computer)": "주소는 https://로 시작해야 합니다 (암호화 없는 http는 이 컴퓨터의 서버만)",
  "no connector added by address has that name": "그 이름으로 주소로 추가한 커넥터가 없습니다",
  "give it a short name: letters, digits, - and _ (up to 40)": "짧은 이름을 붙이세요: 영문자, 숫자, -, _ (최대 40자)",
  "a connector with that name is already here": "그 이름의 커넥터가 이미 있습니다",
  "that token can't be used": "그 토큰은 쓸 수 없습니다",
  __patterns: [
    [/^Remove (.+)\?$/, "$1을(를) 제거할까요?"],
    [/^(\d+) is the most\. Remove one to add another\.$/, "최대 $1개입니다. 하나를 제거해야 더 추가할 수 있습니다."],
    [/^(\d+) is the most; remove one first$/, "최대 $1개입니다. 먼저 하나를 제거하세요"],
    [/^a connector added by address already has this name: (.+)$/, "주소로 추가한 커넥터가 이미 이 이름을 씁니다: $1"],
  ],
};
export default SELF;
