# CLAUDE.md — verdict

Go orchestrator for evidence assessment of scientific claims
(discovery → smart citations / retraction checks → LLM verdict).
Docs: README.md, docs/how-it-works.md (design), docs/usage.md (usage),
.claude/skills/research/SKILL.md (the agent research workflow).

## Rules

- **File changes — Write/Edit tools only**, never `cat >`/python heredocs in Bash: diffs must be visible in the CLI and go through approval. Exception — programmatic transformation of large generated files: announce it explicitly and show a change summary (what and how much was replaced). (2026-09-09)
- **Write only inside this repository** — never touch files or configs outside it.
- **Paid APIs and LLM calls — only on an explicit user request** (tests run on httptest, not live APIs). Read-only curl checks against free public APIs (OpenAlex, Crossref, Semantic Scholar, PubPeer) to verify response shapes are allowed without asking (approved 2026-09-07).
- `claude-zai` blocks the corporate gateway for hours after the first request — never launch it without an explicit user request.
- **Models by exact name only** (aliases silently fall back to the wrapper default).
- **Research requests → invoke the `research` skill first:** any request to investigate a new question, ask about a topic under `runs/`, or challenge an earlier verdict starts with the research skill (`.claude/skills/research/SKILL.md`).
- **Discovery default is mode C (semantic, via Consensus):** hand the user 1-3 ready Consensus questions (consensus.app, free; the UI caps the list at ~50 rows — recall comes from reformulations, user scrolls each list to the end before copying), wait for the paste, resolve titles→DOIs (`bin/verdict resolve`), run `--dois`. If a Consensus MCP tool is configured in the session, run the queries yourself instead of asking the user. Never silently replace semantic search with OpenAlex keyword search or agent-side curation — keyword search misses papers that do not contain the query terms (verified live). Engine choice rationale: docs/how-it-works.md §2.2. Modes A/B only on an explicit user opt-out.
- Never commit `.env` or `runs/`; never leak keys into code/logs/docs.
- **Images — via vision MCP only** (`mcp__zai-mcp-server`): GLM models cannot see image blocks in messages — never answer from them (hallucination); never touch image-cache files.

## Verification

`PATH=/usr/local/go/bin:$PATH make check` (build + vet + test + gofmt).

Live-run verifications: Scite `/tallies` — done 2026-09-07 (request body is a bare JSON array; response uses the `{"tallies": ...}` envelope); clean stdout from `~/.zshrc` — done. Still pending: the Consensus API/MCP integration for mode B (see docs/how-it-works.md §2.2).
