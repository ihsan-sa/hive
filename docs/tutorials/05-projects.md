# 5. Run a bigger project, or something on a schedule

## Plan first

Bigger jobs go better, and use less of your limits, if you agree the plan first:

> I want a small site that tracks prices of three products and emails me when one drops. Before you build anything, tell me your plan, what you need from me, roughly how much of my limits it'd use, and what could go wrong. Then wait for my answer.

Read the plan, push back and only then say go. Pick a **Focus** in the box when you start it, so the project's conversations sit on that focus's card in the grid on the Chat home and together under **By focus** in the list.

## Let it split the work

Say it can use helpers if the parts really are separate. Each helper uses your limits and counts towards **Agents at once**, so a job can stall behind its own helpers if that's set too low. If you'd like it done faster, raise that in Settings for the job.

## Keep notes where they'll last

For a project that will go on for weeks, ask for:

> Keep the goal and current state in `~/projects/price-tracker/goals.md`. Update it whenever something changes.

Later agents can pick up from that file. The same goes for what you want remembered across jobs:

> Add to your notes in `~/kb`: I always want spreadsheets in .xlsx with totals at the top.

## Start again without losing the work

Long conversations get expensive, because an agent re-reads what's recent. When one has grown long, start a new one and point it at the work:

> Carry on with the price tracker. The state is in `~/projects/price-tracker/goals.md`.

## Do something on a schedule

Agents can set up timers on the Hive's account. Ask for it directly, and be careful about what runs each time:

> Every morning at 8, check the prices and only tell me if one dropped. Use a plain script with a timer and no agent, unless something needs judgement. Show me how to turn it off.

A script run by a timer uses none of your limits. A timer that starts an agent uses some every time, so check the Usage page after a few days. The machine has to be on for a timer to run.

## Let it find work by itself

**Keep work moving** in Settings starts an agent every 3 hours to pick up stalled work and find work nobody asked for. It's always on: picking up work that stopped is what a Hive promises. You choose the pace; it uses your limits, so watch Usage for the first few days.

Next: [Add tools with extras](06-extras.md).
