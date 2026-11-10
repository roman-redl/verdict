# verdict

An orchestrator for evidence assessment of scientific claims. It answers the
question: **"How well-proven is this claim, and by which specific papers?"**

A pipeline of ready-made services with a thin Go orchestrator on top:

```
             ┌───────────┐   ┌───────────────────┐   ┌─────────────────────┐
 question ──>│ Consensus │──>│ Scite (tallies)   │──>│ LLM (pluggable      │──> report.md
 or          │ semantic  │   │ + OpenAlex        │   │ backend, see below) │    matrix.csv
 a DOI list  │ (API/web) │   │ (retractions, OA, │   │ map: per-paper      │    verdict
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

`verdict eval` scores the verdicts against the expert conclusions of Cochrane
reviews (MedEvidence benchmark) — see the "Evaluation" section of
[docs/usage.md](docs/usage.md).

## LLM backends (no separate tokens required)

The analytical stages (critique + synthesis) go through a pluggable
`pipeline.Completer` backend (implemented in `internal/llm`). The choice is env `LLM_BACKEND` (empty =
claude-cli) or the `--llm-command` / `--llm-model` flags per run.

| Backend | When | What it needs |
|---|---|---|
| **claude-cli** (default) | Spend existing Claude Code subscriptions — zero new costs | A Claude Code wrapper by name (see below); models are aliases (`opus`, `sonnet`, `haiku`, `fable`) |
| **anthropic** | Max API credits (`LLM_BACKEND=anthropic`) | `ANTHROPIC_API_KEY`, `ANTHROPIC_PROXY` (HTTPS CONNECT proxy — direct calls are forbidden), `ANTHROPIC_MODEL` (exact API model id, no default) |
| **openai-compat** | OpenAI, Groq, OpenRouter, DeepSeek, vLLM, Together (`LLM_BACKEND=openai-compat`) | `OPENAI_BASE_URL`, `OPENAI_API_KEY`, `LLM_MODEL`; `OPENAI_PROXY` (HTTPS CONNECT proxy) — mandatory for OpenAI and Groq |

`LLM_BACKEND` empty always means claude-cli: a key in `.env` never silently
switches a run to a paid API — `anthropic` and `openai-compat` are chosen
only explicitly. Personal paid API keys (Anthropic, OpenAI, Groq) never go
out directly. The anthropic backend's base address is pinned to
api.anthropic.com in code, and its HTTP timeout is about 8 minutes
(synthesis answers run up to 8000 tokens). For openai-compat, `OPENAI_PROXY`
is mandatory when `OPENAI_BASE_URL` points at api.openai.com or api.groq.com
(or their subdomains) — startup fails without it; for other endpoints
(DeepSeek, OpenRouter, vLLM) it is optional and used whenever set.

The claude-cli backend runs `<wrapper> -p --output-format json` with the
prompt on stdin (independent of the CLI's argument quirks); the JSON
envelope carries the usage numbers, the model's answer is parsed as JSON
with one retry. When the model wraps the answer in prose or code fences, the
extractor strips the fences and takes the first `{` up to the last `}`.

### claude-cli: how it works and its constraints

**Wrapper resolution** (first match wins):

1. the `--llm-command` flag;
2. `CLAUDE_WRAPPER` — the label every wrapper exports into its session, so a
   run launched from an agent session uses that session's wrapper;
3. `LLM_COMMAND` from `.env` — for runs outside a session (terminal, `verdictd`);
4. `claude-max`.

The resolved wrapper, its source, the role, the model and the effort are
logged. The Claude Code CLI is never invoked bare (`claude`, `claude -p`):
only through a wrapper by name (`claude-max`, `claude-zai*`, `claude-ds`),
because the wrapper sets the account, the gateway and the proxy. The code
rejects `LLM_COMMAND=claude`, `--llm-command claude` and
`CLAUDE_WRAPPER=claude` at startup.

**Execution.** The CLI is executed directly when the wrapper is found in
`PATH`, otherwise through `zsh -ic` (wrappers defined as zsh functions;
agent shells may lack `~/.local/bin` in `PATH`). The prompt goes on stdin;
the session is lean and isolated:

```
<wrapper> -p --output-format json --system-prompt-file <tmp> --tools ""
    --strict-mcp-config --mcp-config '{"mcpServers":{}}'
    --disable-slash-commands --no-session-persistence --safe-mode
    [--model M] [--effort E] [--fallback-model sonnet]
