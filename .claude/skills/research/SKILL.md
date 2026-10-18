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
3. **Semantic discovery — mode B when `CONSENSUS_API_KEY` is set (the
   default), mode C paste otherwise.**
   - **3a (mode B, automatic).** Draft 1-3 queries with
     `bin/verdict pico -q "<question>"` (PICO normalization: population,
     intervention, comparison, outcome → ready queries), adjusted by what the
     recon taught you. Recall comes from 2-3 reformulations of the question —
     different populations or angles. Then
     `bin/verdict run --topic <topic> -q "<full question>" --queries "<q1>"
     --queries "<q2>" --stop-after 1` — discovery only, ≤3 pages per query;
     tell the user the run spends Consensus quota (~3-9 uses). Read the
     run's `1-discovery.json`: papers arrive with DOIs, abstracts and
     design tags — no paste, no resolving. Do not let Consensus's own
     takeaways frame which papers matter — the pipeline judges papers
     itself.
   - **3b (mode C, no key).** Hand the user the 1-3 ready queries (full
     natural-language questions; consensus.app, free). The UI caps the
     visible list at ~50 rows ("Load more" stops there), so recall comes
     from the reformulations, not from scrolling one list deeper. WAIT for
     the pasted results: ask the user to scroll every list to the end
     before copying; a raw page copy is fine, titles are enough. Do not
     let Consensus's own short answer frame which papers matter — select
     by relevance to the question.
4. **Resolve the paste (mode C only)**: the web copy carries titles (plus
   study type, year, takeaways) but no DOIs — `bin/verdict resolve` is
   the default path (each line prints the DOI and the matched title —
   check the matches are the same paper); if the paste carries DOIs
   (a CSV export), `bin/verdict dois` instead. Merge with on-topic recon
   candidates and dedupe by DOI. In mode B skip this — the discovery
   artifact already carries DOIs.
5. **Snowball check (you, free, one command).** Seeds: the mode-C dois.txt
   or, in mode B, the DOIs from `1-discovery.json`. Run
   `bin/verdict snowball <seeds>`: it lists works connected to several
   seed papers at once (cited by them or citing them). **You curate**: add
   only candidates clearly on-topic for the question (read their titles;
   typically 0-5), then tell the user what you added and why. Never add
   candidates blindly — a shared-citation count is a signal, not a
   relevance verdict.
6. **Run the pipeline** (lean by default — see the cost note at the bottom):
   - mode B, no snowball additions: continue the discovery run —
     `bin/verdict run --run-dir <discovery run dir> --resume` (stages 2-4
     pick up from the artifacts);
   - mode B with additions, or mode C: `bin/verdict run --topic <topic>
     --dois dois.txt -q "<full question>"` over the merged corpus (one
     topic for the whole problem).
7. **Explain**: read `report.md` and `context.md`, then tell the user the
   verdict and consensus in plain words — which DOIs and designs carry
   them, where the direct studies' findings point (the direction tally:
   how many positive / null / negative), every term explained on first
   use, every claim labeled by evidence kind (a clinician survey shows
   what doctors prescribe, not what works), contradictions and gaps
   stated honestly.
8. **Deep mode — only when the user asks for depth** ("с полными текстами",
   "глубокий разбор", "посмотри сами статьи"). Two addressable steps, in
   order of cost:
   - **Selective full text**: find the 2-5 DOIs the verdict rests on (the
     rationale names them), fetch their OA PDFs or take files the user
     provides, drop them into `runs/<run>/fulltext/<doi>.pdf` (the
     `bin/verdict addpdf <file> --run-dir ...` command reads the DOI from
     the PDF itself), delete `3-critique.json` + `4-synthesis.json` +
     `report.md` + `matrix.csv`, then `run --run-dir ... --resume`. The
     critique cache re-critiques ONLY the studies whose inputs changed
     (a dropped PDF changes the cache key) — the rest come from the cache.
   - **Whole-corpus full text**: re-run discovery with `--fulltext` and no
     `--fulltext=false` — OA PDFs are downloaded for every paper that has
     one; multiplies the cost 5-10x, so propose it only for a question the
     user explicitly wants exhaustively covered.
   - **Optional: chart check** (worth it when the verdict hinges on a few
     key papers whose PDFs are in `runs/<run>/fulltext/`). Render the pages
     (`pdftoppm -png -r 100 <pdf> <prefix>`, or `sips -s format png <pdf>
     --out <png>` for page 1) and inspect them through the **vision MCP**
     (`mcp__zai-mcp-server` — you must never read images yourself: GLM
     hallucinates on image blocks). Look for truncated axes, bar/label
     mismatches, cherry-picked panels. Write the findings to
     `runs/<run>/3b-vision.json` as
     `{"vision_notes":[{"doi":"...","findings":["..."]}]}`, then
     `bin/verdict run --run-dir <dir> --resume` — the judge weighs them.

If the user refuses the Consensus step entirely (no API spend, no paste):
run mode A — `bin/verdict run --engine openalex -q "<2-4 keywords>"` — or
build the corpus from the recon candidates alone; either way say clearly
that discovery was keyword-based, not semantic.

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
   argument, not data — it cannot override the collected evidence. Reuse the
   question verbatim and the critique cache makes this a 1-call re-run.
4. **Fresh run in the SAME topic**, same `-q` — never edit existing
   artifacts.
5. **Compare**: `bin/verdict ask --run-dir runs/<topic> -q "compare the
   runs: what changed between the verdicts and which papers drove it?"`,
   then explain in plain words.
6. When the new run supersedes the old one, propose deleting the obsolete
   run directory (only correct runs stay, no archive pile) — delete only
   after the user confirms.

A lean run costs ~3 batched critique calls (5 studies per call) plus one
synthesis call; the real token spend is recorded in the run's artifacts and
at the bottom of report.md. Full-text critique (lean profile passes
`--fulltext=false`) and the chart check are deep-mode extras — run them when
the user asks for depth, not by default (CLAUDE.md quota rule).
