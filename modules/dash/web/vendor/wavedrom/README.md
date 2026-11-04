WaveDrom 3.8.0, MIT (see LICENSE).

Copied unchanged from wavedrom@3.8.0's npm tarball, fetched with npm pack
--ignore-scripts. Tarball SHA-1: d79ffe4ff8521aaa7674d80ca6368ce5d2f63721.
The upstream wavedrom.unpkg.min.js bundle exposes renderAny and includes its
standard skin. The smaller wavedrom.min.js exposes only the DOM-oriented API.

Only renderAny and waveSkin are used. eva/processAll/editorRefresh (including
upstream eval-based parsing) are never called. The page CSP forbids eval.
