// What a fence's first word draws. Anything not named here is a code block, so a typo or a new language still reads fine.
// Adding a kind: one line here, its drawing in its own file, its library loaded only when one is near the screen.
import { codeBlock } from "./code.js";
import { mathFence } from "./math.js";
import { svgFigure } from "./figure.js";
import { mermaidFigure } from "./mermaid.js";
import { tableFigure } from "./table.js";
import { plotFigure, chartFigure } from "./plot.js";
import { waveFigure } from "./wave.js";
import { touchstoneFigure } from "./touchstone.js";
import { spiceFigure } from "./spice.js";
import { artifactFigure } from "./artifact.js";

const KINDS = {
  math: code => mathFence(code),
  svg: svgFigure,
  mermaid: mermaidFigure,
  csv: tableFigure,
  tsv: tableFigure,
  plot: plotFigure,
  chart: chartFigure,
  html: artifactFigure,
  wavedrom: waveFigure,
  spice: spiceFigure, cir: spiceFigure, sp: spiceFigure,
  touchstone: touchstoneFigure,
  s1p: touchstoneFigure, s2p: touchstoneFigure, s3p: touchstoneFigure, s4p: touchstoneFigure,
};

/** Parse an info string: first word is the kind, `title=x` or `title="a b"` names the block. */
function info(s) {
  const t = (s || "").trim(), lang = (t.match(/^[^\s=]+(?=\s|$)/) || [""])[0].toLowerCase();
  const m = t.match(/(?:^|\s)title=(?:"([^"]*)"|(\S+))/);
  return { lang, title: m ? (m[1] ?? m[2]) : null };
}

export function fenceBlock(infoStr, code) {
  const { lang, title } = info(infoStr), k = KINDS[lang];
  return k ? k(code, { lang, title }) : codeBlock(lang, title, code);
}
