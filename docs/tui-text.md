# The control center's text wraps and reads well

Design for `tui-text-wraps-and-reads-well` (issue #140). Spec §11, rules for the whole UI: "No line should run off the screen; detail opens, it doesn't pile up. Information is spaced and grouped so a screen can be read at a glance."

## What it does for the person

Whatever size the terminal is, the thing they are acting on is on screen whole:

- **A pick-list opened under the bottom row is never cut off.** On a short window, enter on the last visible setting (say "model for website" at 80×24) used to open its list below the bottom edge: the selected row stayed in view with two lines under it and the rest of the choices, `custom…` included, were clipped. The pane now scrolls so the row and its whole list are in view; when even that can't fit, the row wins and the list scrolls under it.
- **An ask's answers stay on screen.** In Waiting, the selected ask's options line is the last thing it draws; for the bottom ask with a long detail it fell off the same way. The pane now keeps the whole selected ask in view, from its header to its options.
- **Options wrap instead of running off.** An ask's options lay out in rows that fit the pane; the inline `←→ pick  enter answer` hint goes when it doesn't fit (the bottom row says the same).
- **A long setting wraps under its own column.** A value such as a pinned release's note wraps at the value column instead of ending in `…`. A label too long for the label column (a long project name in "model for …") sits on its own line with the value under the column. The label column is 28 cells, narrower on a narrow pane, so a 50-column window reads as two columns instead of clipping every value.
- **Long words break.** Text wrapping (chats, details, asks, module descriptions) used to leave a word longer than the line, a URL or a path, whole, so the pane cut it with `…`. It now breaks the word across lines (`ansi.Wrap`); nothing is lost.

## How it fits

- One helper, `span(lines, top, bot, h)` in `internal/tui/tui.go`, keeps a run of lines in view; `window` is `span` with two lines of what follows, as before, and on a pane under three rows it no longer scrolls the selected line away.
- No new keys, no new layout, no theme. Golden frames (`internal/tui/testdata`) are unchanged.

## What it leaves out

- One-line rows that are summaries (dashboard rows, the sidebar, scores' notes) still clip with `…`: per the spec, their detail opens on enter rather than piling up.
- Compose boxes and message bodies already wrapped; they are unchanged apart from long words breaking.

## Open questions for the person

- Should a value that wraps in Settings be shown whole, or clipped to one line with its full text on enter (as dashboard rows do)? Built: whole, since a setting has no detail view.
- At very narrow widths (under ~50 columns) the label column shrinks to 12–13 cells and most labels sit on their own line. Is a stacked label/value layout preferred there, or a minimum width under which the control center asks for a wider window?
- Are there other places the person has seen text clipped? The only case given was the pick-list; the Waiting options and long words were found while fixing it.
