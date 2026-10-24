# The Hive

*On long-horizon agentic architecture: towards dynamic RSI*

Source: <https://ihsan.cc/writing/the-hive>, 21 October 2026 (2026-10-21). The person's essay, and the architecture spec the Hive is built against. Its text is copied here unchanged from the page; only the relative link and figure paths were made absolute. Where the Hive departs from it today is in [spec.md](spec.md#17-departures-from-the-essay), as known gaps. Change the essay only from the page.

---

*This essay is also a [7-page PDF](https://library.ihsan.cc/p/UMJh95rrN6cPhyvzoZsvZsFHBOb_hXog).*

*Read about [how I used Autobox](https://ihsan.cc/writing/autobox), my previous orchestration system.*

## Design philosophy

### Core

The key design principle of *The Hive* is simplicity and flexibility. At its core, the hive is a skeleton which enables agents to work simultaneously on many projects with interconnected dependencies, whether they be files or shared *modules*. This is accomplished via separate git worktrees and unix users for agents and projects. In addition to enabling concurrent agent work, the hive skeleton facilitates inter-agent communication and discovery. The core also makes it possible for agents to spawn and be spawned by other agents. The core enforces permissions and hard safety ceilings as well. Hence, in its simplest form, *The Hive* is a barebones system with an arbitrary number of agents which can talk to one another and collaborate on the same projects under one hard safety boundary.

In short:

1. Agents work in separate worktrees and unix users.
2. An effects process (*fx*) holds all credentials and acts on agents' behalf.
3. Agents can spawn and be spawned by other agents.
4. Inter-agent messaging and discovery enables collaboration across projects.
5. Safety is ensured by a hard permissions ceiling.

> Maintaining a lightweight and simple core is critical to ensuring tight self-improvement loops and continued evolution as models progress.

![The hive as a matrix: each project a lead over its workers, building the modules every agent shares, with wardens and tribunals on call to all of them.](https://ihsan.cc/writing/the-hive/hive.svg)

*The hive as a matrix: each project a lead over its workers, building the modules every agent shares, with wardens and tribunals on call to all of them.*

### Modules

The *core* gains functionality through *modules*: shared tools, services, scripts, or knowledge bases which agents can engage with at will to complete work. Examples of modules include but are not limited to:

- **Slack** module which enables agents to communicate in the Slack workspace.
- **Phone** module which makes it possible for agents to communicate with the user via phone call.
- **Knowledge base** module which agents write to and read from. Search and filing engines manage and consolidate the knowledge generated, to enable the system's ephemeral hive mind and inter-/intra-project context.
- **Self-improvement** module which runs scheduled sweeps over the entire hive, logs, Slack comms, etc., to identify failures, deficiencies, and potential upgrades.
- **System management** modules manage server resources, dispatch jobs to the GPU, etc.
- **Library** module files and updates documents in the web library.
- **Agent/harness** module contains the agents deployed to *The Hive*, and the adapters which enable different models and harnesses to be used.
- **Scripts/helpers/tools** which agents write for themselves, for use by the collective, or for the user.

Agents are encouraged to develop and file such modules, as well as identify weaknesses in released modules and improve them. Large modules are owned by *projects* which live on *The Hive*, while small modules may not have an owner or were made by a since-deactivated agent. When an agent identifies a weakness or upgrade for a module, it either updates it itself for small edits/fixes, or raises it to the module's project for major changes. Fixes to the hive's *core* go to the hive admin project.

### Projects

Work gets done in projects. All projects can see one another by default, with sandboxing and isolation possible. Isolated projects/workspaces may have limited agent permissions, access to modules, and/or communication with, or awareness of, other agents.

Projects are tracked via git and stored in a remote repository; agents work in them on separate worktrees, opening PRs as work gets done. Internal projects which serve as a means to an end are released as modules, while projects which serve an end themselves produce deliverables for the user/public. In working on a project, agents are encouraged to develop internal tools themselves, or request them from related projects. For example, a project developing an agentic chip design flow may be looking to train an ML model, in which case it would request a GPU workload distributor from the project managing the system management module.

While agents work on separate projects, they have visibility into all projects and their agents by default. This enables increased collaboration and autonomous upgrading of shared systems, debugging of common problems, or development of internal tools agents can benefit from.

### Agents

Agents are very bare-bones by design. They consist of a harness, such as Claude Code, and are spawned with a `GOALS.md` document. This document contains primarily information on how the hive's core functions, and where to find the modules, communicate with agents, etc. It is kept incredibly minimal and generic to ensure it easily evolves as the system and models do.

Key principles include:

- Research-first approaches, where agents obtain concrete information with sources on the task(s) they are planning on completing. This can be accomplished via knowledge base queries and research.
- Proactivity, where agents take charge and develop tools by default.
- Communication, which is encouraged to enable collective problem solving.
- A dynamic approach to weighing safety concerns and permissions, where these are evaluated based on high-level goals and scope of impact.

Hence, nearly all agents are the same, differing only in the specific instructions regarding the project or task they are spawned into, and in their permissions, depending on the isolation of the workspace they are in. This modularity makes it much easier for the system to improve itself and adapt without obscure dependencies breaking. Additionally, by relying on agent judgement and allowing agents to choose from a range of tools and approaches informed by previous work on the hive, obsolete approaches and frameworks automatically get filtered out as new ones surface. By its very nature, this maintains a dynamic and self-improving hive.

Key agent types include:

- **Lead** agents, which own a project's goals and asks.
- **Worker** agents, which represent the bulk of the agents in the hive and perform generic tasks, from writing code to reviewing it or completing research. Workers, like any other agents, may access specialized modules which offer additional procedures and tools to better complete a task, similar to how skills are implemented in Claude Code. For example, a worker tasked with researching a certain topic may access a module or entries in the knowledge base which provide guidance on how to execute such tasks. These are chosen *at will* by the agent, ensuring obsolete procedures and frameworks are not forced upon agents.
- **Warden** agents, which hold elevated privileges over workers and are spawned by workers to execute tasks which reach outside the hive. Wardens follow a thorough safety evaluation framework which acts similarly to a Claude Code auto-mode classifier, except scoped to the hive.
- A **Tribunal** of agents, which is spawned by a warden when it cannot determine the safety of an operation. The warden generates a script to run the required command and submits it to the tribunal. The tribunal, a panel of different models and agents which each run their own independent investigation, holds the final say. Agents on the panel discuss, and then each casts a vote. If a majority is achieved, the script is run. Tribunals do not complete any work.

All these different agents are stored in a module, which enables their continuous improvement and the deployment of new releases, as with any other process on the hive.

### Safety

Risks are classified into separate classes:

- **T1:** Risks scoped to the hive (e.g. a module edit).
- **T2:** Risks external to the hive but scoped to non-mission-critical systems (e.g. updating a line on my website), or risks internal to the hive but part of mission-critical systems (e.g. editing the hive core).
- **T3:** Risks external to the hive and part of mission-critical systems (e.g. changes to the process which issues live links to documents in my library).

*T1:* The core is built such that state is preserved, and risks scoped to the hive can be recovered from or rebuilt with scoped work. This falls primarily under the responsibility of worker agents, who are instructed to weigh risks and permissions against high-level goals. Risks at this level are often acceptable and enable accelerated development and higher-quality work. An example would be editing the Slack module: if it breaks, it would not be ideal, but it is recoverable and fixable. Reviews on PRs and adaptive test suites, which run based on the changed files/systems, are designed to catch most errors.

*T2:* These risks need to be weighed more carefully and are typically evaluated by wardens.

*T3:* At this level, tribunals are typically involved. If the tribunal rejects a change and it is deemed to provide a significant improvement over other allowed routes, the user is briefed on the situation and then decides.

![Who weighs an action, by risk tier. Only one path reaches the user.](https://ihsan.cc/writing/the-hive/risk-path.svg)

*Who weighs an action, by risk tier. Only one path reaches the user.*

### Landing PRs, credentials, and workflow

Changes are checked by a combination of agent reviews and test suites, run adaptively based on which files and projects were touched. Small fixes and changes, such as a tiny bug fix or an update to non-critical docs, are merged by the worker that made them. More major changes get reviewed by another agent, run adaptive test suites, may be sent back to the worker for fixes, and then finally get merged by the *fx* process described below.

An *effects process (fx)* holds all credentials, from remote repository to model API keys and logins, and manages merging of PRs. Agents never hold credentials themselves. A worker messages fx with the action it needs, such as a push, a merge or a Slack post. fx checks which agent is asking and whether it is allowed, and then performs the action on the worker's behalf.

If an AI usage limit is hit or systems fail, the hive recovers gracefully. When a usage limit is hit, work waits and resumes once the limit resets. State is preserved at checkpoints, and if those cannot be recovered, work is relaunched from logs, etc.

### Recursive Self-Improvement (RSI)

RSI appears in various forms in the hive. The first is through the structured self-improvement sweeps and flows. These run automatically and identify failures, successes, holes, and potential improvements across all systems running on the hive. Through communication with relevant projects and agents, these get filed as improvements across the hive's projects, get reviewed by the respective project's agents, and then get implemented.

The second is part of the design philosophy. Agents are encouraged to develop tools, modules, etc., to support the collective, while being critical of their own work and biases to file improvement tickets themselves. This mechanism incrementally improves both the projects the agents work on, and the modules which support the hive, as well as the core of the hive itself.

Lastly, the system is structured in such a way that it stays simple and lends itself to improvement as models improve. The soft judgement layer the hive operates on gets better as the models improve, while the deterministic scripts and tools which are implemented as modules get continually upgraded, replaced, and archived as they age. This ensures the system remains dynamic and no rigid aspect ever ties it down.

This RSI flow and soft-judgement paired with at-will use of deterministic tools can be, and are, applied to projects which run on the hive. The most obvious examples are the chip and PCB design flows, which both improve themselves, but also rely on complex deterministic scripts called at-will by the agent to verify and evaluate the system.

### User input and steering

I communicate with the hive via Slack, email, and phone calls. I can reach any agent in the hive via these channels. That being said, most of my interaction is with the hive lead session or the project leads, whose role it is to parse through all my requests and turn them into structured goals and worker dispatches. Most of my work on the agentic architecture front comes down to architectural decisions and design of the safeguards.
