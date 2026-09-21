# verdict

An orchestrator for evidence assessment of scientific claims. It answers the
question: **"How well-proven is this claim, and by which specific papers?"**

A pipeline of ready-made services with a thin Go orchestrator on top:

```
             ┌───────────┐   ┌───────────────────┐   ┌─────────────────────┐
 question ──>│ Consensus │──>│ Scite (tallies)   │──>│ LLM (pluggable      │──> report.md
 or          │ semantic  │   │ + OpenAlex        │   │ backend, see below) │    matrix.csv
 a DOI list  │ (web/MCP) │   │ (retractions, OA, │   │ map: per-paper      │    verdict
             └───────────┘   │ abstracts)        │   │ methodology critique│
                             └───────────────────┘   │ reduce: evidence    │
                                                     │ matrix + verdict    │
                                                     └─────────────────────┘
```

This is a **standalone CLI tool**: it runs in a terminal on its own and is
not tied to any IDE or agent session. All artifacts are files on disk.

## Three ways to use it

**Short version here, the full guide in [docs/usage.md](docs/usage.md)**
(discovery modes, artifacts, flags, costs, troubleshooting).

1. **Plain CLI** — `verdict run`, the report lands in `report.md` (plus a
   `context.md` digest and `matrix.csv`). Read it, archive it, move on.
2. **CLI + analyst Q&A** — `verdict ask --run-dir runs/<...>`: an interactive
   session over a finished run (papers, citation tallies, critiques and the
   verdict loaded into context), using the same LLM backend as the pipeline.
   One-shot: `ask --run-dir ... -q "..."`.
3. **Everything inside a Claude Code / agent session** — the agent runs
   `verdict run` itself, reads `runs/<...>/context.md` (a purpose-built
   digest for agents) and answers questions in chat: the whole
   research-and-ask loop in one session.

A plain-language explanation of the design (what each service does, what
retractions and smart citations are, how to read the report) lives in
[docs/how-it-works.md](docs/how-it-works.md).

## LLM backends (no separate tokens required)

The analytical stages (critique + synthesis) go through a pluggable
`internal/llm.Completer` backend. The choice is env `LLM_BACKEND` (auto by
default) or the `--llm-command` / `--llm-model` flags per run.

| Backend | When | What it needs |
|---|---|---|
| **claude-cli** (default here) | Spend existing Claude Code subscriptions — zero new costs | A wrapper from `~/.zshrc` (`LLM_COMMAND`), optionally `LLM_MODEL` as an exact name |
| **anthropic** | Personal API keys appear | `ANTHROPIC_API_KEY` |
| **openai-compat** | Migration to cheap personal subscriptions: OpenRouter, DeepSeek, vLLM, Together | `OPENAI_BASE_URL`, `OPENAI_API_KEY`, `LLM_MODEL` |

