# A public page for a lab's circuit boards

**First message** (pasted into Chat):

> Can we get a page with just a list of all our boards, with links to download the Gerbers, the CPL, the BOM and whatever other files there are, a 3D render image, and a public link to each board's documentation? It can be public, and better hosted on Cloudflare Pages than on my own machine. I want it simple and easy, not vibe-coded looking.

**What the agents did**

- One agent read the board repos and built a list: 58 boards, each with a name, revision and files.
- It checked every board's silkscreen text and embedded images for phone numbers, emails and names before anything went public. Two boards had personal details; they were taken off the page until the owner said what to do.
- It generated a 3D render per board, tested each documentation link signed out, and put it all in one plain static page.
- It deployed with a small script, then loaded the live page from outside the machine and spot-checked downloads.
- Later messages from the owner ("date-stamp the file names so I know they're up to date") were done in the same conversation.

**Result:** a live public page, one row per board, with Gerbers, BOM, CPL, schematic, render and docs link. Live at https://boards.ihsan.cc

**Worth copying:** ask for a check before publishing ("look for names and phone numbers") and say what "simple" means for you.
