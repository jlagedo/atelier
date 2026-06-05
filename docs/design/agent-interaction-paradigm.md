# Agent Interaction Paradigm — design exploration

**Status:** Proposal / live exploration (in progress)\
**Started:** 2026-06-04 (UX exploration session)\
**Reader:** product + UX + whoever builds the next-gen Atelier front-of-house\
**Resume here:** jump to [Next step](#next-step) — we stopped about to spec the
stage-② "relate" canvas op.

> This is an exploratory design session, not an implementation plan. It captures a
> rethink of *how a non-developer interacts with an agent* — deliberately rejecting
> three norms of today's agentic UX. Nothing here is built yet. Decisions are
> directional, not committed.

---

## 1. The thesis — three rejections

We are designing an agent for a **non-developer doing daily work**, and rejecting
three defaults of current agentic UX:

1. **Permission prompts are a design flaw.** The agent should be *safe by default*;
   don't hand per-action consent decisions to the user.
2. **Re-prompting every time is the worst interaction.** A good agent *minimizes
   prompting* — it shouldn't make the user re-describe recurring work.
3. **Sessions should be reproducible.** Every session should be able to crystallize
   into a saved, replayable **workflow / routine** the user can run anytime.

These aren't three features — they're one coherent rejection of the **"chat-and-
consent" paradigm** (blank prompt box = user supplies all initiative; permission
dialogs = user absorbs all risk). We invert all three axes:

| Today's default | Our direction | HCI lineage it revives |
| --- | --- | --- |
| User carries all initiative (prompt every time) | **Mixed-initiative** — agent & user trade who-acts-when | Horvitz, *Principles of Mixed-Initiative UIs*, CHI 1999 |
| Safety = user approves each action | **Safety = containment** (the cage is the consent) | sandbox / zero-trust isolation |
| Each session is disposable | **Each session crystallizes into a replayable routine** | Programming-by-Demonstration (PbD) |

**Why this is white space:** the workflow-automation crowd (n8n, Zapier AI, node
canvases) makes reproducibility easy but reintroduces developer-grade configuration.
The chat crowd (ChatGPT, Claude) kills configuration but throws away reproducibility
and leans on consent. Almost nobody has shipped all three *together* for
non-developers. Atelier's containment architecture already votes for goal #1.

---

## 2. The three goals in depth

### Goal 1 — permission prompts are a design smell

Per-action consent is **decision offloading**: the system dumps its own uncertainty
on the user as a yes/no tax → consent fatigue → users click *Allow* blindly. The
dialog provides the *feeling* of safety without the substance. The field's reframe:
**safety is a property of the environment, not of the conversation** — which is
literally Atelier's containment thesis.

**Trap:** removing the dialog removes the (bad) trust signal it provided. Replace it
with three things:

1. **Legible containment** — the user must *see* the cage (what the agent can touch),
   ambiently. Invisible containment reads as *no* containment.
2. **Reversibility as the real safety net** — Action Audit & Undo. Move the decision
   to *after* the result is visible, instead of before, when neither party knows yet.
3. **Escalation, not permission** — interrupt only when genuinely uncertain or about
   to do something irreversible / outside the cage. Rare interrupts *mean* something.

**Direction:** replace the permission *dialog* with a **boundary + ledger + undo**
triad. Agent acts freely inside a visible cage; everything lands in a reversible
ledger; it interrupts only at the cage's edge.

> **Refined by the persona (see §4):** kill *procedural* consent (read file, run
> step — cage covers it). **Preserve and elevate** *consequential* sign-off (the
> NAV ships) — in regulated work the gate is a feature the user is proud of, not a
> tax. This is the honest, earned version of a permission prompt.

### Goal 2 — prompting-every-time is the worst interaction

Decomposes into three escalating moves:

- **(a) Mixed-initiative (Horvitz 1999).** System models likely intent and *offers*
  the next step. Canonical example LookOut: watches you read an email, infers
  "scheduling," pre-stages the calendar action — confirm with a glance instead of
  composing a request. *Most important paper for goal 2; 27 years old; mostly ignored.*
