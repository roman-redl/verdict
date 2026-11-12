# Working with verdict

The operations manual: usage modes, discovery modes, artifacts, flags,
costs, and "what to do when it breaks". For how the pipeline works
internally see [how-it-works.md](how-it-works.md).

## Your workflow at a glance

| Scenario | What you do |
|---|---|
| **Research something new** (the comfortable way) | Open a Claude Code session **in this repo**, say: `/research <your problem or question>`. The agent runs the [research skill](../.claude/skills/research/SKILL.md): with `CONSENSUS_API_KEY` set it runs semantic discovery through the Consensus API itself and you just wait for the report; without a key it hands you 1-3 Consensus queries, you paste the results back, and it runs the pipeline. Either way it explains the report in plain language. |
| **Research something new** (hands-on, no agent) | With a key: `bin/verdict run --topic <topic> -q "<full question>"` — done, discovery included. Without: `bin/verdict web -q "<question>"` → run the question at consensus.app, scroll to the end, copy the list → `pbpaste \| bin/verdict resolve > dois.txt` → `bin/verdict run --topic <topic> --dois dois.txt -q "<full question>"` → read `runs/<topic>/<run>/report.md`. |
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
A and 1-3 natural-language queries for the Consensus side (mode B:
`run --queries`, or mode C's free web UI).

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

## Discovery: mode B (semantic, automatic) with an API key

Set `CONSENSUS_API_KEY` in `.env` (consensus.app → API & MCP dashboard) and
a bare `run -q` does semantic discovery by itself:

```bash
bin/verdict run --topic keratosis-uvb -q "is topical adapalene effective for keratosis pilaris?"
```

The Consensus API returns papers with DOIs, abstracts and study-design tags
ready — no title→DOI resolving, no paste. Recall still comes from
**reformulations**: run `bin/verdict pico -q "<question>"` and pass its 1-3
semantic queries:

```bash
bin/verdict run --topic keratosis-uvb -q "<full question>" \
    --queries "<reformulation 1>" --queries "<reformulation 2>"
```

Quota math: a request carries `page_size` (`CONSENSUS_PAGE_SIZE`, default
100) and the API charges one call per 100 papers a page returns, rounded up
(500 calls/mo on the Pro $12/mo tier). The walk stops at `CONSENSUS_PAGES`
(default 1) pages per query or at the ranking's end — whichever first — so
two queries at the defaults are up to 200 candidates for 2 calls (measured:
a second page of 100 adds almost nothing the review references miss). Tune per
run with `--consensus-pages`, globally with `CONSENSUS_PAGES` and
`CONSENSUS_PAGE_SIZE`. The plan caps the page size (Free 20, Pro and Teams
300, Deep 750); the log names the applied size when it is smaller than the
requested one, and a Free key gets 403 on pages after the first — that ends
the query's walk with a warning, not a failed run. Provenance (engine,
queries, pages, `api_calls`, applied `page_size`, truncation) is recorded in
`1-discovery.json` under `discovery`, and the run log prints the per-page and
total counts. A Consensus failure on the first page (bad key, quota) fails
the run loudly with the mode C escape hatch in the error — never a silent
keyword fallback.

