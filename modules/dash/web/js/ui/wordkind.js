/** The three lines root's card takes (its confirm.js KINDS): one action, a goal (a standing word, projects/word-shell/GOALS.md), or a goal's withdrawal.
 *  Its own file, not rootword.js: the release swaps rootword.js for a stub, and the composer's Send label needs these either way. */
const KINDS = [["standing", "yes, standing:"], ["withdraw", "withdraw standing:"], ["yes", "yes:"]];
/** [kind, prefix] of an approval line, or [null, ""]. */
export const kindOf = t => KINDS.find(([, x]) => String(t || "").trim().slice(0, x.length).toLowerCase() === x) || [null, ""];
/** What Send says for these words: Approve, Approve goal, or Withdraw goal. */
export const LABEL = { yes: "Approve", standing: "Approve goal", withdraw: "Withdraw goal" };
export const approveLabel = text => LABEL[kindOf(text)[0]] || "Approve";
