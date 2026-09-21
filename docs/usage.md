# Working with verdict

The operations manual: usage modes, discovery modes, artifacts, flags,
costs, and "what to do when it breaks". For how the pipeline works
internally see [how-it-works.md](how-it-works.md).

## Your workflow at a glance

| Scenario | What you do |
|---|---|
| **Research something new** (the comfortable way) | Open a Claude Code session **in this repo**, say: `/research <your problem or question>`. The agent runs the [research skill](../.claude/skills/research/SKILL.md): hands you 1-3 Consensus queries → you paste the results back (scroll each list to the end) → it runs the pipeline and explains the report in plain language. |
| **Research something new** (hands-on, no agent) | `bin/verdict web -q "<question>"` → run the question at consensus.app, scroll to the end, copy the list → `pbpaste \| bin/verdict resolve > dois.txt` → `bin/verdict run --topic <topic> --dois dois.txt -q "<full question>"` → read `runs/<topic>/<run>/report.md`. |
| **Ask questions over finished research** | Terminal: `bin/verdict ask --run-dir runs/<topic>` (whole topic) or `.../runs/<topic>/<run>` (one run). Or just ask the agent: `/research answer questions about topic <topic> using runs/<topic>`. |
| **Challenge a conclusion / add papers** | Tell the agent (it assembles the corrected DOI list, re-runs in the same topic, compares verdicts) — or build the union DOI list yourself and `run --topic <same topic>` again. |
| **A messy life problem, not a question** | `/research <your story>` (the agent triages it via `brief`) — or yourself: `bin/verdict brief problem.txt`. |

Nothing here is mandatory: every scenario also works with bare CLI commands,
the skill is just the agent doing them for you.

## The three ways to work

**Start here if your problem is a story, not a question** — real problems
arrive as descriptions, not research questions. Run them through triage
first:

```bash
bin/verdict brief problem.txt        # or: pbpaste | bin/verdict brief
```

`brief` splits the problem into: what a capable agent settles on its own
(shopping, engineering, logistics, spec sheets) versus scientific evidence
questions, and prints ready-to-run `verdict run` commands for the latter.
It also extracts factual claims worth verifying, marking which of them an
agent can check and which need the evidence pipeline.

Related: `bin/verdict pico -q "<question>"` normalizes a question into the
PICO frame (population / intervention / comparison / outcome) and prints
ready queries for both search flavors — the PubMed-style keywords for mode
A and 1-3 natural-language queries for mode C's free Consensus web UI.

### 1. Plain CLI: run it — get the report

```bash
make run QUESTION="does creatine improve cognitive performance"
# same by hand: bin/verdict run -q "..."
```

The result lands in `runs/<question>-<date>/`: `report.md` (verdict,
consensus, matrix, contradictions, gaps), `matrix.csv` (the matrix as a
table), `context.md` (a compact digest of the whole run). Good for "look
and shelve".

### 2. CLI + analyst Q&A (`ask`)

Follow-up questions over finished runs — an analyst holding the full
context (papers, tallies, critiques, verdict), the same LLM backend the
pipeline used:

```bash
bin/verdict ask --run-dir runs/keratosis-uvb                  # the whole topic
bin/verdict ask --run-dir runs/keratosis-uvb/kp-light-full    # one run
bin/verdict ask --run-dir runs/... -q "why did the mega-trial disagree?"  # one-shot
```

- answers only from the run's data, citing DOIs; "not in the data" is a
  normal answer;
- the dialogue history carries over between questions (follow-ups work);
- questions are sequential — one LLM call at a time.

### 3. Everything inside a Claude Code / agent session

The research-and-ask loop in one session:

1. Ask the agent: "run `bin/verdict run -q "<question>"`, wait for it to
   finish, then read `runs/<...>/context.md`".