- **(b) Anticipatory / ambient.** Agent runs in the background on events/context, not
  prompts. Key primitive: **ambient presence display** — agent state via a
  low-attention signal (color, pulse), not a screen you must read. Prompting → glancing.
- **(c) Demonstration over description.** Highest leverage for non-developers: let
  users **show, not tell.** Research (ALLOY, PUMICE) finds non-technical users express
  *procedural* preferences far better by demonstrating than by prompting — prompting
  forces them to verbalize tacit knowledge they don't have words for.

**Trap — the annoying paperclip (Clippy).** Failed not because anticipation is wrong
but because it interrupted with *low-confidence, high-cost* suggestions and couldn't
be taught. Guardrails: act silently when confident+reversible; *suggest* when medium;
*ask* only when high-cost (Autonomy Dial + Confidence Signal). Rejected suggestions
must change future behavior or you've built Clippy.

**Direction:** make the *default* surface not a prompt box but a **proposed next
action** the agent drafted from context — accept/tweak/dismiss. Free-text prompting
becomes the escape hatch (to *redirect*), not the front door (to *initiate*).

### Goal 3 — every session crystallizes into a replayable routine

This is **Programming by Demonstration**, newly tractable via 2025 LLM work:

- **ALLOY** (arXiv 2510.10049) — generates reusable agent workflows from user
  demonstration; workflow is a *visualized, transparent, editable* artifact.
- **AgentRR** (arXiv 2505.17716) — record one successful run, replay on new inputs;
  **replay acts as a guardrail** (more deterministic, less hallucination).
  *Reproducibility doubles as safety — connects Goal 3 back to Goal 1.*
- **PUMICE / Ringer** — classic end-user lineage: non-programmers automate by
  demonstrating + a little natural-language repair.

**Trap — the n8n trap (see §6).** The no-code/node-canvas industry already "solved"
reusable AI workflows, and in doing so *rebuilt the developer experience we're
fleeing.* The challenge isn't "make workflows reusable" (solved) — it's **make the
reusable artifact a byproduct of just doing the work, with zero authoring step.**

**Direction:** the user never *builds* a workflow. They do a task once; the agent
**silently crystallizes** it into a named, parameterized routine. The user's only
authoring act is, afterward, naming it and confirming which parts were *the specific
values this time* (the file, the date) vs. *the procedure*. That single distinction —
**constants vs. parameters** — is the entire UX of reusability, far smaller than a
node graph. Surface in *task language*, not flowchart language.

---

## 3. Synthesis — the unit of interaction is the *routine*, not the *message*

> Stop thinking of this as a chat that can save macros. Think of it as a **library of
> living routines** you occasionally talk to.

Flip the primary object from the **conversation** to the **routine**:

