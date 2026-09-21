---
name: research
description: >-
  Research workflow over verdict topics: investigate a new question or a
  messy problem, answer questions over finished runs, challenge/correct
  existing conclusions. Use when the user mentions a topic under runs/,
  asks to research something, or questions an earlier verdict.
---

# Research operator for verdict

You operate the `verdict` CLI **for** the user: you type every command
yourself (Bash tool); the user never runs the CLI — they just talk to you.
The only things you may need from them: a Consensus paste and yes/no
confirmations.

Layout: one research problem = one topic directory `runs/<topic>/`, every
assessment = a run inside it; runs are immutable. The tool collects papers,
citation tallies and retraction flags, then drives the LLM
critique-and-synthesis pipeline; the run directory holds everything.

Pick the scenario by the user's request. (Scenarios are numbered; discovery
modes A/B/C are a different thing — see docs/usage.md.)

## Scenario 1 — the user brings a problem or question (any form)

1. **Understand the input.** A wall of text, a dialogue dump with another
   agent, a one-liner — anything works: save it to a file and run
   `bin/verdict brief <file> --out briefs/`. Read the brief: `agent_tasks`
   you settle yourself right away; `evidence_questions` drive the next
   steps, highest priority first.
2. **Recon (you, free, a couple of minutes).** Probe
   `https://api.openalex.org/works?search=<short keywords>&per-page=5`
   with 2-4 keyword queries to learn the field's vocabulary and collect
   candidate DOIs. Use what you learn to sharpen the Consensus queries in
   the next step.
3. **Hand the user 1-3 ready Consensus queries** (full natural-language
   questions; consensus.app, free). Draft them with
   `bin/verdict pico -q "<question>"` (PICO normalization: population,
   intervention, comparison, outcome → ready queries), adjusted by what the
   recon taught you. The Consensus UI caps the visible list at ~50 rows
   ("Load more" stops there), so recall comes from 2-3 reformulations of
   the question — different populations or angles — not from scrolling
   one list deeper. WAIT for the pasted results: ask the user to scroll
   every list to the end before copying; a raw page copy is fine, titles
   are enough. Do not let Consensus's own short answer frame which papers
   matter — select by relevance to the question. If a Consensus MCP tool
   is configured in this session, run the queries yourself through it
   instead of asking the user.
4. **Resolve the paste**: the Consensus web copy carries titles (plus
   study type, year, takeaways) but no DOIs — `bin/verdict resolve` is
   the default path (each line prints the DOI and the matched title —
   check the matches are the same paper); if the paste carries DOIs
   (a CSV export), `bin/verdict dois` instead. Merge with on-topic recon
   candidates and dedupe by DOI.
5. **Snowball check (you, free, one command).** Run
   `bin/verdict snowball dois.txt`: it lists works connected to several
   seed papers at once (cited by them or citing them). **You curate**: add
   only candidates clearly on-topic for the question (read their titles;
   typically 0-5), then tell the user what you added and why. Never add
   candidates blindly — a shared-citation count is a signal, not a
   relevance verdict.
6. **Run**: `bin/verdict run --topic <topic> --dois dois.txt -q "<full
   question>"` (one topic for the whole problem).
7. **Explain**: read `report.md` and `context.md`, then tell the user the
   verdict and consensus in plain words — which DOIs and designs carry
   them, where the direct studies' findings point (the direction tally:
   how many positive / null / negative), every term explained on first
   use, every claim labeled by evidence kind (a clinician survey shows
   what doctors prescribe, not what works), contradictions and gaps
   stated honestly.
8. **Optional: chart check** (worth it when the verdict hinges on a few
   key papers). Full-text critique is on by default; if the run has
   `runs/<run>/fulltext/*.pdf` (`pdftoppm -png -r 100 <pdf> <prefix>` when
   installed, else `sips -s format png <pdf> --out <png>` for page 1) and
   inspect them through the **vision MCP** (`mcp__zai-mcp-server` — you
   must never read images yourself: GLM hallucinates on image blocks).
   Look for truncated axes, bar/label mismatches, cherry-picked panels.
   Write the findings to `runs/<run>/3b-vision.json` as
   `{"vision_notes":[{"doi":"...","findings":["..."]}]}`, then
   `bin/verdict run --run-dir <dir> --resume` — the judge weighs them.

If the user explicitly refuses the manual Consensus step: build the corpus
from the recon candidates alone — and say clearly that discovery was
keyword-based, not semantic.

## Scenario 2 — the user asks questions about an existing topic

1. Read `runs/<topic>/*/context.md` — usually enough to answer directly;
   cite DOIs, admit what is not in the data.
2. Heavier interrogation → `bin/verdict ask --run-dir runs/<topic>` (loads
   all runs of the topic, newest with full context; a single run directory
   works too).

## Scenario 3 — the user challenges a conclusion or brings more papers

1. **Find the challenged run** under `runs/<topic>/` (newest = the current
   verdict). Reuse its question **verbatim** — the `question` field of its
   `1-discovery.json` — otherwise the verdicts are not comparable.
2. **Assemble the corrected corpus**: union of the run's DOIs (also in its
   `1-discovery.json`) and the new papers (resolve as in Scenario 1); a
   snowball check is welcome here too.
3. **A counterargument, not papers?** (`"your moderate is too generous"`)
   Re-run the SAME corpus with `run --challenge-note "<the argument>"`:
   the judge must weigh it and answer it in the rationale. The note is an
   argument, not data — it cannot override the collected evidence.
4. **Fresh run in the SAME topic**, same `-q` — never edit existing
   artifacts.
5. **Compare**: `bin/verdict ask --run-dir runs/<topic> -q "compare the
   runs: what changed between the verdicts and which papers drove it?"`,
   then explain in plain words.
6. When the new run supersedes the old one, propose deleting the obsolete
   run directory (only correct runs stay, no archive pile) — delete only
   after the user confirms.

A run costs ~1 LLM call per paper plus one synthesis call.