The claude-cli backend shells out to `<wrapper> -p --output-format text`
with the prompt on stdin (independent of the CLI's argument quirks). The
result is parsed as JSON with one retry. Backend autodetection: with no API
keys configured, claude-cli is selected.

### claude-cli: how it works and its constraints

Every LLM call is a separate `zsh -ic '<wrapper> -p --output-format text'`
process, prompt on stdin, flags after the prompt. Constraints that
shaped the defaults:

- **One-way cooldown:** the first request through `claude-zai` blocks the
  corporate gateway for hours. Choosing the wrapper is a deliberate
  per-run decision: burn the personal plan's allowance when corporate models are not
  needed, otherwise `--llm-command claude` (or another corporate wrapper).
- **Exact model names only** (`LLM_MODEL`): aliases do not resolve and
  silently fall back to the wrapper default. Empty = wrapper default.

## Discovery: three modes

**A — free, fully automatic:**

```bash
verdict run -q "does creatine improve cognitive performance"
```

Discovery via OpenAlex: works out of the box, zero keys. Caveat: keyword
search, not semantic — it can miss on convoluted questions.

**B — paid, fully automatic (Consensus API/MCP — a roadmap item, not yet
wired into `run`):**

Consensus Pro ($12/mo) includes 500 API & MCP uses per month. Until the
integration lands, mode B is unavailable: use mode C (the default) or
mode A. The previous paid engine was removed from the codebase entirely —
the criteria and the comparison live in
[docs/how-it-works.md](docs/how-it-works.md) §2.2.

**C — free bridge over the Consensus web UI (manual selection, the
default):**

```bash
verdict web -q "..."                  # prints the question, opens consensus.app
# in the browser: run the question, scroll the list to the end (~50-row
# cap), copy the whole thing — a raw paste is fine
pbpaste | verdict resolve > dois.txt  # titles-only paste → DOIs (or `dois`
                                      # when the paste/CSV carries DOIs)
verdict run --dois dois.txt -q "..."  # the rest of the pipeline as usual
```

Consensus-grade semantic selection for $0: you decide which papers to
take. Recall trick: the UI caps one list at ~50 rows, so run 2-3
reformulations of the question instead of trying to scroll deeper.

## Stages and artifacts

Every stage writes a full checkpoint into the run directory
(`runs/<slug>-<time>/`); a failed run resumes with `--resume` without
paying twice.

| Artifact | Stage | Contents |
|---|---|---|
| `0-input.txt` | always (`--dois`) | the raw paste/DOI list the corpus came from |
| `1-discovery.json` | Consensus paste resolved to DOIs (or OpenAlex over a DOI list) | papers: DOIs, abstracts, design tags |
| `2-enrichment.json` | Scite tallies + OpenAlex + Crossref (in parallel) | supporting/contradicting/mentioning, retraction flags (two sources), OA links, contradicting DOIs (Scite key) |
| `3-critique.json` | LLM, fan-out per paper | design quality, sample adequacy, N from the abstract, direction of findings, concerns |
| `4-synthesis.json` + `report.md` + `matrix.csv` | LLM + deterministic assembly | evidence matrix, verdict + consensus + direction tally, contradictions, gaps |
| `context.md` | always | compact digest for `ask` and interactive agents |

Failure isolation: one source failing for one paper does not kill the run —
it lands in `Errors`/`Warnings` and synthesis proceeds on partial data.

## Quick start

```bash
cp .env.example .env   # zero keys needed: auto backend = claude-cli wrapper
make build             # or: go build -o bin/verdict ./cmd/verdict

make run QUESTION="does creatine improve cognitive performance"
# verdict: moderate — report in runs/<slug>/report.md
```

## Costs (per average run: 15 papers)

| Component | Default | Paid option |
|---|---|---|
| Discovery | OpenAlex — $0 | Consensus Pro $12/mo (API & MCP, 500 uses/mo — integration is a roadmap item) |
| Citations | $0 (tallies without a key, 1 batched request per run) | Scite Pro $50/mo — not needed at this volume |
| Metadata/retractions | OpenAlex — $0 | — |
| LLM | existing Claude Code tariff | direct API ≈ $0.3–0.5/run (a Sonnet-class model, estimate) |

## Verified live (2026-09-07)

- `internal/scite`: the `/tallies` request body must be a **bare JSON array**
  of DOIs (`{"dois": [...]}` is rejected with 400); the response uses the
  `{"tallies": {...}}` envelope — the parser handles all known variants.
- OpenAlex search rejects `*` and `?` in queries (wildcards) — natural
  questions ending in "?" are stripped before sending.
- The claude-cli backend parses model answers through `~/.zshrc` cleanly.
- Crossref: a retracted work carries `updated-by` entries with
  `type: "retraction"` and `source: "retraction-watch"` (checked live on the
  Wakefield DOI); corrections arrive the same way with `type: "correction"`.
  Both power the enrichment-stage integrity check.
- Semantic Scholar: the Graph API batch endpoint works keyless (one request
  per run); it supplies TLDRs, abstracts OpenAlex misses, influential-citation
  counts and OA PDF links.
- PubPeer: the v3 API exists but **requires a manually issued developer
  key** (POST `/v3/publications`, field `dois` + `devkey`; response shape
  `{feedbacks: [{id, total_comments, url, last_commented_at}]}` per the
  official Zotero plugin source — counts and links, no comment bodies). A
  devkey was requested from the team on 2026-09-08; whether the check is
  worth it gets decided by their reply (comment contents or count only).
  The dormant client stays behind an empty `PUBPEER_DEVKEY` meanwhile.

The discovery engine was replaced on 2026-09-08 after a measured bake-off
(KP-treatment ground truth); the full criteria table lives in
docs/how-it-works.md §2.2.

## Design decisions

- **A CLI, not a microservice:** the job is batch-oriented and needs a human
  eye on intermediate artifacts. `internal/pipeline` is transport-agnostic —
  it can be wrapped in a worker/HTTP service later without a rewrite.
- **LLM as an interface (`Completer`):** corporate tariffs via claude-cli
  today, personal keys or OpenRouter tomorrow — an env change, not a code
  change. Prompts are provider-independent.
- **Map-reduce instead of a single LLM pass:** per-paper critiques run in
  parallel goroutines (errgroup + a dedicated LLM rate limit), the final
  call only aggregates — more stable, every critique visible on its own.
- **A GRADE-style rubric for the verdict**, not vibes: strong/moderate/weak/
  insufficient definitions are baked into the synthesis prompt, numbers are
  copied from the data verbatim. The consensus field is separate: the
  verdict says how *proven* the claim is, the consensus says *where the
  field leans*.
- **Deterministic checks stay out of the LLM:** retractions come from
  OpenAlex `is_retracted` plus Crossref's Retraction Watch notices (and
  corrections land as notes), not from a model's opinion. The evidence
  matrix is assembled in Go from the stage data — numbers never pass
  through the model.
- **Scite sobriety:** `tally == nil` ≠ confirmation; the rule "absence of
  contradictions is not proof" is baked into the judge prompt.

## Roadmap

v1–v3 are all implemented in this codebase:

- **v1**: discovery → enrichment → critique → synthesis; Crossref/Retraction
  Watch as the second retraction source; contradicting-citation drill-down
  (behind a Scite key); per-stage models; `resolve` + `snowball`; challenge
  notes; Semantic Scholar as a second metadata source; PubPeer checks
  (behind a devkey); direction-of-findings + author-group independence.
- **v2 (partially superseded)**: sample sizes come from abstracts (the
  extraction-session feature left with the retired discovery engine); full
  texts (full text on by default — OA download,
  text critique); chart checks through the agent's vision MCP
  (`3b-vision.json` consumed by synthesis). PubPeer — devkey requested,
  decision pending their reply (see the live-verification notes).
- **v3**: `verdictd` — the HTTP wrapper (queue + artifacts API,
  `cmd/verdictd`); the SQLite run index (`runs/verdict.db`, `verdict
  index`); the per-topic enrichment cache; PICO normalization (`verdict
  pico`).
