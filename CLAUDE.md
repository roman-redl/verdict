# CLAUDE.md — verdict

Go orchestrator for evidence assessment of scientific claims
(discovery → smart citations / retraction checks → LLM verdict).
Docs: README.md, docs/how-it-works.md (design), docs/usage.md (usage),
.claude/skills/research/SKILL.md (the agent research workflow).

## Rules

- **File changes — Write/Edit tools only**, never `cat >`/python heredocs in Bash: diffs must be visible in the CLI and go through approval. Exception — programmatic transformation of large generated files: announce it explicitly and show a change summary (what and how much was replaced). (2026-09-09)
- **Commit messages: conventional prefixes + English, text only** — `feat:` / `fix:` / `docs:` / `chore:` / `test:` / `refactor:` / … ; no auto-added trailers (`Co-Authored-By: …`, "Generated with …"). (2026-10-18)
- **Write only inside this repository** — never touch files or configs outside it.
- **Paid APIs and LLM calls — only on an explicit user request** (tests run on httptest, not live APIs). Read-only curl checks against free public APIs (OpenAlex, Crossref, Semantic Scholar, PubPeer) to verify response shapes are allowed without asking (approved 2026-09-07).
- `claude-zai` blocks the corporate gateway for hours after the first request — never launch it without an explicit user request.
- **Models by exact name only** (aliases silently fall back to the wrapper default).
- **Research requests → invoke the `research` skill first:** any request to investigate a new question, ask about a topic under `runs/`, or challenge an earlier verdict starts with the research skill (`.claude/skills/research/SKILL.md`).
- **Discovery default is mode B (semantic, automatic) when `CONSENSUS_API_KEY` is set:** run `bin/verdict pico` for 1-3 semantic queries, then `bin/verdict run --topic <topic> -q "<full question>" --queries "<q1>" --queries "<q2>"` — the pipeline searches Consensus itself (≤3 pages/query = ≤3 API uses/query; tell the user the run spends Consensus quota). Without the key, mode C is the default: hand the user 1-3 ready Consensus questions (consensus.app, free; the UI caps the list at ~50 rows — recall comes from reformulations, user scrolls each list to the end before copying), wait for the paste, resolve titles→DOIs (`bin/verdict resolve`), run `--dois`. Mode A (`--engine openalex`, keyword query in `-q`) only on an explicit user opt-out. Never silently replace semantic search with OpenAlex keyword search or agent-side curation — keyword search misses papers that do not contain the query terms (verified live). Engine choice rationale: docs/how-it-works.md §2.2.
- **Lean profile is the default for agent-launched runs:** `--max-papers 15 --text-mode lean --consensus-pages 2`, two queries. Critique is batched (`LLM_CHUNK=5` studies per call, flash wrapper via `LLM_COMMAND_CRITIQUE`) — a lean run is ~3 critique calls + 1 synthesis; the real token spend lands in the artifacts (`cost` field) and at the bottom of `report.md`. Default `--text-mode hybrid` (abstracts pass, then OA full texts for up to `HYBRID_PIVOTAL_MAX` pivotal studies + re-synthesis) costs ~2-4 calls more — fine for user-facing runs, skip it for cheap batch work with `--text-mode lean`. Whole-corpus full text (`--text-mode full`, 100+ full-text critiques — millions of tokens pre-optimization, measured 2026-10-11) only on an explicit user request for depth.
- **After each run, sanity-check `report.md`'s rationale:** a synthesis JSON retry can drop the study payload and emit a hollow `insufficient` saying "no per-study input data was received" (the retry now resends the payload — fixed 2026-10-18 — but stay alert). If so: delete `4-synthesis.json` + `report.md` + `matrix.csv` in the run dir, then `run --run-dir ... --resume` (re-synthesis costs 1 LLM call; critiques are kept and cached in `runs/<topic>/critique-cache.json`).
- Never commit `.env` or `runs/`; never leak keys into code/logs/docs.
- **Images — via vision MCP only** (`mcp__zai-mcp-server`): GLM models cannot see image blocks in messages — never answer from them (hallucination); never touch image-cache files.

## Verification

`PATH=/usr/local/go/bin:$PATH make check` (build + vet + test + gofmt).

Live-run verifications: Scite `/tallies` — done 2026-09-07 (request body is a bare JSON array; response uses the `{"tallies": ...}` envelope); clean stdout from `~/.zshrc` — done; Consensus `/v1/search` — done 2026-10-10 (`x-api-key` auth; the `page`/`page_size`/`is_end`/`next_page` envelope; `page_size` in the response is the plan-applied value).