Three channels feed one pool: Consensus, PubMed and the reference lists of
reviews. By default Consensus runs first and its pool is checked: PubMed
(the third channel) switches on only when that pool holds fewer than 2
systematic reviews or meta-analyses (by Consensus's tag, else by title) —
then the reference mining has little to work with and PubMed is the fallback;
with 2 or more it stays off and saves the screening of its ~350 extra
candidates. The decision and the review count are logged and saved in
`discovery.pubmed_decision`. `--pubmed on` forces PubMed, `--pubmed off`
disables it. The walk does not cut the pool by rank. The saved `discovery.pool`
holds every candidate with `sources`: every channel that found it, each with
its own rank (`{"source": "consensus", "query": 1, "rank": 12}`,
`{"source": "pubmed", "search": "rct_filter", "rank": 3}`,
`{"source": "review_refs", "citing_reviews": 2}`), so the recall of each
channel can be measured from the artifact. PubMed (free, E-utilities) costs one
`plan`-role call per run — the model writes the topic query of the question's
population and intervention (a specific named comparator too, never the
outcome): MeSH headings plus free-text synonyms, brand names, abbreviations
and truncation — and Go joins it and adds the Cochrane Highly Sensitive Search
Strategy for trials (300 hits) and a review filter (50 hits); the query, the
hit counts and the records without a DOI (kept in the pool for the audit, not
selectable) are in `discovery.pubmed`. The reference lists of up to 8
systematic reviews and meta-analyses of the pool (Consensus's and PubMed's)
are mined through OpenAlex (free) for further candidates, a cheap model (the
`plan` role, sonnet by default) screens the pool in batches of 50 — title,
year, tag and a short snippet — and the corpus for the critic is selected from
what passed: trials first, then other primary studies, then at most 3 reviews,
each group by the best rank over the channels (ties: the paper more channels
found), cut to `MAX_PAPERS` (default 40; `--max-papers` overrides). The
query-writing and screening calls appear in the `cost` record and the report
footer (`pubmed query`, `screening`); a failed batch is a warning and stays
unscreened (Consensus hits in, PubMed-only hits and mined references out). A
PubMed failure is a warning too: the other channels still deliver.
`--dois` and mode C runs skip the mining and the screening — the list is
respected as given. Design: docs/how-it-works.md §2.2.

Audit switches (`run` flags; the env equivalents set the default):

- `--discovery-sources consensus,pubmed,review_refs` (`DISCOVERY_SOURCES`) —
  any subset, taken literally (PubMed runs only when listed); measure one
  channel at a time. Unset: `consensus` and `review_refs`, PubMed by
  `--pubmed`. Without `consensus` the key is not needed.
- `--pubmed auto|on|off` (`PUBMED_DISCOVERY`) — the PubMed channel. `auto`
  (the default without a channel list) runs it when the Consensus pool holds
  fewer than 2 systematic reviews; `on` and `off` override that and a listed
  or unlisted `pubmed` in `--discovery-sources`.
- `--no-screen` (`NO_SCREEN=true`) — skip the LLM screening: every candidate
  with a DOI counts as included, the selection still runs. With
  `--stop-after 1-discovery` a run then spends no LLM call except the PubMed
  query writer.
- `--pubmed-query "<query>"` — a ready PubMed topic query (Boolean, with field
  tags, e.g. `("Gastroesophageal Reflux"[Mesh] OR GERD[tiab]) AND
  (fundoplicat*[tiab])`) instead of the model-written one: no writer call. The
  trial filter and the review filter are added to it as usual.

`NCBI_API_KEY` (optional, in `.env`) raises the PubMed rate limit from 3 to 10
requests a second; it is sent as `api_key` and never logged.

## Discovery: mode C (semantic, free) — the path without a key

No key, still semantic: you become the search engine's proxy.

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

Fallback — only when you explicitly opt out of semantic search:

- **Mode A**: `run -q "<2-4 keywords>"` — free, fully automatic,
  keyword-based. With `CONSENSUS_API_KEY` set, force it with
  `--engine openalex` (a bare `-q` would go through Consensus).

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
| `2-enrichment.json` | Scite tallies + retraction flags (OpenAlex **and** Crossref/Retraction Watch) + OA links + who contradicts (Scite key) + abstracts OpenAlex withholds (Europe PMC fallback, `abstract_source`) + `trial_registrations` (registration numbers from the abstract and the PubMed record; papers that share one are reports of one trial, pooled and counted once — `left_out` in the pooled estimate names the reports set aside) + `publication_types` (OpenAlex type, PubMed publication types: an erratum, comment, letter or other paper that is not a study report is never pooled or counted in the direction tally) |
| `3-critique.json` | per-paper LLM critiques (quality, relevance, direction of findings; for the extracted effect also `outcome_match`, `timepoint_weeks` / `timepoint_match`, `adjusted` and, for a cluster trial, `cluster`) |
| `4-synthesis.json` | the final report JSON; `pooled_estimate` carries `stratum` (`randomized` or `non-randomized`: the two are never pooled together) and `left_out`, the studies with numbers that were not pooled, each as `doi (reason)` |
| `report.md` / `matrix.csv` | human-readable report / matrix as a table (numbers assembled deterministically, never LLM-copied) |
| `context.md` | digest for `ask` and interactive agents |
| `fulltext/*.pdf` | paper PDFs: ones you drop in yourself (`<doi with / as _>.pdf`, always read in every mode — `verdict addpdf` reads the DOI from the PDF) plus OA PDF downloads in `full` mode or for the pivotal studies the hybrid pass deepens (also where an XML source supplies the full text: that text is what the critic reads, the PDF is saved besides it and not read; a failed download there is only logged) — kept for full-text critique + chart checks. The "Full texts not obtained" section of `report.md` lists the direct studies read from the abstract, each with the file name to save its PDF under; rerun with `--resume` |
| `3b-vision.json` | optional agent-written chart critique (via the vision tool, or the agent's own eyes in a Claude session); the synthesis stage weighs it |

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
--queries "<semantic q>" # mode B: repeatable discovery reformulations
                         #   (defaults to -q; feed from `verdict pico`)
--engine consensus       # force the engine: consensus | openalex
                         #   (default: consensus when CONSENSUS_API_KEY is set)
--consensus-pages 2      # max Consensus pages per query — a page of 100
                         #   papers is one API call (defaults to CONSENSUS_PAGES, 1)
--discovery-sources pubmed,review_refs
                         # mode B channels, any subset of consensus | pubmed |
                         #   review_refs, taken literally (defaults to
                         #   DISCOVERY_SOURCES; unset: consensus + review_refs)
--pubmed auto            # the PubMed channel: auto (when the Consensus pool has
                         #   fewer than 2 reviews) | on | off (PUBMED_DISCOVERY)
--no-screen              # skip the LLM screening: every candidate counts as
                         #   included, the selection still runs (or NO_SCREEN=true)
--pubmed-query "<q>"     # a ready PubMed topic query: skips the query-writing call
--exclude-dois <d1>,<d2> # DOIs the run must not see (repeatable, comma-separated): dropped from
                         #   every channel before the review mining, and from --dois; a Cochrane
                         #   DOI excludes all .pubN versions — for recall against a review
--max-papers 40          # papers sent to the critic after discovery's screening
                         #   and selection / DOI-list cap (defaults to MAX_PAPERS, 40)
--text-mode lean         # what the critic reads: lean | hybrid | full
                         #   (defaults to TEXT_MODE from env, hybrid).
                         #   lean — abstracts only; hybrid — abstracts, then
                         #   OA full texts for the pivotal studies, re-critiqued
                         #   before the single synthesis; full — OA
                         #   full texts for every paper. A PDF dropped into
                         #   fulltext/ is read in every mode.
--llm-command claude-max # LLM wrapper for this run, by name (never bare `claude`).
                         #   Order: this flag -> CLAUDE_WRAPPER (set by the
                         #   wrapper of the current session) -> LLM_COMMAND
                         #   in .env -> claude-max
--llm-model sonnet       # model alias for all roles (opus | sonnet | haiku |
                         #   fable; aliases resolve in every wrapper)
--model-critique <alias> # per-role overrides: cheap critic + strong judge
--model-deep <alias>     # the hybrid re-critique of the pivotal studies
--model-synthesis <alias>
--deep                   # the final verdict on fable instead of opus
--llm-chunk 15           # studies per critique call (LLM_CHUNK in .env; 1 =
                         #   one call per study). The per-call session
                         #   overhead dominates the cost — see Costs below.
--challenge-note "..."   # a counterargument the judge must weigh and answer
# bin/verdict addpdf ~/Downloads/paper.pdf --run-dir runs/<topic>/<run>
#   drops a downloaded PDF into the run; the DOI is read from the PDF itself
--llm-concurrency 4      # LLM stage parallelism
```

## Costs

| Component | Default | Paid option |
|---|---|---|
| Discovery | no key: mode C web paste / mode A keyword — $0 | mode B: Consensus API, 1 call per page of 100 papers — 2 queries × 1 page = 2 calls per run (Pro $12/mo, 500 calls/mo); plus the LLM screening, ~1 cheap call per 50 candidates |
| Scite | free (tallies without a key, 1 batched request per run) | Pro $50/mo — not needed at this volume |
| LLM | the Claude Code tariff | direct API ≈ $0.3–0.5/run (estimate) |

### Paid API backends (explicit only)

`LLM_BACKEND` empty means claude-cli; a key in `.env` never switches a run to
a paid API. Personal paid keys never go out directly:

- `LLM_BACKEND=anthropic` needs `ANTHROPIC_API_KEY`, `ANTHROPIC_PROXY` (HTTPS
  CONNECT proxy) and `ANTHROPIC_MODEL` (exact API model id, no default). The
  HTTP timeout is about 8 minutes (synthesis answers run up to 8000 tokens).
- `LLM_BACKEND=openai-compat` needs `OPENAI_BASE_URL`, `OPENAI_API_KEY` and
  `LLM_MODEL`. `OPENAI_PROXY` (HTTPS CONNECT proxy) is mandatory when the base
  URL is api.openai.com or api.groq.com (or a subdomain) — startup fails
  without it; for other endpoints (DeepSeek, OpenRouter, vLLM) it is optional
  and used whenever set.

### Quota economy: lean vs fat runs

The dominant cost is not the payload but the **per-call session overhead**:
every claude-cli call is a fresh session, and a default `-p` session loads
the whole Claude Code harness — ~74k input tokens per call measured
(2026-10-18), against 1–2k of useful critique payload. The pipeline attacks
this on three fronts:

- **Lean invocation**: each call runs with a custom `--system-prompt-file`
  (the built-in prompt, CLAUDE.md and memory auto-load are skipped),
  `--tools ""`, `--strict-mcp-config` with an empty config,
  `--disable-slash-commands` and `--safe-mode` (skips the user's hooks,
  plugins, CLAUDE.md, skills and MCP servers; the OAuth login stays). The
  payload becomes (almost) the whole prompt. The saving from `--tools ""` and
  `--safe-mode` is not measured live yet.
- **Batched critique**: one call assesses `LLM_CHUNK` (default 15) studies,
  each in its own JSON object, independently; studies a batch drops fall
  back to single-study calls. 40 papers = 3 calls instead of 40. Measured on
  77 abstracts (4 topics): batches of 15 agree with batches of 5 as closely as
  two runs at 5 agree with each other, with half the calls and 15-20% fewer
  input tokens.
- **Per-role models** inside one wrapper: `opus` at low effort for the
  abstract critique, `opus` at high effort for the judge call and for the
  hybrid re-critique of pivotal studies (`--deep`: `fable` for the judge). Defaults per role are in the README (LLM backends); override with
  `LLM_MODEL_PLAN`, `LLM_MODEL_CRITIQUE`, `LLM_MODEL_DEEP`,
  `LLM_MODEL_SYNTHESIS`, `LLM_MODEL_ASK` (`LLM_MODEL` / `--llm-model` for all
  roles). A cheaper critic (`sonnet`, `haiku`) only after comparing it with
  `opus` on the benchmark (`verdict eval`, see Evaluation).

Two more levers cut the bill without touching quality:

- **Critique cache** (`runs/<topic>/critique-cache.json`): a critique is a
  pure function of (study data, question), so challenge re-runs with the
  question verbatim and crash resumes reuse it — a challenge re-run costs 1
  synthesis call. `CRITIQUE_CACHE_TTL_DAYS` (30; 0 disables) bounds the
  staleness tradeoff.
- **Judge payload diet**: the synthesis call re-reads an abstract only for
  `direct` studies (and studies whose critique failed); matrix notes are
  required for direct studies only — the pipeline auto-notes the rest from
  the critique concerns.

A measured fat run (2026-10-11, pre-optimization): 3 queries × 3 pages →
113 papers × full-text critique ≈ 115 calls — millions of tokens, a
weekly-quota hazard. The lean profile is the **default for agent-launched
runs** unless the user explicitly asks for depth:

```bash
bin/verdict run --topic <t> -q "<question>" --queries "<q1>" --queries "<q2>" \
    --max-papers 40 --text-mode lean
```

- Consensus calls are not the constraint (the Pro plan has 500 calls/month):
  do not trim queries to save them. The LLM calls are the cost —
  the screening (~1 call per 50 candidates), the critique (~1 call per 15
  papers: ~3 calls at 40 papers) and the synthesis.
- The two cost multipliers remain **corpus size** and **full text**:
  `MAX_PAPERS` (default 40) truncates the corpus sent to the critic hard (it
  used to be a warning-only soft cap — the 113-paper run slipped through it);
  `--text-mode full` multiplies prompt size 5–10× and is for deep single
  questions by deliberate choice only. The default `hybrid` adds one
  deepening pass at the end of the critique stage (OA full-text fetch — Europe
  PMC XML, PubMed Central XML, the PDF from every known location, then the
  Jina Reader — for up to `HYBRID_PIVOTAL_MAX`, default 7, pivotal
  studies, direct ones first (then by design quality and sample size) and
  indirect ones in the free slots; a study whose text cannot be obtained
  passes its slot to the next candidate, within three fetch attempts per
  slot; + ~1 deep re-critique call over up to 7 full texts on top of lean);
  the synthesis still runs once, on the final critiques.
  The Jina Reader is a third-party service that sees the addresses of the
  papers it reads (no personal data); `JINA_API_KEY` in `.env` (optional, a
  free key) lifts its anonymous limit of 20 requests a minute to 500. The log
  has one line per study ("full text obtained" / "full text not obtained") with
  the reason each source was skipped.
  A full text is stored up to 60,000 characters and the critic reads the
  first 30,000 of them (tables first), so a batch of seven deepened studies
  carries up to ~210,000 characters (~52,500 tokens) of full text.
  `--resume` of a run whose critique artifact predates the pass deepens
  before the synthesis; once `4-synthesis.json` exists the pass is not
  repeated.
- With batching + lean invocation a 50-paper corpus is back in the
  affordable range — the marginal cost per extra paper is its abstract, not
  a fresh 74k-token session.
- Every run records the actual spend in the artifacts (`cost` in the stage
  JSONs, an "LLM usage" line at the bottom of `report.md`).

### Known failure mode: hollow "insufficient"

If a synthesis-stage JSON retry drops the study payload, the report lands a
hollow `insufficient` whose rationale literally says *"no per-study input data
was received"*. The critiques are intact. Fix for 1 LLM call: delete
`4-synthesis.json`, `report.md`, `matrix.csv` in the run dir and
`run --run-dir runs/<...> --resume` — synthesis re-runs on the kept critiques.

## Evaluation

`verdict eval` measures how well verdict's judgment matches the expert
conclusions of Cochrane reviews (the MedEvidence benchmark; the question set
is `evals/medevidence.yaml`, the header there credits the dataset). For every
question the normal pipeline runs on exactly the studies the review used (a
DOI list, no search) with the question's claim as the research question; the
outcome is then compared with the expert answer. The prompts are not changed:
the eval measures verdict as users run it.

Both sides are reduced to one of four outcomes, relative to the claim:

| Outcome | Expert answer | verdict's report |
|---|---|---|
| `works` | the same direction as the claim (`works_if`) | the report's `effect_direction` is `supports` |
| `opposite` | the other direction | `effect_direction` is `opposite` |
| `no_effect` | `no difference` | `effect_direction` is `no_effect` |
| `not_proven` | `uncertain effect`, `insufficient data` | `effect_direction` is `unclear`, or the verdict level is `insufficient` |

The report's own `effect_direction` decides; the verdict level `insufficient`
comes first and always gives `not_proven`. The summary shows it in the
`Effect direction` column.

A report without `effect_direction` (an artifact written before the field
existed) keeps the older rule: a consensus status of `insufficient_data`,
`contested` or `no_consensus` gives `not_proven` (verdict says the direction
of the evidence cannot be stated or the studies disagree); with
`broad_agreement`, `emerging`, or a status that is empty or not one of the
five values (shown as `unknown` in the summary) the majority of the matrix
rows with relevance `direct` decides — strictly more `positive` than any
other direction is `works`, `negative` is `opposite`, `null` is `no_effect`;
a tie, a majority of `mixed` / `unclear` studies or no direct studies is
`not_proven`. The summary shows the consensus status in the `Consensus
status` column next to the direct-study tally. The strength of the evidence is scored separately:
the expert certainty maps to a verdict level (high → strong, moderate →
moderate, low → weak, very low → insufficient); the summary reports exact
matches and matches within one level, and skips questions without a
certainty.

```bash
bin/verdict eval                              # all questions, hybrid text mode
bin/verdict eval --ids 10,30 --repeat 3       # chosen questions, three runs each
bin/verdict eval --text-mode lean --llm-model sonnet --out runs/eval-lean
bin/verdict eval --search --ids 10,30         # discovery finds the studies, recall is measured
```

Flags: `--set` (default `evals/medevidence.yaml`), `--ids`, `--repeat`,
`--out` (default `runs/eval-<YYYYMMDD-HHMMSS>`), `--text-mode` (default
`hybrid`), `--search` (below) and the model flags of `run` (`--llm-command`,
`--llm-model`, `--model-critique`, `--model-deep`, `--model-synthesis`,
`--deep`).

### Search mode (`--search`)

By default the studies are given. With `--search` every question instead goes
through the normal semantic discovery (mode B), as a user's run would:

- The two Consensus queries are the question's `question` text and its `claim`
  text; `verdict pico` is not called.
- The source review is excluded automatically: the question's `review_doi` is
  passed as the run's exclude DOIs (the same mechanism as
  `verdict run --exclude-dois`; a Cochrane DOI excludes every version of its CD
  number). Without it, mining the review's own reference list would hand the
  answer back. Every question of the set needs a `review_doi`.
- Everything else is as in a default `verdict run`: Consensus, the mining of
  review reference lists, PubMed on `auto`, the screening, the `MAX_PAPERS`
  selection, the text mode of the eval flag. The scoring of outcome and
  certainty is unchanged.

It needs `CONSENSUS_API_KEY` and spends the Consensus quota: 2 queries × 1 page
(`CONSENSUS_PAGES`, default 1) per question, per run, so about two calls with
the default page size. The LLM spend grows too: the screening calls of the
discovery (one cheap call per 50 pool candidates) come on top of the critique
and synthesis calls; the figures in the summary include them.

The summary adds the search recall per run: of the review's studies that have a
DOI (the DOIs are compared normalized: case and a `https://doi.org/` prefix do
not matter), how many are found anywhere in the discovery candidate pool, how
many the screening kept (an unscreened Consensus hit stays in the selection, so
it counts as kept) and how many are in the corpus given to the critic. They are
the columns `Recall pool`, `Recall screened` and `Recall corpus` (`found/total`),
which take the place of `Studies given`, and the totals add the pooled recall
over all scored runs: the sum of found studies over the sum of the review's
studies, with percentages. The figures are computed from the run's artifacts
(`1-discovery.json`: `discovery.pool` and the corpus `papers`) after the run, so
they cost no extra call. In the default mode the summary is unchanged.

The mode is recorded in `<out>/<id>-<n>/mode` (a directory made before the
marker existed counts as the DOI-list mode when it holds `dois.txt`). Reusing an
`--out` in the other mode stops at once with an error and leaves the earlier
summary alone: a run of one mode must not be scored as the other.

Every run lives in `<out>/<id>-<n>/run/` with its own caches, so repeats never
reuse each other's critiques; `<out>/summary.md` and `<out>/summary.csv` list
every run (expected and actual outcome, the direct-study tally, strength,
studies given versus studies that reached the report, LLM calls and tokens)
and the totals, including the expected-by-actual outcome table and, with
`--repeat` above 1, how many questions gave the same outcome every time.

Questions run one at a time. A usage limit stops the eval and writes the
summary of the finished runs; after the limit resets, rerun the same command
with the same `--out`: finished runs are loaded from disk, an interrupted one
resumes, the rest run. A failed question is recorded in the summary and the
eval goes on. `runs/` is not committed.

Cost: one hybrid run per question is about 4–6 LLM calls (batched critique,
the deep re-critique of the pivotal studies, one synthesis); `--repeat` and
the number of questions multiply it. `--text-mode lean` is cheaper. Plan the
run against the usage limit of the wrapper before starting it.

## verdictd (the HTTP wrapper)

`bin/verdictd --addr localhost:8080` exposes the same pipeline over HTTP —
handy for driving runs from other tools. One run executes at a time (the
LLM concurrency budget lives inside the stages); artifacts land in `runs/`
exactly as with the CLI. Mode B discovery works here too (the key comes
from `.env`); the engine/queries selection is not yet exposed over the
request body — a follow-up.

Jobs live in SQLite (`runs/verdict.db`, the same file as the run index), so
a client can read the outcome of a run after the daemon was restarted. A
job is `queued`, `running`, `done`, `failed` or `interrupted`. On `SIGINT` /
`SIGTERM` the daemon stops accepting requests, cancels the running job and
marks it `interrupted`; at the next start every job still `queued` or
`running` is marked `interrupted` too. Interrupted jobs are not re-queued —
a run costs LLM calls, so repeating it is the client's decision (the run
directory keeps its checkpoints: `verdict run --run-dir runs/<...> --resume`
continues it for free). The request body is capped at 1 MiB; a failed job
reports a general message, the details are in the daemon log.

