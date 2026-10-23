# Measure, then keep or revert

No claim stands in for a check. An agent's word that something works doesn't count; a test or a score run where the agent can't touch it does.

- **Know where you start.** Run the tests and the score before you change anything, and note the numbers.
- **One number per goal** where you can: what is measured, which way is better, how much noise to tolerate.
- **After a merge, measure again.** If it's better, the change stays. If it's worse, it is reverted by a change that merges, with the reason written where the next worker reads it.
- **Two tries that don't move the number** mean the approach is wrong, not the effort. Step outside it before a third try, and say what's different.
- **Compare like with like**: the same command, the same data, at the precision the measure prints.

The Hive improves its own prompts and modules the same way: a change goes in, gets measured, and is kept or reverted.
