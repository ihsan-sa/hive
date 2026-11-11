# What to ask it

Ten jobs worth handing over, each with a message you can paste. Replace the words in [brackets] with your own. Time and usage are rough estimates from our own use, not measurements on your machine; the **Usage** page shows what a job really used. Some of these need an extra (see [tutorial 6](06-extras.md)); they say so.

None of these has yet been run on a clean install by someone new. Each has been done by agents on our own Hive with messages like these.

**Start with 1 if you have never used it.** For the overnight ones, read [Keep work moving](05-projects.md) first.

## 1. A web page or small tool
**Paste:** "Make a one-page website for [what it is]. Put [the sections you want]. When it's done, attach it here."
**Happens:** an agent writes the page, opens it to check, attaches it. **Time:** a few minutes. **Usage:** small. **You get:** a file you open in a browser. Then say "make the header blue" and it changes.

## 2. A printable kit or document
**Paste:** "Make a [murder mystery for 8 guests, about one hour]. I need a host guide, character sheets and clues, as one PDF I can print."
**Happens:** it plans, writes, builds the pages, checks them. **Time:** 10 to 30 minutes. **Usage:** small to medium. **You get:** HTML and PDF.

## 3. A researched report with sources
**Paste:** "Find out [your question]. Use at least five sources, link each one, and say which claims you could not confirm. Give me a one-page summary first."
**Happens:** it searches, reads, and writes the report with links. **Time:** 10 to 30 minutes. **Usage:** medium. **You get:** a document. Open two of the links yourself; agents can be wrong.

## 4. A spreadsheet from messy files
**Paste:** "I've attached [receipts, statements or a CSV]. Total them by category and month, flag anything odd, and give me a spreadsheet."
**Happens:** it reads the files, builds the sheet, lists what it could not read. **Time:** a few minutes. **Usage:** small. **You get:** a .xlsx or .csv.

## 5. The overnight fix-and-test run
**Paste:** "Work in [path or repo]. Run the tests, fix the failures one at a time, and make a separate commit for each fix. Don't touch [what's off limits]. Stop after [N] fixes or when the tests pass. Write what you did and what is left in a file called MORNING.md."
**Happens:** an agent works through the failures; a second one can check the first. **Time:** hours; leave it overnight. **Usage:** large, so ask for a plan and its cost first. **You get:** commits and a note on what is done and what stopped.

## 6. A second opinion on a design or code
**Paste:** "Review [attached design or repo] as a skeptical expert. List the five biggest risks, ranked, each with the evidence. Don't change anything."
**Happens:** read-only review, so low risk. **Time:** 10 to 20 minutes. **Usage:** medium. **You get:** a ranked list.

## 7. A circuit board, brief to fab files
Needs the **pcb-flow** extra.
**Paste:** "Design a small board: [what it does, size, power, connectors]. Check it, and give me the files to order it."
**Happens:** schematic, layout, automatic checks, fab files. **Time:** an hour or more. **Usage:** large. **You get:** the board files and a check report. Complex high-speed routing is still best left to a person.

## 8. A chip block
Needs the **chip-flow** extra.
**Paste:** "Build [a small digital block] for [process] and run the checks. Report what passes and what fails."
**Time:** hours. **Usage:** large. **You get:** the design and a pass/fail report.

## 9. A short film of your own project
Needs the **video** extra.
**Paste:** "Make a 30-second film showing [what the project does]. Use the files in [folder]."
**Time:** 10 to 30 minutes. **Usage:** medium. **You get:** a video file.

## 10. Something every morning
**Paste:** "Every morning at 7, [check X and write me a short note]. Tell me how you'll do it first."
**Happens:** it sets up a scheduled job. **Usage:** none for plain scripts, small if an agent runs. **You get:** a note each morning. See [scheduled work](05-projects.md).

## When it goes wrong
- Ask "what did you do, and what is left?" The answer is in the conversation.
- If usage runs high, ask for a plan and its cost before big jobs, and check **Usage**.
- If it stops half way, say "carry on from where you stopped".

Back to the [guide](../guide.md#asking-well).
