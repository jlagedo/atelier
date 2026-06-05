# Agent Interaction Paradigm — design exploration

**Status:** Proposal / live exploration (in progress)\
**Started:** 2026-06-04 (UX exploration session)\
**Updated:** 2026-06-05\
**Reader:** product + UX + whoever builds the next-gen Atelier front-of-house\
**Resume here:** §10 locks the mode names, §11 scopes the alpha v1 spine, §12 maps it
onto the NDJSON wire; jump to [Next step](#next-step) for the build kickoff.

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
| User carries all initiative (prompt every time) | **Mixed-initiative** — agent & user trade who-acts-when | Horvitz, *Principles of Mixed-Initiative UIs*, CHI 1999 `[E4]` |
| Safety = user approves each action | **Safety = containment** (the cage is the consent) | sandbox / zero-trust isolation |
| Each session is disposable | **Each session crystallizes into a replayable routine** | Programming-by-Demonstration (PbD) `[E9]` |

**Why this is white space:** the workflow-automation crowd (n8n, Zapier AI, node
canvases) makes reproducibility easy but reintroduces developer-grade configuration.
The chat crowd (ChatGPT, Claude) kills configuration but throws away reproducibility
and leans on consent. Almost nobody has shipped all three *together* for
non-developers. Atelier's containment architecture already votes for goal #1.

### Who this is for — the general class, and the wedge

The target is **not an industry — it is a *shape of work*:** recurring, file-based
knowledge work done by a non-developer — the pull → compare/validate → produce → judge loop
(the four stages of §5) that fills most operational, analytical, and back-office jobs.
Reconciliation is the sharpest instance, not the boundary. So are data QA, audit prep,
compliance checks, research-data cleaning, contract/document review, invoice & procurement
matching, onboarding checks — anything whose skeleton is
`INPUT → MATCH/VALIDATE → OUTPUT → DECIDE`. **The four-stage shape is the domain test:** if a
job has it, Atelier fits.

**The narrowness that makes the bet (§8) winnable lives in the *grammar*, not the
*vertical*.** "Relate two files on a shared key, flag the diffs, spawn a result" is a tiny,
domain-general vocabulary (§7) that reappears *identically* across recon, QA, audit, and
research. The agent guesses *within a small grammar*, not by learning each industry from
scratch — so broadening the domain does **not** weaken the guess-quality bet.

**Fund operations is the wedge, not the definition.** We lead with it (§4) because its
constraints are the *harshest* — confidential data that legally cannot leave the building,
four-eyes controls, zero tolerance for a wrong number. An agent that earns trust *there*
generalizes down to every looser domain. Containment unlocks the hard wedge; the four-stage
grammar carries it outward. **Vision broad, wedge narrow, expansion by proving guess-quality
one domain at a time.**

### Domains as inference lenses (the enabler)

If the grammar is general, a **domain** is the *prior* that points it — a lens that biases
what the agent guesses. Tell it "journalism" and a dropped file reads as a
transcript/source/draft, the proposed verbs are fact-check / extract-quotes / structure, the
output is an article, and DECIDE is editorial sign-off. "Art / photo" → RAWs, color-grade /
cull / batch-export, an exported set, aesthetic approval. "Travel" → constraints, plan /
optimize / fit-budget, an itinerary, book it. **Same four stages, same tiny op-vocabulary —
the domain just supplies the prior over each stage.**

**The deepest effect: the domain decides what stage ② *means*.** §5's stage ② is "the
operation," and the lens interprets it — recon → *match*, art → *transform*, travel →
*plan/optimize*, journalism → *structure/fact-check*. One grammar, many readings of the verb.

This is the mechanism behind "vision broad, grammar narrow." Atelier doesn't *learn* each
industry — it ships **domain packs** (a prior over {nouns, verbs, outputs, decision-points,
house vocabulary} over the one shared grammar). The platform scales as a **library of narrow
packs**, each individually guessable (§8), not one fuzzy generalist. Fund ops is the first pack.

**How the lens is chosen — infer first, never a dropdown wall.** Forcing a domain pick up
front would violate §5's "don't make the user pre-classify." So the agent **infers the domain
from the first drop and confirms it** (the same christening move as labeling a file —
correctable in one gesture), with explicit pick as an *accelerator/override* when the first
artifact is ambiguous. Because a saved routine (Goal 3) **carries its domain**, selection is a
run-#1 concern only — re-runs never ask. A wrong lens poisons every downstream guess, so the
agent must also *notice* a mismatch ("this doesn't look like journalism — switch lens?").

---

## 2. The three goals in depth

### Goal 1 — permission prompts are a design smell

Per-action consent is **decision offloading**: the system dumps its own uncertainty
on the user as a yes/no tax → consent fatigue → users click *Allow* blindly. The
dialog provides the *feeling* of safety without the substance. The field's reframe:
**safety is a property of the environment, not of the conversation** `[E26]` — which is
literally Atelier's containment thesis.

**Trap:** removing the dialog removes the (bad) trust signal it provided. Replace it
with three things:

1. **Legible containment** — the user must *see* the cage (what the agent can touch),
   ambiently. Invisible containment reads as *no* containment.
2. **Reversibility as the real safety net** — Action Audit & Undo `[E21]`. Move the decision
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

- **(a) Mixed-initiative (Horvitz 1999) `[E4]`.** System models likely intent and *offers*
  the next step. Canonical example LookOut: watches you read an email, infers
  "scheduling," pre-stages the calendar action — confirm with a glance instead of
  composing a request. *Most important paper for goal 2; 27 years old; mostly ignored.*
- **(b) Anticipatory / ambient.** Agent runs in the background on events/context, not
  prompts. Key primitive: **ambient presence display** `[E21]` — agent state via a
  low-attention signal (color, pulse), not a screen you must read. Prompting → glancing.
- **(c) Demonstration over description.** Highest leverage for non-developers: let
  users **show, not tell.** Research (ALLOY, PUMICE) `[E6, E9]` finds non-technical users express
  *procedural* preferences far better by demonstrating than by prompting — prompting
  forces them to verbalize tacit knowledge they don't have words for.

**Trap — the annoying paperclip (Clippy).** Failed not because anticipation is wrong
but because it interrupted with *low-confidence, high-cost* suggestions and couldn't
be taught. Guardrails: act silently when confident+reversible; *suggest* when medium;
*ask* only when high-cost (Autonomy Dial + Confidence Signal `[E21]`). Rejected suggestions
must change future behavior or you've built Clippy.

**Direction:** make the *default* surface not a prompt box but a **proposed next
action** the agent drafted from context — accept/tweak/dismiss. Free-text prompting
becomes the escape hatch (to *redirect*), not the front door (to *initiate*).

### Goal 3 — every session crystallizes into a replayable routine

This is **Programming by Demonstration**, newly tractable via 2025 LLM work:

- **ALLOY** (arXiv 2510.10049) `[E6]` — generates reusable agent workflows from user
  demonstration; workflow is a *visualized, transparent, editable* artifact.
- **AgentRR** (arXiv 2505.17716) `[E8]` — record one successful run, replay on new inputs;
  **replay acts as a guardrail** (more deterministic, less hallucination).
  *Reproducibility doubles as safety — connects Goal 3 back to Goal 1.*
- **PUMICE / Ringer** `[E9]` — classic end-user lineage: non-programmers automate by
  demonstrating + a little natural-language repair.

**Trap — the n8n trap (see §6) `[E23]`.** The no-code/node-canvas industry already "solved"
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
where PbD historically breaks `[E7, E10]` (plan a repair conversation, not perfect first-shot
inference); legible containment can become noise (must be ambient, not nagging); silent
action erases the audit trail people trust by — minimizing prompting and the reversible
ledger are inversely coupled.

---

## 4. Persona — Jonny, 34, Fund Accounting Operations

**The wedge, not the market** (see §1 — Atelier targets a *shape of work*, not an industry).
Chosen because fund ops is the *ideal* proving ground: the job is already made of the
materials we're designing for, and its constraints are the harshest, so winning here
generalizes outward to every looser domain.

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
  real data. (PUMICE/ALLOY pattern `[E7, E9]`.)

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
right… that meaning exists to us" — non-technical users read spatial layout natively.* `[E15]`

### What the n8n trap *is*

n8n = open-source Zapier/Make: drag **nodes**, wire **ports**, data flows left→right.
Powerful, but demands the user decide node types, wire connections, understand data
shapes, think in flowcharts = *programming with rounded corners.* Jonny bounces in 30s.
**The cliff:** the instant the canvas asks Jonny to *wire* anything, you've become n8n —
reproducibility kept, non-developer lost. Our edge: keep the *graph*, move the *pen* from
human to agent.

### Risks to hold the line on

Blank-canvas paralysis `[E28]` (drop-file entry + a gentle default so it's never a cold void);
invisible affordances ("click to add text" is undiscoverable → agent *demonstrates* with
a ghost text object); spatial overwhelm (agent **auto-lays-out**; Jonny *can* nudge, never
*must* arrange); the n8n cliff (every structure piece agent-drawn + human-corrected,
never human-authored).

---

## 7. Architecture — "drive the canvas from within the agent"

The pivotal decision. The canvas becomes **a shared surface where the agent is a
first-class actor** (place/draw/ask/spawn) — its **hands and eyes**: it *reads* canvas
state to know what's happening, and *writes* canvas state to act.

**The mechanism is already proven** `[E19]` — tldraw's agent starter kit: canvas → agent
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
"Plan-Preview-Apply" ghost-node preview `[E20]`), ambient presence ("looking at your files").

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
most sophisticated AI canvas shipping. **But the human still wires the graph** `[E14]` — founder
Steve Ruiz frames it as human direction with AI assistance, not an AI that autonomously wires
the connections ("arrows and LLMs powered every step of a graph," his words). It's n8n with
better taste + AI in the boxes. **Our differentiator —
agent draws, human corrects — is the exact step they chose not to take.**

**Three clusters, each with a gap:**

1. **AI-canvas / spatial tools** (tldraw computer, Flowith, Lovart ChatCanvas, OpenAI
   Agent Builder, Figma Make): right surface, **human is the wirer**, aimed at
   *creative/builder* work, not non-dev ops. None infers "you're reconciling these — shall I?" `[E27]`
2. **Finance reconciliation agents** (moveo, Hypatos, wizr, BlackLine-style STP): right
   vertical, but **top-down engineer-configured black-box STP** — Jonny consumes output,
   never teaches it, never sees a canvas. Automation *done to* his team, not wielded *by* him. `[E22]`
3. **PbD research** (ALLOY, AgentRR, Toby Li co-adaptive PbD): right *thesis* (agent learns
   by watching; workflow is a byproduct), but **academic prototypes** — web/mobile-agent,
   not spatial, not finance, not shipped. `[E6, E8, E25]`

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

> Several of these are now evidence-backed by the §13 sweep — especially parameterization
> (unsolved in the literature) and guess-quality/repair (the PbD data says lead with the
> repair conversation, not one-shot). Read §13 alongside this list.

- **Guess quality + graceful repair (stage ②)** — the make-or-break. Everything rides on
  "agent proposes, you correct" feeling better than "you build it." *(§13: in Alloy,
  hand-building beat demonstration on first attempt — don't sell one-shot.)* `[E7]`
- **Parameterization** — constants-vs-parameters inference; the historical PbD failure point. `[E7, E10]`
- **Confidence + fallback UX** — how the agent signals low confidence and offers "show me one
  match you'd accept" instead of a dead end.
- **Generalization proposals** — agent proposing "apply $50 to all bonds?" without sounding
  like a config wizard.
- **Where stage 4 must stay human** — agent must know what it's *not* allowed to learn.
- **Maker-checker handoff** — design the elevated, consequential sign-off as the model for
  *all* escalation.
- **Persona validation** — confirm Jonny's segment (fund accounting vs. adjacent).

---

## 10. Modes — Canvas & Sketch (naming)

Atelier has two interaction modes. The canvas is the flagship front door; chat is the
escape hatch — not co-equal peers (per §3, the routine is the unit, conversation is the
redirect).

| Mode | Was (in code) | Is | Role |
| --- | --- | --- | --- |
| **Canvas** | "WORK mode" | spatial, agent-draws-you-correct, work crystallizes into a routine | flagship |
| **Sketch** | "chat mode" | linear conversation, one-off, freeform | escape hatch / redirect |

The relationship between the names *is* the product story: a **sketch** is rough and
exploratory; it **graduates** into a **canvas** when worth keeping (mirrors §3 — "a new
task is an unnamed routine in progress"). "Sketch" signals *rough mode* without signalling
*lesser mode*. Both words live in one workshop (Atelier).

Rejected: **Maestro** (conducting/orchestration fits the *canvas*, not the chat) — better
repurposed later as the agent's name than a mode; **Flow** (the n8n/Zapier vocabulary we
are explicitly fleeing, §6). *Naming not yet propagated to code/docs (the stack still says
"WORK mode" / "chat mode").*

---

## 11. Alpha — Canvas v1 (the build target)

v1 proves the **whole differentiating spine in one happy path** — two files, a reconcile,
an output — *not* a single-file loop. Single-file would only prove "ChatGPT with file
upload"; the two-file reconcile is the §8 bet and the doc's `relate` keystone. That is the
thing worth building first.

**The spine** — three lanes: canvas (renderer, local) · wire (NDJSON semantic event) ·
agent (partisan, in cage):

```text
1. open          agent bubble center — "drop a file to start"
2. drop file 1   object appears instantly, gray pulsing   [local 0ms, optimistic ghost §7]
                 wire: file_dropped(path) → agent reads in cage, guesses
3. resolve 1     object = label("Fund NAV report") + mini-table + "Right? / Not quite"
4. drop file 2   second ghost → agent reads, detects shared key
5. THE LEAP      relate(A,B,"share trade ID — reconcile book vs custodian?")
   (relate)      → ghost edge "drawing…" + question on the edge
                   options [Reconcile] [Not that] [something else]
6. confirm       answer(edge,"reconcile") → commit(edge→reconcile); agent states its
                 default: "matching on trade ID, flagging diffs over $50"
7. OUTPUT        spawn(output ← edge) → new object "847 matched · 3 breaks" + break rows
   (spawn)
8. decide        Jonny eyeballs the 3 rows (stage ④ stays human)
```

Exercises 6 of the 7 vocabulary ops (`place, label, relate, commit, spawn, ask`) — proves
the whole grammar in one demo.

**The two load-bearing moments.** The *relate leap* (step 5) must feel **noticed, not
configured** — the question lives on the edge, posed in his language ("reconcile book vs
custodian?"), arriving as a ghost edge, never a committed action. The *output object*
(step 7) is the payoff and must be **legible evidence**: the count *plus* the actual break
rows highlighted (the §5 moment — "847 matched, 3 breaks — right?"), an object with an edge
back to its sources so the canvas reads as a *space*, not a chat.

**The trust triad** on every resolved file object: **label** (his language) + **table**
(evidence it read the real data) + **"Right? / Not quite"** (one-gesture correctable). The
confirm click is the *christening* (§5) — the one consequential sign-off we keep, not a
procedural tax.

**Must-decide before build:**

1. **File → cage plumbing.** Renderer has the bytes; partisan reads the *guest* fs. v1:
   write into `/sessions/<tag>/` via the Files door; partisan reads from there. Nail this
   first — nothing downstream runs without it.
2. **Tolerance = a stated default, never a dialog.** Agent picks ($50), *announces* it,
   correctable later. A "set your threshold" prompt = n8n (§6 cliff).
3. **Controlled demo data** — two files, a clean shared key, ~3 real breaks. Happy path;
   v1 proves the *feel*, not robustness.

**Deferred in v1:** the guess re-guess loop ("Not quite" → retype the label is enough);
row-level rule repair (the §5 accretion engine — defer, but leave the break rows
*clickable* so the affordance is visible); routine saving (free later — every step is
already an agent op).

---

## 12. Architecture — canvas on the existing NDJSON wire

The agent **already emits a structured event per tool call** (`tool_use`,
`apps/desktop/src/main/host-client/types.ts`); a canvas-op is just another message family
in the *same* loop the app already ships and tests. So "still a chat session underneath" is
literal: **a semantic gesture is a user turn; a canvas-op is a tool call; the canvas is a
projection of the conversation — and that projection is the replayable routine (Goal 3),
for free.**

**Where it lives — one truth, three layers:**

| Layer | Holds | Why |
| --- | --- | --- |
| Renderer (`renderer/features/canvas/`) | the view + optimistic mechanical state | drag/zoom/pan must be 0ms — never cross the wire (§7) |
| Main / Session Manager (`sessions/manager.ts`, `store.ts`) | **the durable canvas document = source of truth** | survives renderer reload; *is* the saved routine; host-owned like the transcript |
| Agent (`cli_guest.py`) | nothing persistent — fed canvas context per turn | it's a chat turn underneath; stateless re: canvas in v1 |

Main owns the truth; the renderer is a fast mirror; the agent is a per-turn op-emitter
handed the canvas state in the turn. Do **not** make the agent hold canvas truth in v1 —
that is the hard version and unnecessary.

**The exchange — extend the two existing unions** (`host-client/types.ts`, with the
hand-kept mirror in `cli_guest.py`):

- Inbound `LoopControl` += a typed `gesture` (`file_dropped {objectId, path}`,
  `answer {edgeId, choice}`), which `cli_guest` renders into a user turn. Typed on the wire
  (not raw text) is what keeps the op-log replayable.
- Outbound `LoopEvent` += `canvas_op {op, objectId?, edgeId?, payload}`. The 7 ops
  (`place / label / relate / commit / spawn / ask / flag`) are **custom OpenHands tools**;
  each handler `emit()`s its op and returns "ok" to the model. The agent *cannot* create
  anything outside the 7 — §7's no-n8n discipline becomes **enforced by the tool surface,
  in code**. The op sequence *is* the routine.

**Domain packs (§1) ride the same seam, no new wire machinery.** The active lens is a *prior
injected into the agent's per-turn context* (a system-prompt fragment + example noun/verb
sets) — it shapes *which* canvas-ops the agent proposes, not how they travel. The inferred
domain rides back as a `canvas_op` (or a field on `init`); a saved routine persists its lens
in the durable doc, so re-runs reload it for free.

**The round trip:**

```text
drop → optimistic ghost (renderer assigns temp id) → IPC gesture → Manager writes bytes
  to /sessions/<tag>/ → PartisanClient.gesture() → stdin → cli_guest renders a user turn
  → OpenHands Conversation → agent Read/Bash in cage, guesses → calls label()/relate()
  canvas tools → each handler emit({type:"canvas_op",...}) → PartisanClient.onEvent
  → Manager applies op to the durable doc + persists (store.ts) → IPC push → renderer
  applies: ghost resolves / ghost edge draws / output spawns
```

**Correlation trick (optimistic ghosts):** the renderer assigns the object id at drop and
ships it in the gesture; the agent's ops reference that same id (learned from the canvas
context fed into the turn) → the local ghost and the resolving op line up, no flicker, no
dupe.

**Files — the vertical:** `cli_guest.py` (7 canvas tools + gesture→turn) ·
`host-client/types.ts` (extend both unions, keep the py mirror in sync) ·
`sessions/client.ts` (add a `gesture()` send method) · `sessions/manager.ts` + `store.ts`
(own / apply / persist the doc, route ops to the renderer) · `ipc/channels.ts` + preload
(gesture-in / op-out channels — or fold ops into the existing `WorkEvent` push) ·
`renderer/features/canvas/` (new feature).

**Decisions still open:**

1. **Canvas runtime** — tldraw (proven agent-kit plumbing, §7 `[E19]`; heavy/opinionated) vs
   hand-rolled React (light; rebuild pan/zoom/hit-testing). Lean: **tldraw** for v1, to
   de-risk the mechanical layer and spend effort on the relate *judgment* (the real bet).
2. **Gesture encoding** — typed control → turn (rec) vs raw templated `user` text.
3. **Canvas-op event** — dedicated `canvas_op` + custom tools (rec, enforces the
   vocabulary) vs reusing the generic `tool_use` event.

**Recommended first build step:** prototype the tldraw canvas in the renderer driven by a
*faked* op-stream behind the existing `LoopTransport` seam (the one the tests already use),
to feel ghost→resolve→relate before wiring the cage.

---

## 13. External research & evidence

Literature and market evidence for and against the design above. Each finding references its
source in §14 by `[E#]`, tiered there ([R] research · [P] primary · [A] analyst · [O] opinion).

### What validates the thesis

- **Chat is genuinely poor for analytical/ops work.** *The Keyhole Effect* `[E1]` — chat's
  linear "keyhole" overloads working memory (~4 items, Cowan 2001) and loses spatial layout;
  you can't juxtapose views. Direct support for canvas-over-chat *in our domain*.
- **A shared visual workspace measurably beats dialogue.** *CoMAP* `[E2]` — human+AI on a
  shared graph canvas scored higher expression (d=1.11) and understanding (d=1.16) with lower
  cognitive load vs a dialogue-only baseline. Empirical support for
  canvas-as-externalized-working-memory.
- **Vertical/domain agents are winning** `[E16, E17]` — Bessemer's *State of the Cloud 2024*
  predicts vertical-AI market cap ≥10× legacy vertical SaaS; Menlo Ventures measures **$3.5B**
  invested in vertical AI in 2025, ≈3× the prior year. With our wedge/domains framing.
- **The n8n trap is real** `[E23]` — n8n is built for a specific kind of user; non-devs hit a
  real learning-curve drag. tldraw.computer is node-and-wire / human-wires `[E14]` — the §8
  differentiator holds.

### Prior art / novelty check (the uncomfortable part)

- **Our deepest "selling point" already has a paper.** *Mixed-Initiative Context* `[E3]` —
  "reconceptualizes context formed during multi-turn human–AI collaboration as an
  interactive object that can be explicitly surfaced, structured, and managed." Almost
  verbatim our chat-as-objects + building-context thesis.
- **"Beyond the chatbox" / generative UI is a named 2026 trend** `[E24]` (CopilotKit, AG-UI;
  Google's A2UI echoes it). The space is filling.

→ **The representation is the *substrate*, not the moat.** Differentiation is the
*combination*: agent-draws-the-canvas **+** containment (reads real confidential data others
can't) **+** domain-pack priors **+** non-dev-ops vertical **+** routine-as-byproduct. Claim
the stack, not the canvas.

### Debiases (disconfirming evidence)

- **"Demonstrate once, just correct it" is empirically shaky.** *Alloy* `[E7]` — the flagship
  2025 LLM-PbD system, ≈ our §3/§5 plan: hand-building **beat** demonstration on first-attempt
  success (83% vs 75%, single-agent baseline 58%); corrections come in **multiple rounds**;
  workflows are **linear** (no conditionals/loops/error recovery); parameterization is delegated
  to an LLM guessing which literals are variables (the classic constants-vs-variables problem
  *relocated, not solved* — our §9 risk); demos capture *how, leaving out why* → confident
  misgeneralization; only 2 of 12 users noticed the workflow updating as they demonstrated
  (undercuts "watch it think builds trust"). The canonical warning stands `[E10]`. Useful
  alternative: the classic **"imitation"** approach — *ask on each new case* instead of
  generalizing (maps onto our escalation/repair loop).
- **Agent-drawn structure can cause premature convergence.** *Cognitive Bridge* `[E11]` —
  AI-generated boundary objects "risked premature convergence that constrained creative
  exploration." A confident first draft can **anchor** the human. → make *diverging* as
  cheap as *confirming*; have the agent offer alternatives, not a single guess.
- **The canvas is a liability without the agent** `[E28]`. An infinite canvas is only as useful
  as what understands the things on it; drop forty cards on a blank canvas and you have forty
  cards, and most users facing a blank canvas never return. Confirms §6's blank-canvas risk and
  reframes it: **the agent-understanding *is* the product; the canvas is just where it shows.**
- **Proactivity is a hard, actively-contested frontier — not abandoned.** ChatGPT Pulse
  (launched Sep 2025, expanding Pro→Plus→all) `[E13]` shows both incumbent investment and a
  competitive threat — OpenAI owns the horizontal "works while you sleep" space. Our edge:
  proactivity **scoped to a contained, domain-specific routine over real confidential files** —
  what Pulse structurally can't reach. Caution: proactive assistance carries real
  interruption/timing trade-offs `[E12]` — Clippy's actual lesson.

### Implications for the design

1. **Reframe the "selling point"** (§1, §7): substrate = chat→objects+context (grounded, not
   novel); moat = the stack on top. Claim the combination.
2. **Don't sell one-shot demonstration** (§3, §5): lead with the repair conversation; design
   for *several* correction rounds + an "imitation"-style ask-on-new-case fallback.
3. **Make divergence cheap** (anti-premature-convergence): agent proposes *with
   alternatives*; confirm and reject equally one gesture.
4. **Treat parameterization (constants vs variables) as a named, unsolved risk** (§9), not a
   detail — the research says nobody has cracked it.

---

## 14. Claims & sources

Every external claim the argument leans on, with its tier and source. Prose references a row by
`[E#]`; citekeys resolve in [Source keys](#source-keys). Tier: [R] research · [P] primary ·
[A] analyst · [O] opinion.

| ID | Claim | Tier | Source |
| --- | --- | --- | --- |
| E1 | Chat's linear "keyhole" overloads working memory (~4 items) | R | `@keyhole2026` |
| E2 | Shared canvas beats dialogue — expression d=1.11, understanding d=1.16, lower load (N=30) | R | `@comap2026` |
| E3 | Multi-turn context can be a surfaceable, structured, manageable interactive object | R | `@mictx2026` |
| E4 | Horvitz, *Principles of Mixed-Initiative UIs* (CHI 1999); Lookout infers scheduling from email | R | `@horvitz1999` |
| E5 | Working-memory capacity is ~4 chunks, not 7 | R | `@cowan2001` |
| E6 | Demonstration can yield a reusable, visualized, editable workflow artifact | R | `@alloy2025` |
| E7 | Hand-building beat demo on first attempt (83% vs 75%, baseline 58%); workflows linear; LLM-guessed parameterization; only 2/12 noticed live updates | R | `@alloy2025` |
| E8 | Record one run, replay on new inputs; replay acts as a guardrail | R | `@agentrr2025` |
| E9 | Non-programmers automate by demonstrating + a little natural-language repair | R | `@pumice2019` |
| E10 | Canonical PbD-failure warning (lessons for usable AI) | R | `@lau2009` |
| E11 | AI-generated boundary objects risk premature convergence that constrains exploration | R | `@cogbridge2026` |
| E12 | Proactive programming support carries real interruption/timing trade-offs | R | `@proactive2025` |
| E13 | ChatGPT Pulse launched Sep 2025 and is expanding (Pro→Plus→all) | P | `@openai-pulse-2025` |
| E14 | tldraw computer is node-and-wire; the human wires the graph | P | `@tldraw-computer`, `@ruiz-latentspace` |
| E15 | "source on the left, result on the right… that meaning exists to us" (verbatim) | P | `@ruiz-latentspace` |
| E16 | Vertical-AI market cap predicted ≥10× legacy vertical SaaS | A | `@bvp-sotc-2024` |
| E17 | $3.5B invested in vertical AI in 2025 (≈3× prior year) | A | `@menlo-2025` |
| E19 | tldraw agent kit: canvas↔agent via structured shape data + mutation ops | P | `@tldraw-agent-kit` |
| E20 | cognograph "Plan-Preview-Apply" ghost-node preview pattern | P | `@cognograph` |
| E21 | Agentic-UX pattern vocabulary (Autonomy Dial, Confidence Signal, Action Audit & Undo, Ambient Presence…), anchored in HCI research | R | `@amershi2019`, `@horvitz1999`, `@smashing-agentic-ux`, `@bprigent-ambient`, `@aiux-ambient`, `@uxmag` |
| E22 | Recon incumbents are top-down, engineer-configured black-box STP — operator consumes output, never teaches it | P/O | `@moveo`, `@hypatos`, `@wizr` |
| E23 | n8n is an open-source node-and-wire Zapier/Make alt; non-devs hit a learning curve | P | `@n8n` |
| E24 | "Beyond the chatbox" / generative UI is a named 2026 trend | P | `@copilotkit`, `@ag-ui` |
| E25 | Co-adaptive human–agent PbD relationship (Li et al., CHI 2018 workshop) | R | `@tobyli-coadaptive` |
| E26 | Safety is a property of the environment, not the conversation (least-privilege / capability-security) | R | `@least-privilege`, `@dust-safe`, `@mindstudio-safe` |
| E27 | AI-canvas competitors keep the human as the wirer of the graph | P/O | `@flowith`, `@tldraw-computer` |
| E28 | An infinite canvas is useless without an agent that understands its contents | O | `@storyflow` |

### Source keys

Resolves the citekeys above. Tier in brackets.

- `@keyhole2026` — [R] [The Keyhole Effect — why chat fails at data analysis (arXiv 2602.00947)](https://arxiv.org/pdf/2602.00947)
- `@comap2026` — [R] [CoMAP — shared visual workspace (arXiv 2604.06200)](https://arxiv.org/html/2604.06200v1)
- `@mictx2026` — [R] [Mixed-Initiative Context (arXiv 2604.07121)](https://arxiv.org/pdf/2604.07121)
- `@horvitz1999` — [R] [Horvitz, Principles of Mixed-Initiative UIs (CHI 1999, PDF)](http://erichorvitz.com/chi99horvitz.pdf)
- `@cowan2001` — [R] [Cowan, The magical number 4 in short-term memory (2001)](https://doi.org/10.1017/S0140525X01003922)
- `@alloy2025` — [R] [Alloy — PbD with LLM web agents (arXiv 2510.10049)](https://arxiv.org/html/2510.10049v1)
- `@agentrr2025` — [R] [AgentRR — record & replay (arXiv 2505.17716)](https://arxiv.org/abs/2505.17716)
- `@pumice2019` — [R] [PUMICE (arXiv 1909.00031)](https://arxiv.org/pdf/1909.00031)
- `@lau2009` — [R] [Lau — Why PBD Systems Fail (2009, AI Magazine)](https://doi.org/10.1609/aimag.v30i4.2262)
- `@cogbridge2026` — [R] [Cognitive Bridge — AI-generated boundary objects (ACM)](https://dl.acm.org/doi/10.1145/3772318.3791399)
- `@proactive2025` — [R] [Proactive AI programming support trade-offs (CHI 2025)](https://dl.acm.org/doi/10.1145/3706598.3713357)
- `@tobyli-coadaptive` — [R] [Li et al. — Supporting Co-adaptive Human-Agent Relationship through PbD (CHI 2018 workshop)](https://toby.li/files/Li_SupportingCoAaptiveHumanAgentRelationship.pdf)
- `@amershi2019` — [R] [Amershi et al. — Guidelines for Human-AI Interaction (CHI 2019)](https://www.microsoft.com/en-us/research/wp-content/uploads/2019/01/Guidelines-for-Human-AI-Interaction-camera-ready.pdf)
- `@least-privilege` — [R] [Principle of least privilege / capability-based security](https://en.wikipedia.org/wiki/Principle_of_least_privilege)
- `@openai-pulse-2025` — [P] [Introducing ChatGPT Pulse (OpenAI)](https://openai.com/index/introducing-chatgpt-pulse/)
- `@tldraw-computer` — [P] [tldraw computer](https://computer.tldraw.com/)
- `@tldraw-agent-kit` — [P] [tldraw agent starter kit](https://tldraw.dev/starter-kits/agent)
- `@ruiz-latentspace` — [P] [Latent Space — Steve Ruiz interview](https://www.latent.space/p/tldraw)
- `@cognograph` — [P] [cognograph (Plan-Preview-Apply)](https://github.com/skovalik/cognograph)
- `@n8n` — [P] [n8n.io](https://n8n.io/) · [workflow gallery](https://n8n.io/workflows/)
- `@flowith` — [P] [Flowith](https://flowith.io/)
- `@copilotkit` — [P] [CopilotKit — the frontend stack for agents (GitHub)](https://github.com/CopilotKit/CopilotKit)
- `@ag-ui` — [P] [AG-UI protocol](https://www.copilotkit.ai/ag-ui)
- `@bvp-sotc-2024` — [A] [Bessemer — State of the Cloud 2024 ("Vertical AI ≥10× legacy SaaS")](https://www.bvp.com/atlas/state-of-the-cloud-2024)
- `@menlo-2025` — [A] [Menlo Ventures — 2025 State of Generative AI in the Enterprise ("$3.5B vertical AI")](https://menlovc.com/perspective/2025-the-state-of-generative-ai-in-the-enterprise/)
- `@smashing-agentic-ux` — [O] [Smashing — Designing Agentic AI UX patterns](https://www.smashingmagazine.com/2026/02/designing-agentic-ai-practical-ux-patterns/)
- `@bprigent-ambient` — [O] [bprigent — 7 UX patterns for human oversight in ambient AI](https://www.bprigent.com/article/7-ux-patterns-for-human-oversight-in-ambient-ai-agents)
- `@aiux-ambient` — [O] [AI UX Playground — Ambient Presence Displays](https://www.aiuxplayground.com/pattern/ambient-presence-displays)
- `@uxmag` — [O] [UX Mag — Secrets of Agentic UX](https://uxmag.com/articles/secrets-of-agentic-ux-emerging-design-patterns-for-human-interaction-with-ai-agents)
- `@moveo` — [O] [moveo.ai — financial reconciliation AI agents](https://moveo.ai/blog/financial-reconciliation-ai-agents)
- `@hypatos` — [O] [Hypatos — agentic back-office automation](https://hypatos.ai/guides/agentic-back-office-automation)
- `@wizr` — [O] [wizr — agentic AI for finance & accounting](https://wizr.ai/blog/agentic-ai-for-finance-and-accounting/)
- `@dust-safe` — [O] [Dust — agents only as safe as where they run](https://dust.tt/blog/ai-agents-safe-as-where-they-run)
- `@mindstudio-safe` — [O] [MindStudio — safety is a system problem](https://www.mindstudio.ai/blog/ai-agent-safety-system-vs-model-problem)
- `@storyflow` — [O] [Storyflow — 12 Best Infinite Canvas Tools 2026](https://storyflow.so/blog/best-infinite-canvas-tools-2026)

---

## Next step

Naming locked (**Canvas + Sketch**, §10); alpha v1 spine scoped (§11); architecture
drafted onto the NDJSON wire (§12). To resume:

1. **Lock the three open decisions** (§12): tldraw vs hand-rolled canvas, gesture encoding,
   canvas-op event shape.
2. **Build step 1** — the tldraw canvas in the renderer driven by a *faked* op-stream
   behind the `LoopTransport` seam, to feel ghost→resolve→relate before touching the cage.
3. **Spec the `relate` op payloads** in full: the **request** (file 2 dropped, shared key
   detected), the **ghost-edge proposing state**, the **reconcile? / join? question UI**,
   the **graceful-degradation path** when the guess is wrong ("show me one row you'd call a
   match"), and how corrections **attach to the edge and accrete into the saved rule** (the
   §5 repair engine — deferred past v1, but designed now).

Goal unchanged: make "I'll guess, you fix it" feel *safer and faster* than tldraw's "you
wire it."