```

`--safe-mode` skips the user's hooks, plugins, CLAUDE.md, skills and MCP
servers but keeps the OAuth login and model selection (`--bare` is not an
option: it disables OAuth). The saving from `--tools ""` and `--safe-mode`
is not measured live yet.

**Roles and models.** Models are aliases that resolve in every wrapper (the
GLM and DeepSeek wrappers map them to their gateway models); exact model ids
are not used. Each stage has a role with a default model:

| Role | Stage | Default | Effort (`claude-max` only) |
|---|---|---|---|
| `plan` | `pico`, `brief` (question parsing, search plan) | `sonnet` | low |
| `critique` | per-study critique on abstracts | `opus` | low |
| `deep` | hybrid re-critique of pivotal studies with full texts | `opus` | high |
| `synthesis` | the final verdict | `opus` (`fable` with `run --deep`) | high |
| `ask` | `verdict ask` | `sonnet` | medium |

Overrides, most specific first: role flag (`--model-critique`,
`--model-deep`, `--model-synthesis`) → role env (`LLM_MODEL_PLAN`, `LLM_MODEL_CRITIQUE`,
`LLM_MODEL_DEEP`, `LLM_MODEL_SYNTHESIS`, `LLM_MODEL_ASK`) → `--llm-model` →
`LLM_MODEL` (all roles) → the role default. A cheap critic is a role model
inside one wrapper, not a separate wrapper. Effort, and
`--fallback-model sonnet` for the `opus`/`fable` roles, are passed only with
`claude-max`; the GLM and DeepSeek gateways disable adaptive thinking. A
cheaper critic (`sonnet`, `haiku`) only after comparing it with `opus` on the
benchmark (`verdict eval`).

Constraints that shaped the defaults:

- **Every call spends a limit:** `claude-max` calls count against the Max
  5-hour and weekly limits, corporate wrappers against the corporate
  gateway. Choosing the wrapper is a deliberate per-run decision.
- **`claude-zai` one-way cooldown:** its first request blocks the corporate
  gateway for hours. The warning is logged only when `claude-zai` comes from
  `.env` or the flag, not when the run is launched from a `claude-zai`
  session.
- **Failures are classified.** An exhausted usage limit or a login failure
  stops the run at once (no per-study fallback calls); the finished
  critiques stay in the critique cache, and `--resume` continues after the
  limit resets. Transient errors (429, 529, overloaded, 5xx, connection) are
  retried up to 3 attempts with 20 s and 60 s pauses; a call timeout is not
  retried. `LLM_TIMEOUT` is a Go duration (`10m`; a bare `600` is rejected).

## Discovery: three modes

**A — free, fully automatic:**

```bash
verdict run -q "does creatine improve cognitive performance"
verdict run -q "..." --engine openalex   # force it even with a key set
```

Discovery via OpenAlex: works out of the box, zero keys. Caveat: keyword
search, not semantic — it can miss on convoluted questions. Without
`CONSENSUS_API_KEY` this is also what a bare `run -q` falls back to (with a
warning).

**B — paid, fully automatic (Consensus API — the default with a key):**

```bash
# .env: CONSENSUS_API_KEY=...   (consensus.app → API & MCP dashboard)
verdict run -q "does creatine improve cognitive performance"
verdict run -q "..." --queries "<reformulation 1>" --queries "<reformulation 2>"
```

Semantic discovery through the Consensus API, straight into the pipeline:
DOIs, abstracts and study-design tags arrive ready (no title→DOI resolving).
A page is `CONSENSUS_PAGE_SIZE` papers (default 100) and costs one API call
per 100 papers; the walk is capped at `CONSENSUS_PAGES` (default 1) per
query — 2 calls for two queries (the Pro tier, $12/mo, includes 500/mo).
Reformulations still matter: 2-3 `--queries` (from `verdict pico`) recover
the recall. PubMed (free) is searched when the Consensus pool holds fewer than
2 systematic reviews (`--pubmed on|off` overrides), with a model-written MeSH
query and the Cochrane trial filter. The whole candidate pool is kept, each paper
with every channel that found it; the reference lists of its systematic
reviews are mined for more trials, a cheap model screens the pool, and the
best `MAX_PAPERS` (default 40) go to the critic. Provenance — engine, queries,
pages, API calls, the pool with its per-channel ranks and screening
decisions — lands in `1-discovery.json` under `discovery`.

**C — free bridge over the Consensus web UI (manual selection — the
semantic path without a key):**

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
| `1-discovery.json` | Consensus API / paste resolved to DOIs (or OpenAlex) | papers: DOIs, abstracts, design tags; the `discovery` block: engine, queries, pages spent, truncation |
| `2-enrichment.json` | Scite tallies + OpenAlex + Crossref (in parallel), then Europe PMC for the abstracts still missing | supporting/contradicting/mentioning, retraction flags (two sources), OA links, abstracts (`abstract_source` marks the Europe PMC ones), contradicting DOIs (Scite key) |
| `3-critique.json` | LLM, fan-out per paper | design quality, sample adequacy, N from the abstract, direction of findings, the extracted effect numbers (`effect`), concerns |
| `4-synthesis.json` + `report.md` + `matrix.csv` | pooling in Go, then LLM + deterministic assembly | the pooled estimate (`pooled_estimate`), evidence matrix, verdict + effect direction + consensus + direction tally, contradictions, gaps |
| `context.md` | always | compact digest for `ask` and interactive agents |

Failure isolation: one source failing for one paper does not kill the run —
it lands in `Errors`/`Warnings` and synthesis proceeds on partial data.

## Quick start

```bash
cp .env.example .env   # zero keys needed: default backend = claude-cli
make build             # or: go build -o bin/verdict ./cmd/verdict