```
POST /runs   {"question": "...", "topic": "...", "max_papers": 15,
              "text_mode": "hybrid", "stop_after": "2"}     # or "dois": [...]
GET  /runs                                                # jobs with status
GET  /runs/{id}                                           # one job + verdict
GET  /runs/{id}/{artifact}                                # report.md, context.md, N-*.json, ...
```

## When something breaks

- **The pipeline died midway** → `run --run-dir runs/<...> --resume`.
- **Usage limit reached** (`claude-max`: the 5-hour or weekly Max limit) →
  the run stops at once; the finished critiques stay in the critique cache.
  Wait for the limit to reset, then `run --run-dir runs/<...> --resume`.
  A login failure stops the run the same way (log in again first).
- **Transient LLM errors** (429, 529, overloaded, 5xx, connection) → retried
  up to 3 attempts with 20 s and 60 s pauses; a call timeout is not retried
  (raise `LLM_TIMEOUT`, a Go duration such as `15m`; a bare number is
  rejected).
- **`claude-zai` reported a corporate gateway block** → specific to that
  wrapper: its first request blocks the gateway for hours (the one-way
  cooldown); wait out the timer, re-login does not help. The run logs a
  warning when `claude-zai` comes from `.env` or the flag.
- **A startup error about the bare `claude` command** → the Claude Code CLI is
  only used through a wrapper by name (`claude-max`, `claude-zai*`,
  `claude-ds`); fix `--llm-command`, `LLM_COMMAND` or `CLAUDE_WRAPPER`.
- **LLM answers are garbage/empty** → the wrapper is run directly when it is
  in `PATH`, otherwise through `zsh -ic`: in the second case make sure
  `zsh -ic 'echo MARKER' 2>/dev/null` prints nothing but MARKER (junk from
  ~/.zshrc leaks into the answer).
- **Scite is silent about recent papers** → not an error: "no data", and
  that is what lands in the report.