2. Then just ask the agent questions in chat — it answers from context.md
   (the whole run is there: question, verdict, consensus, every paper with
   its DOI, supported/refuted counts, the critic's concerns).
3. To go deeper, the agent reads `runs/<...>/2-enrichment.json` /
   `3-critique.json`.

No need to spawn subagents for Q&A: the parallel work already happens
inside the pipeline (critics fan out); one agent with the context is
enough for questioning.

## Discovery: mode C (semantic, free) is the default

The default workflow — semantic search quality without paying:

1. The agent hands you 1-3 ready queries for the **free Consensus web UI**
   (`bin/verdict web -q "..."` prints the question and opens consensus.app).
   Reformulations, not one query: the Consensus UI caps a single list at
   ~50 rows ("Load more" stops there) — the extra queries recover the
   recall the cap cuts off.
2. You run each query, **scroll the results list to the end** (~50 rows),
   then select and copy the whole page — raw paste back is fine. The web
   copy carries titles (no DOIs); a CSV export, when available, carries
   DOIs too.
3. The agent resolves titles to DOIs (`bin/verdict resolve` — prints each
   DOI next to the matched title so the match is verifiable; `bin/verdict
   dois` when the paste/CSV carries DOIs), merges and dedupes, then runs
   `verdict run --dois ... -q "<full question>"`.
4. Optional recall booster: `bin/verdict snowball dois.txt` — works
   connected to several of your papers at once (co-citation signal).
   Candidates are printed for curation; the agent picks the on-topic ones
   and says what it added.

Why the paste step (verified live): semantic search finds papers whose
text does not contain the query terms — e.g. NB-UVB studies on related
follicular disorders. Keyword search structurally cannot. **Never silently
replace this step with keyword curation.**

Fallbacks — only when you explicitly opt out of the manual step:

- **Mode A**: `run -q "<2-4 keywords>"` — free, fully automatic,
  keyword-based.
- **Mode B** (planned): fully automatic semantic discovery through the
  Consensus API/MCP (Pro $12/mo, 500 uses/mo) — not wired into `run` yet;
  see the roadmap. Until then mode C is the semantic path.

**Mode A query tip (verified live):** OpenAlex search is keyword-based —
long natural-language questions dilute relevance and pull in tangential
reviews. Formulate mode-A queries like a PubMed search: 2-4 keywords
(`keratosis pilaris laser`), not full sentences. Full questions are fine
for the critique/synthesis stages (`-q` is used for relevance rating) —
only discovery suffers.

**Agent-curator fallback (opt-in):** when a mode-A run already happened and
its evidence matrix reports mostly `tangential` relevance, an agent may
probe OpenAlex with several short keyword queries and curate DOIs itself.
This is a *patch*, not a substitute for semantic search — the agent only
sees what keyword search sees. Always tell the user the corpus came from
keyword curation, and offer the mode-C redo.

## Run artifacts

Runs are grouped by topic — one research problem, one directory:

```
runs/<topic>/<run>/        e.g. runs/keratosis-uvb/kp-light-full/
```

`run --topic <name>` puts a run there; `ask --run-dir runs/<topic>` loads
all runs of the topic at once, `--run-dir runs/<topic>/<run>` pins one.
Keep only correct runs: when a run is superseded or broken, delete it
(after confirming with the user) — no archive pile.

| File | What it is |
|---|---|
| `0-input.txt` | the raw paste/DOI list the corpus came from (audit trail) |
| `1-discovery.json` | found papers (DOIs, abstracts, design tags); its `question` field is reused verbatim by challenge re-runs |
| `2-enrichment.json` | Scite tallies + retraction flags (OpenAlex **and** Crossref/Retraction Watch) + OA links + who contradicts (Scite key) |
| `3-critique.json` | per-paper LLM critiques (quality, relevance, direction of findings) |
| `4-synthesis.json` | the final report JSON |
| `report.md` / `matrix.csv` | human-readable report / matrix as a table (numbers assembled deterministically, never LLM-copied) |
| `context.md` | digest for `ask` and interactive agents |
| `fulltext/*.pdf` | paper PDFs: open-access ones (downloaded by default) or ones you drop in yourself (`<doi>.pdf`) — full-text critique + chart checks |
| `3b-vision.json` | optional agent-written chart critique (via the vision MCP); the synthesis stage weighs it |

Every `N-*.json` is a full state snapshot: `--resume` continues a failed
run from the last completed stage without paying twice.

## Common `run` flags

```bash
--resume                 # continue a failed run
--topic keratosis-uvb    # group the run under runs/<topic>/
--run-dir runs/<...>     # write into a specific directory (overrides --topic)
--stop-after 2           # only the deterministic stages (no LLM)
--dois dois.txt          # input as a DOI list (whole list runs; above the
                         #   default you get a cost warning; --max-papers caps)
--max-papers 25          # discovery limit / DOI-list cap
--llm-command claude-zai # LLM wrapper for this run (default comes from .env)
--llm-model <exact name> # model for both LLM stages (aliases do not resolve)
--model-critique <name>  # cheap critic + strong judge (per-stage overrides)
--model-synthesis <name>
--challenge-note "..."   # a counterargument the judge must weigh and answer
--fulltext               # ON BY DEFAULT: download OA PDFs for full-text critique
                         #   (+ chart checks); --fulltext=false to skip
                         #   (a PDF you drop into <run>/fulltext/<doi>.pdf is
                         #   always used, flag or not — the paywall workaround)
# bin/verdict addpdf ~/Downloads/paper.pdf --run-dir runs/<topic>/<run>
#   drops a downloaded PDF into the run; the DOI is read from the PDF itself
--llm-concurrency 4      # LLM stage parallelism
```

## Costs

| Component | Default | Paid option |
|---|---|---|
| Discovery | mode C — Consensus web + your paste: $0 | Consensus Pro $12/mo (API & MCP, 500 uses/mo — integration pending); mode A keyword search — free |
| Scite | free (tallies without a key, 1 batched request per run) | Pro $50/mo — not needed at this volume |
| LLM | the Claude Code tariff | direct API ≈ $0.3–0.5/run (estimate) |

An average run over 15 papers = 16 LLM calls of 2–4k tokens — a drop in the
bucket of a GLM Coding Plan quota (15,000 credits per 5 hours).

## verdictd (the HTTP wrapper)

`bin/verdictd --addr localhost:8080` exposes the same pipeline over HTTP —
handy for driving runs from other tools. One run executes at a time (the
LLM concurrency budget lives inside the stages); artifacts land in `runs/`
exactly as with the CLI.

```
POST /runs   {"question": "...", "topic": "...", "max_papers": 15,
              "fulltext": true, "stop_after": "2"}        # or "dois": [...]
GET  /runs                                                # jobs with status
GET  /runs/{id}                                           # one job + verdict
GET  /runs/{id}/{artifact}                                # report.md, context.md, N-*.json, ...
```

## When something breaks

- **The pipeline died midway** → `run --run-dir runs/<...> --resume`.
- **claude-zai reported a corporate gateway block** → that is the one-way
  cooldown after the first zai request; wait out the timer, re-login does
  not help.
- **LLM answers are garbage/empty** → make sure `zsh -ic 'echo MARKER'
  2>/dev/null` prints nothing but MARKER (junk from ~/.zshrc leaks into the
  answer).
- **Scite is silent about recent papers** → not an error: "no data", and
  that is what lands in the report.
