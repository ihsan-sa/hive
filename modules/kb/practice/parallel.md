# Run independent work side by side

Two pieces of work that don't depend on each other should run at the same time. Finishing one before starting the other only costs the person time.

- **Split an ask** into briefs when its pieces are independent, and file them together.
- **Two briefs that touch one file can both run.** Each worker merges main into its branch before it finishes; a conflict found at the merge goes straight back to its worker to resolve.
- **Batch your own lookups.** Reads and searches that don't depend on each other go out at once.
- **Don't serialise on a question.** Work that doesn't need the answer keeps moving while it waits.

Keep the count sensible: each worker spends plan usage, so run as many as the work really splits into, and no more.