make run QUESTION="does creatine improve cognitive performance"
# verdict: moderate — report in runs/<slug>/report.md
```

## Architecture

```
cmd/verdict, cmd/verdictd   flags, wiring, startup; one file per CLI command
cmd/internal/app            config -> clients (the only reader of internal/config besides the mains)
internal/pipeline           the domain: stages, state, artifacts, report rendering;
                            depends only on the ports it declares (ports.go)
internal/openalex, crossref, scite, s2, europepmc, pmc, jina, doi, pubpeer, consensus, pdf, llm
                            adapters: each implements a pipeline port over an external service
internal/jats               JATS XML -> plain text (shared by the europepmc and pmc adapters)
internal/httpx              the shared HTTP client: timeout, retries, Retry-After, size cap
internal/db                 SQLite: numbered migrations, run index, daemon jobs
internal/server             the verdictd HTTP API and its job worker
internal/model              data types shared by the pipeline and the adapters
internal/config             the environment (caarlos0/env), validated
```

The layer boundaries are enforced by depguard (see `.golangci.yml`): the
pipeline imports no HTTP, database or process packages, and only `cmd/*` may
read the config.

## Development

```bash
make check   # build + vet + test (-race) + golangci-lint + govulncheck
make tidy    # go mod tidy
```

`make check` needs golangci-lint v2.15.0 or newer (older releases cannot
typecheck against Go 1.27.2+). Tests use handwritten fakes and `httptest`:
no network, no LLM calls. Golden files in `internal/pipeline/testdata` are
refreshed with `go test ./internal/pipeline -update`. Logging is `slog`;
`LOG_LEVEL` and `LOG_FORMAT` (`text` | `json`) tune it.

## Costs (per average run: 15 papers)

| Component | Default | Paid option |
|---|---|---|
| Discovery | mode B: Consensus API — ~4 calls per run (500/mo on Pro $12/mo); no key → mode C paste / mode A, $0 | — |
| Citations | $0 (tallies without a key, 1 batched request per run) | Scite Pro $50/mo — not needed at this volume |
| Metadata/retractions | OpenAlex — $0 (abstract fallback: Europe PMC — $0, no key) | — |
| LLM | existing Claude Code tariff | direct API ≈ $0.3–0.5/run (a Sonnet-class model, estimate) |

## Verified live (2026-09-07)

- `internal/consensus`: `/v1/search` — `x-api-key` auth, the
  `page`/`page_size`/`is_end`/`next_page` envelope (checked 2026-10-10;
  page 2 of a live query walked correctly). `page_size` in the response is
  the value the plan actually applied, not the requested one — the walker
  counts pages, never assumes 20.
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
- Europe PMC (checked 2026-11-06): `/search?query=DOI:"a" OR DOI:"b"&resultType=core&format=json`
  returns `resultList.result[]` with `doi`, `source` (`MED`, `PMC`, `PPR`
  preprints) and `abstractText` — HTML (`<h4>` section headings, `<i>`), with
  bare `<` characters (`<10 y`, `P<.001`) left unescaped, so the stripper only
  removes well-formed tags. It has the abstracts OpenAlex withholds for
  publisher-restricted works (Elsevier DOIs). The same record carries
  `pmcid` and `isOpenAccess` (`Y`/`N`); for an open-access PMC article
  `GET /{PMCID}/fullTextXML` returns the JATS full text (checked
  2026-11-06; an unknown PMCID answers 500 with a JSON body), which the
  pipeline converts to plain text (tables, results, methods, the rest).
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

- **A CLI first, an HTTP daemon on top:** the job is batch-oriented and needs
  a human eye on intermediate artifacts. `internal/pipeline` is
  transport-agnostic — `verdictd` wraps the same pipeline in a queue and an
  artifacts API.
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
- **Effects are pooled in Go, like a meta-analysis:** the critic only
  *extracts* the numbers a direct study reports (counts, means, or a
  reported estimate with its CI and the CI's level), `internal/meta` *computes* the
  random-effects pooled estimate (DerSimonian-Laird), and the judge *reads*
  it — so three non-significant trials with a significant pooled effect are
  not judged one by one. The report states the direction of the effect
  (`effect_direction`) next to the verdict.
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
  text critique); chart checks through the agent's vision tool
  (`3b-vision.json` consumed by synthesis). PubPeer — devkey requested,
  decision pending their reply (see the live-verification notes).
- **v3**: `verdictd` — the HTTP wrapper (queue + artifacts API,
  `cmd/verdictd`); the SQLite run index (`runs/verdict.db`, `verdict
  index`); the per-topic enrichment cache; PICO normalization (`verdict
  pico`).
- **v4 — token economy (2026-10-18)**: the measured problem was the
  per-call session overhead of the claude-cli backend — ~74k input tokens
  per call for 1–2k of useful payload (a default `-p` session loads the
  whole harness), 16 calls ≈ 1.24M tokens per lean run. The fix, measured
  end-to-end on the same corpus (1.24M → ~144k input tokens, 8.5×):
  per-call usage recording (`cost` in the stage artifacts, an "LLM usage"
  footer in report.md); lean CLI invocation (custom `--system-prompt-file`,
  no CLAUDE.md/memory/MCP/skills per call); batched critique (`LLM_CHUNK=5`
  studies per call, single-study fallback for what a batch drops); the
  per-topic critique cache keyed by (study, question, content fingerprint —
  a dropped PDF re-critiques, an unchanged corpus rides free, so a
  challenge re-run costs one synthesis call); the judge payload diet
  (abstracts re-read only for direct studies, matrix notes required only
  for them); per-role models (a cheap model for the routine critique, the
  strong one for the judge); and the
  stateless JSON retry fixed to resend the payload it is asking to correct.
- **v4.1 — text modes + pickup fixes (2026-10-19)**: one `--text-mode lean |
  hybrid | full` flag instead of the bool `--fulltext` (hybrid — abstracts
  critique, then OA full texts for the pivotal studies (direct first,
  indirect in the free slots; Europe PMC XML, else the PDF) and their deep
  re-critique before the single synthesis — is the default); the full-text pickup no longer dies on a warm enrichment cache
  or a resume (PDFs in `fulltext/` always enter, at critique time if stage
  2 was skipped); multi-query discovery fetches page 0 of every query
  before page 1 of any and trims the corpus fairly across the
  reformulations.