- Home surface = a **shelf of routines** ("Tidy my downloads," "Draft replies to
  today's invoices"), each a card: last run, what it touches, a Run button.
- A **new** task = an *unnamed routine in progress*. Demonstrate/converse once; it
  runs; it offers to keep itself.
- **Goal 1** = each routine declares its cage up front, visibly; replaying a known
  routine is inherently safer than free agency.
- **Goal 2** = invoking a known routine is one tap, or fires ambiently on its trigger.
  Prompt-cost amortizes to ~zero from the second run on.
- **Goal 3** = native data model, not a feature.

Dissolves a key tension: proactive agents are creepy on *novel* intent but reassuring
when running a routine you already blessed. **Anticipation scoped to "shall I run a
routine you already own?" is the safe, non-Clippy form of proactivity.**

**Two carrier primitives:**

1. **The Recipe Card** — any session rendered as plain-language steps with highlighted
   variables. Save format + trust/legibility surface + edit surface. One artifact,
   three jobs.
2. **The Cage Chip** — always-visible, glanceable badge on every routine/session:
   *what it can touch.* Replaces the permission dialog with persistent ambient legibility.

**Tensions to design against:** demonstration cold-start (run #1 is where trust is
lowest and the routine is the reward — don't optimize it away); parameterization is
where PbD historically breaks (plan a repair conversation, not perfect first-shot
inference); legible containment can become noise (must be ambient, not nagging); silent
action erases the audit trail people trust by — minimizing prompting and the reversible
ledger are inversely coupled.

---

## 4. Persona — Jonny, 34, Fund Accounting Operations

Chosen because fund ops is the *ideal* proving ground: the job is already made of the
materials we're designing for.

**Role.** Global custodian / asset-servicing arm of a big bank. Team strikes **NAVs**
(daily official fund prices) before each fund's cutoff. Owns ~15 funds. He's a
**maker**; a senior colleague is his **checker**.

**Day (matters for the design).**

- 07:30 arrives before funds strike. Morning is a *time-boxed sprint* vs. deadlines.
- Pulls overnight reports from the fund platform (Multifonds/Geneva/InvestOne) into Excel.
- Runs **morning checks**: cash & position recs (book vs custodian), stale/breaching
  price checks, missing trades, FX, accruals.
- Most days most funds **tie out** — clean, sign off, move on.
- The job is the **exceptions**: a rec breaks by $4,200; a bond breaches tolerance; a
  corporate action wasn't booked → **investigate the break**, **write the commentary**,
  fix/escalate or sign off.
- Tools: fund platform, **Excel everywhere**, recon tool (TLM/SmartStream),
  Bloomberg/Refinitiv, Outlook. Maybe an inherited VBA macro he's scared to touch.

**Mindset = design constraints.**

- Not a developer (Excel power-user at best).
- **Terrified of being wrong** (bad NAV to a client = real incident). Risk-averse by formation.
- **Lives inside controls** — four-eyes (maker-checker), change control, audit trail.
- **Data cannot leave the building** — confidential, regulated. Hard wall → Atelier's
  cage is the *only* thing that makes an agent legally usable here.
- **Bored by the ritual, paid for the judgment** — 80% mechanical pull-compare-check;
  value is the 20% of investigating/explaining breaks.

**The four trigger-moments (he opens the agent to…):**

1. **"Run my morning."** The daily ritual battery. Walk in to *"3 of 15 funds need you;
   12 are clean."* Should never re-describe it. → Goal 2 + Goal 3, purest form.
2. **"Help me chase this break."** Agent *assembles the evidence* (book vs custodian,
   the offending trade/price, the trail); *Jonny keeps the call.* → mixed-initiative.
3. **"Write this up for me."** Standard break-commentary email/log, house style, numbers
   slotted in. → micro-routine.
4. **"Is this right?"** Pre-checker sanity pass before handoff. → note: he wants *more*
   scrutiny, not less.

**The productive tension (refines Goal 1):** Jonny's world runs on a permission ritual
— **four-eyes maker-checker** — which he experiences as *professional cover*, not
friction. So "no permissions" splits: kill the low-value *procedural* consent (cage
covers it); **keep and elevate** the high-value *consequential* sign-off. That's a
better principle than "no permissions," found only by giving the abstraction a real job.

**Caveat:** Jonny is built from asset-servicing domain knowledge. If the real target is
the asset-manager side, hedge-fund admin, or middle office, the rituals shift — retune.

---

## 5. The four-stage abstraction (Jonny's own framing)

Jonny abstracted his whole job (and most of fund ops, recs, audits, compliance, data QA):

```text
[1] INPUT  →  [2] MATCH / VALIDATE  →  [3] OUTPUT  →  [4] DECIDE
  (1..n files)   (the old macro)        (the result)   (human judgment)
```

**Key insight: this four-stage shape IS the Recipe Card.** Each stage = one legible
section of the saved routine. The abstraction and the save-format are the same object.

### Onboarding — how does the routine get *born*? (the cold-start keystone)

Drop-a-file flow, agent-driven:

- One **drop zone**, not two buttons — don't make Jonny pre-classify input/process/output.
  Every decision you hand him is one you failed to earn.
- Agent **guesses in his language** ("looks like a custodian position file"), not the
  file's ("CSV, 8 columns"). Domain-aware guessing = magic vs. dumb. *Atelier's cage is
  why the agent can read the real confidential data to guess well.*
- The **naming click** does triple duty: confirmation + routine name + the vocabulary the
  agent uses forever. Christening, not form-filling.
- **The magic moment:** on file 2, the agent notices a shared key and makes the *intent
  leap* — *"these line up on trade ID — looks like you're reconciling book vs custodian.
  Want me to try?"* It offers to **run**, not just label.

### The noun/verb gap (the thing onboarding must not skip)

Dropping files teaches the **nouns** (what the data *is*). The value + difficulty is
**stage 2, the verbs** — the macro's *logic* (match on trade ID; flag diffs > $50;
ignore suspense; a coupon timing diff isn't a real break). That logic lives in Jonny's
head, muscle memory, and an inherited macro — **never in the file.** If the flow reaches
stage 2 and asks Jonny to *describe his rules*, it has turned him back into a programmer.

**The move: teach the process by example, not description.**

- Jonny has last month's **inputs AND output**. The input→output pair *contains the
  macro.* Let him **drop the answer too** → the agent reverse-engineers the transform
  (zero authoring; the old output already exists in a folder).
- Then: agent makes a **confident first attempt, runs it, shows a concrete result**, and
  Jonny **corrects the wrong rows.** *"847 matched, 3 breaks — right?"* → *"row 2 isn't a
  break, that's coupon timing — ignore those"* → *"Got it. Ignore coupon-timing diffs.
  Apply every time?"*
- **Jonny reacts to a wrong number; he never writes a rule.** Reaction is universal;
  specification is a programmer skill. The macro *accretes from repair*, confirmed against
  real data. (PUMICE/ALLOY pattern.)

**Where the repair loop strains:** first guess *badly* wrong (400 "breaks" → can't tell
broken agent from broken data → need confidence signal + "show me one match you'd accept"
fallback); conditional/messy rules ($50 for bonds, $5 for cash, except month-end → agent
must *propose the generalization*, not wait to be told); stage 4 leaking into stage 2
(sometimes "is this a break" *is* the judgment → agent must know what it's *not* allowed
to learn).

---

## 6. The canvas model — agent draws, human corrects

The shift: we stopped designing a **chat** (a scroll work flows past) and started
designing a **space** (a place work persists). Jonny's pipeline *is* a graph, so on a
canvas **the thing he builds and the thing he replays are the same object** — the canvas
*is* the routine; reproducibility becomes the medium, not a save step.

### The reframe that avoids n8n

> The agent **draws** the canvas. Jonny **corrects** it. The canvas is a *living mirror
> of the agent's understanding*, not a tool Jonny operates.

Jonny does three cheap things — **drop**, **click-to-say-something**, **answer a
question**. The agent does all *structural* work — creates objects, lays them out, draws
edges, asks questions. **Jonny never connects two things himself; he corrects a
connection the agent proposed.**

### The grammar (maps onto the four stages)

| Stage | On the canvas | Who makes it |
| --- | --- | --- |
| ① Input | an **object** (dropped file + agent's guess) | Jonny drops; agent labels |
| ② Match/validate (the macro) | an **edge** between objects — *the verb lives here* | **agent proposes**, Jonny confirms/corrects |
| ③ Output | a **new object** the edge spawns | agent creates |
| ④ Decide | **annotations / sign-off on the output object** | Jonny, always |

Nouns = objects, verbs = edges, result = new object, decision = a human gesture *on* the
object (never an edge). Tiny, learnable grammar.

### "Information? command?" — don't make him choose; use space

A text object's meaning comes from *what it's attached to*: on a file → metadata; on an
edge → a rule; floating alone → a goal/intent. **Position is the disambiguation** — the
canvas earning its keep (a chat has no "near"). When wrong, the agent asks ("a note, or
should I always do that?") — same repair loop, not a config dialog.
*Validated independently by tldraw's Steve Ruiz: "source on the left, result on the
right… that meaning exists to us" — non-technical users read spatial layout natively.*

### What the n8n trap *is*

n8n = open-source Zapier/Make: drag **nodes**, wire **ports**, data flows left→right.
Powerful, but demands the user decide node types, wire connections, understand data
shapes, think in flowcharts = *programming with rounded corners.* Jonny bounces in 30s.
**The cliff:** the instant the canvas asks Jonny to *wire* anything, you've become n8n —
reproducibility kept, non-developer lost. Our edge: keep the *graph*, move the *pen* from
human to agent.

### Risks to hold the line on

Blank-canvas paralysis (drop-file entry + a gentle default so it's never a cold void);
invisible affordances ("click to add text" is undiscoverable → agent *demonstrates* with
a ghost text object); spatial overwhelm (agent **auto-lays-out**; Jonny *can* nudge, never
*must* arrange); the n8n cliff (every structure piece agent-drawn + human-corrected,
never human-authored).

---

## 7. Architecture — "drive the canvas from within the agent"

The pivotal decision. The canvas becomes **a shared surface where the agent is a
first-class actor** (place/draw/ask/spawn) — its **hands and eyes**: it *reads* canvas
state to know what's happening, and *writes* canvas state to act.

**The mechanism is already proven** — tldraw's agent starter kit: canvas → agent
(structured shape data + screenshot); agent → canvas (structured mutation ops). So
"can the agent drive the canvas" is solved as plumbing. The hard part is *judgment*
(right ops, good guesses, graceful repair) = stage ②, exactly where we thought.

### The critical carve-out: meaning vs. mechanics

Routing **everything** through the agent makes a *drag* wait on an LLM (2–3s) = dead. Split:

| Kind | Examples | Through the agent? | Feel |
| --- | --- | --- | --- |
| **Mechanical** | drag, move, zoom, pan, select, collapse | **No** — canvas runtime, local | instant |
| **Semantic** | drop a file, type intent, answer reconcile?/join?, correct a row | **Yes** — a request to the agent | "thinking…" then resolves |

Heuristic: **round-trip the agent only for things the user expects to take a moment of
thought.** Drop a file → expects thinking → fine to wait. Drag → expects instant → must
never wait. Match latency to expectation and the wait disappears psychologically.

> **Refined one-liner:** the agent owns the canvas's **meaning**; the canvas runtime owns
> its **mechanics**; Jonny's **semantic** gestures are requests to the agent, his
> **mechanical** gestures are free and local.

### The payoff of "semantic = agent request"

- **Goal 3 is free.** Every semantic change *is* an agent op → the canvas is a replayable
  **op-log**: `[place file] → [relate on trade-ID] → [commit: reconcile] → [spawn breaks]`.
  Replay = re-run the log with tomorrow's files. The act of doing it *is* the recording.
- **Intent is legible by construction.** Semantic gestures *arrive as requests* → the
  agent always knows what Jonny did and why; no separate "watch the human" channel. The
  request stream *is* the shared history.

### Canvas-as-truth (commit to this)

The canvas document *is* the shared truth — simultaneously the work, the saved routine,
the agent's working memory, and the human's control surface. One object, four jobs (same
move as the Recipe Card). Correcting the canvas corrects the agent's *actual* memory —
no view that drifts out of sync.

### Hiding agent latency — optimistic ghosts

```text
Jonny drops a file
  ⤷ canvas INSTANTLY shows a "📄 reading…" ghost      (local, 0ms)
  ⤷ request → agent over the wire
  ⤷ ~2s later the agent op arrives: label + guess + alternatives
  ⤷ ghost resolves into "looks like a custodian position file. Right?"
```

Object appears on finger-lift; *meaning* arrives a beat later. He watches it think —
which *builds* trust (per the research) rather than draining it. Also needs proposing
states for edges (ghost/"drawing…"), propose-before-commit (cognograph's
"Plan-Preview-Apply" ghost-node preview), ambient presence ("looking at your files").

### Constrained action vocabulary (enforces no-n8n in code)

```text
place(file|output)      label(object, guess, alternatives[])
relate(A, B, question)  commit(edge → operation)
spawn(output ← edge)    ask(question, options[])   flag(rows)
```

The agent *literally cannot* create arbitrary node types or wire raw ports — only place
objects, propose relationships *as questions*, commit answered ones. The no-n8n
discipline becomes **enforced by the tool surface**, not a guideline.

### Maps onto Atelier's real stack

- **Mechanical** edits → stay in the renderer (tldraw-style canvas), never cross the boundary.
- **Semantic** gestures → events over the existing **NDJSON wire** (`PartisanClient` →
  Session Manager → into the cage). See `apps/desktop/src/main/sessions/client.ts`,
  `transport.ts`.
- **partisan** (`packages/partisan/cli_guest.py`) reads the dropped file *inside the cage*,
  decides, emits **canvas-ops** back over the same wire → renderer applies them.
- Files + compute stay in the cage; the canvas is the legible window + control surface.
  The agent's tool surface gains the canvas vocabulary above.

**The convergences (the tell we're on a real seam):** the UX division of labor *also*
solves concurrency (two writers, different objects); the constrained vocabulary *also*
enforces no-n8n; the cage *also* powers good guesses (read real confidential data that
cloud players can't); canvas-as-truth *also* makes work = routine. Four concerns collapse
into one architecture.

---

## 8. Competitive landscape (2026 sweep)

**Closest neighbor: tldraw computer** — components on an infinite canvas linked by
data-carrying arrows, each with procedures, output→input flow, branch/loop/iterate. The
most sophisticated AI canvas shipping. **But the human still wires the graph** — founder
Steve Ruiz: *"human direction with AI assistance, not… AI [that] autonomously wires
connections."* It's n8n with better taste + AI in the boxes. **Our differentiator —
agent draws, human corrects — is the exact step they chose not to take.**

**Three clusters, each with a gap:**

1. **AI-canvas / spatial tools** (tldraw computer, Flowith, Lovart ChatCanvas, OpenAI
   Agent Builder, Figma agent canvas): right surface, **human is the wirer**, aimed at
   *creative/builder* work, not non-dev ops. None infers "you're reconciling these — shall I?"
2. **Finance reconciliation agents** (moveo, Hypatos, wizr, BlackLine-style STP): right
   vertical, but **top-down engineer-configured black-box STP** — Jonny consumes output,
   never teaches it, never sees a canvas. Automation *done to* his team, not wielded *by* him.
3. **PbD research** (ALLOY, AgentRR, Toby Li co-adaptive PbD): right *thesis* (agent learns
   by watching; workflow is a byproduct), but **academic prototypes** — web/mobile-agent,
   not spatial, not finance, not shipped.

**The white space:**

```text
                 HUMAN wires the graph         AGENT draws the graph
               ┌──────────────────────────┬──────────────────────────┐
 BUILDER /     │ tldraw computer, Flowith,  │                          │
 creative      │ Lovart, n8n, Figma, OpenAI │      (mostly empty)       │
               ├──────────────────────────┼──────────────────────────┤
 NON-DEV OPS   │ nobody (ops bounce off     │   ★ ATELIER / JONNY ★     │
 (recon, QA,   │ node graphs); enterprise   │   agent-drawn canvas,     │
 audit)        │ STP is black-box, not this │   teach-by-dropping,      │
               │                            │   routine = the work      │
               └──────────────────────────┴──────────────────────────┘
```

**Why it's empty — the honest version:** it's the *hard* cell. tldraw looked at "let the
AI wire it" and **deliberately backed away** — auto-inferring structure from messy intent
is hard, and wrong guesses confuse in a way self-wiring never does. So the moat is a
**capability bet**: can the agent guess well enough, often enough, that "correct my guess"
beats "wire it yourself"? If yes → a category nobody has. If mediocre → worst of both
worlds (can't control *and* can't trust).

**Why Atelier can take the bet others can't:**

- **Containment** lets the agent read the *real confidential data* to guess well — cloud
  canvas + STP players can't (data can't reach their servers). Architecture *is* the edge.
- **Narrow domain** (fund-ops recon) → small, patterned relationship-space ("reconcile on
  a key" ≈ 80% of the job). Guessing within a tight grammar, not arbitrary intent.
- **The repair loop** (correct-the-rows) is the safety net pure-canvas lacks and black-box
  STP hides.

---

## 9. Open questions / risks (for tomorrow+)

- **Guess quality + graceful repair (stage ②)** — the make-or-break. Everything rides on
  "agent proposes, you correct" feeling better than "you build it."
- **Parameterization** — constants-vs-parameters inference; the historical PbD failure point.
- **Confidence + fallback UX** — how the agent signals low confidence and offers "show me one
  match you'd accept" instead of a dead end.
- **Generalization proposals** — agent proposing "apply $50 to all bonds?" without sounding
  like a config wizard.
- **Where stage 4 must stay human** — agent must know what it's *not* allowed to learn.
- **Maker-checker handoff** — design the elevated, consequential sign-off as the model for
  *all* escalation.
- **Persona validation** — confirm Jonny's segment (fund accounting vs. adjacent).

---

## 10. Sources

UX patterns: [Smashing — Designing Agentic AI UX patterns](https://www.smashingmagazine.com/2026/02/designing-agentic-ai-practical-ux-patterns/)
(Intent Preview, Autonomy Dial, Explainable Rationale, Confidence Signal, Action Audit &
Undo, Escalation Pathway) ·
[bprigent — 7 UX patterns for human oversight in ambient AI](https://www.bprigent.com/article/7-ux-patterns-for-human-oversight-in-ambient-ai-agents) ·
[AI UX Playground — Ambient Presence Displays](https://www.aiuxplayground.com/pattern/ambient-presence-displays) ·
[UX Mag — Secrets of Agentic UX](https://uxmag.com/articles/secrets-of-agentic-ux-emerging-design-patterns-for-human-interaction-with-ai-agents)

Mixed-initiative / anticipatory: [Horvitz, Principles of Mixed-Initiative UIs (CHI 1999, PDF)](http://erichorvitz.com/chi99horvitz.pdf)

PbD / record-replay: [ALLOY (arXiv 2510.10049)](https://arxiv.org/pdf/2510.10049) ·
[AgentRR (arXiv 2505.17716)](https://arxiv.org/abs/2505.17716) ·
[Toby Li — co-adaptive PbD](https://toby.li/files/Li_SupportingCoAaptiveHumanAgentRelationship.pdf) ·
[PUMICE (arXiv 1909.00031)](https://arxiv.org/pdf/1909.00031)

Safety-as-environment: [Dust — agents only as safe as where they run](https://dust.tt/blog/ai-agents-safe-as-where-they-run) ·
[MindStudio — safety is a system problem](https://www.mindstudio.ai/blog/ai-agent-safety-system-vs-model-problem)

AI canvas: [tldraw computer](https://computer.tldraw.com/) ·
[tldraw agent starter kit](https://tldraw.dev/starter-kits/agent) ·
[Latent Space — Steve Ruiz interview](https://www.latent.space/p/tldraw) ·
[cognograph (Plan-Preview-Apply)](https://github.com/skovalik/cognograph) ·
[Flowith](https://max-productive.ai/ai-tools/flowith/)

Finance recon (incumbents): [moveo.ai](https://moveo.ai/blog/financial-reconciliation-ai-agents) ·
[Hypatos](https://hypatos.ai/guides/agentic-back-office-automation) ·
[wizr](https://wizr.ai/blog/agentic-ai-for-finance-and-accounting/)

n8n (the trap): [n8n.io](https://n8n.io/) · [workflow gallery](https://n8n.io/workflows/)

---

## Next step

**Spec the stage-② `relate` canvas op** — the agent's most consequential canvas action
and the smallest thing that proves the whole architecture. Design:

1. the **request** that triggers it (file 2 dropped, shared key detected),
2. the **proposing / ghost state** before commit,
3. the **reconcile? / join?** question UI (how the agent poses it),
4. the **graceful-degradation path** when the guess is wrong ("show me one row you'd call
   a match"),
5. how corrections **attach to the edge and accrete into the saved rule.**

Goal: make "I'll guess, you fix it" feel *safer and faster* than tldraw's "you wire it."
