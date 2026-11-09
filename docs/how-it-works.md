# How it works: in plain words

An explanation from zero: first — how scientific publishing is organized at
all (without this the terminology hangs in the air), then every service:
who they are, where their data comes from, what they return, and where it
fits in the pipeline. At the end — a worked example from question to report.

## Part 0. How scientific publishing works

1. A scientist runs a study and writes a **paper** — a document of the form
   "here is the question → here is how we tested it → here is the data →
   here is the conclusion".
2. It goes to a **journal**. The editors send it to other scientists
   (**peer reviewers**) who check for obvious flaws. The check is useful but
   imperfect: reviewers see the text, yet they do not recompute the data
   and do not repeat the experiment.
3. The paper is published and gets a **DOI** — a unique number, forever
   (e.g. `10.1038/nature12373`). Like a license plate: any database in the
   world can find the paper by it.
4. At the top of the paper sits the **abstract** — a half-page summary:
   what was done, on whom, what was found. The rest is the full text:
   methods, tables, figures.
5. When another scientist writes their own paper, they cite previous ones
   ("as shown by Jones et al., ..."). That is a **citation**. Every paper is
   a node in a giant network; the references are the edges.

The whole idea of this tool: cut out a slice of that network around your
question and assess its quality.

## Part 1. Minimum-viable glossary

**DOI** — the paper's "passport number". One per paper, permanent. Any
database will return everything else given a DOI.

**Abstract** — the paper's summary. Enough for a first-pass assessment.

**Retraction** — a journal's official withdrawal of a paper. How it
happens: a paper is published → later it turns out the data was fabricated,
the experiments never happened, or the statistics lie → the journal
publishes a notice: "we retract this paper, its results cannot be
trusted". Why this matters:

- several thousand retractions happen every year;
- the paper itself **does not disappear** from databases — it keeps hanging
  around and getting cited as if it were real; the withdrawal note is small
  and not visible everywhere;
- the classic example: the 1998 paper claiming the measles vaccine causes
  autism (Wakefield, the Lancet). It was retracted only in 2010, when the
  data fabrication came out — in 12 years it wrecked vaccination rates and
  is still cited today.

The "fraud paper" from the original problem statement is almost always a
retraction. That is why the pipeline's first filter is a "has this been
retracted?" check — done **not** by a neural net (which can make things up)
but against a catalog that carries a retraction flag.

**Citation vs smart citation.** A regular database only knows the fact of a
reference: "paper X was cited 200 times". But 200 times tells you nothing:
some praised it, some refuted it, some mentioned it in passing. A **smart
citation** is a classification of the reference's intent: **supporting**
(cited as confirmation), **contradicting** (cited as refuted or
non-reproducible), **mentioning** (just mentioned). Details — in the Scite
section.

**Study design** — how the experiment is built; it determines how much the
result can be trusted (from strongest to weakest):

| Level | What it is | Analogy |
|---|---|---|
| Meta-analysis / systematic review | a rigorous summary of ALL studies on the question | not one witness — a summary of all testimonies |
| RCT | participants are assigned **randomly** to groups: drug / placebo | the lottery rules out self-deception |
| Cohort | a large group is observed for a long time | no intervention; hidden factors corrupt the picture |
| Case report / mice / in vitro | a single case, animals, cells | grounds for a hypothesis, not proof |

**Sample size (N)** — how many participants/objects. 12 people versus
12,000 is the difference between "a curious observation" and "statistics":
a small sample catches coincidences and sells them as effects.

**Publication bias** — journals prefer "we found an effect!" over "we found
nothing". The published world looks more effectful than reality; the report
must remind the reader about this.

**API and JSON.** An API is how a program talks to a service without a
browser: it sends "give me the paper with this DOI" and gets data back.
JSON is the response format: text where everything is laid out in named
fields (`"title": "…"`, `"is_retracted": false`). All of our stage
artifacts are JSON too.

**The verdict** — the final GRADE-adapted scale from evidence-based
medicine: **strong / moderate / weak / insufficient**, with exact
definitions baked into the prompt. As in GRADE, it rates the certainty of
the stated direction, "no effect" included: randomized trials start at
strong and observational studies at weak, each serious problem (risk of
bias, inconsistency, indirectness, imprecision, publication bias) lowers it
one level, and the study count is not a level by itself — one adequate,
well-conducted trial is usually moderate. Measured against Cochrane
conclusions with `verdict eval` (docs/usage.md, "Evaluation").

**Consensus** — where the field as a whole stands on the question. The
important distinction: the verdict answers "how well-proven", the consensus
answers "which way the field leans". There is no central "consensus
registry" in nature; the closest thing to a recorded consensus is
meta-analyses (which we catch into the sample anyway). So the judge LLM
formulates the consensus from the collected data, with a mandatory status:
**broad agreement** / **contested** / **emerging** / **no consensus** /
**insufficient data** — and with DOI references.

## Part 2. The four services

### 2.1 OpenAlex — the free catalog of all science

**Who.** The nonprofit OurResearch — the same team that built the academic
catalog for Microsoft until it was shut down; they rolled out an open
replacement. Hundreds of millions of records, free, no keys.

**How it collects data.** OpenAlex publishes nothing itself — it is a
"stitcher" of other people's sources. Publishers register every paper's
metadata in the Crossref registry (an industry rule); OpenAlex harvests
that, adds PubMed and university repositories, and glues it together:
paper ↔ authors ↔ institutions ↔ journal ↔ the paper's reference list. On
top it computes: citation counts, whether a legal free copy of the full
text exists anywhere, whether the paper is retracted.

**What it returns.** For one DOI (one HTTP request): title, year, authors,
journal, abstract, citation count, a free-PDF link (`oa_url`) when one
exists, the `is_retracted` flag, the work type. A quirk: the abstract is
stored "shuffled" (word → positions — a legal trick around publisher
licenses); our code reassembles it. It can also search: "give me papers
matching this text".

**Where it fits.**
- mode A (free discovery): the question → OpenAlex returns top papers;
- mode C: you bring a DOI list → it fills in the metadata;
- enrichment (always): for every DOI — the retraction flag, the free PDF,
  the abstract if nobody provided it yet. Retractions are checked in two
  places: OpenAlex's `is_retracted` and Crossref's update notices (where
  the Retraction Watch Database lives — it usually learns of retraction
  notices earlier than OpenAlex; corrections and expressions of concern
  land there too).
- enrichment, abstract fallback: OpenAlex withholds the abstracts of
  publisher-restricted works (every Elsevier DOI, for one), and a critic
  without an abstract judges a study by its title alone. After all the primary
  sources, the papers still without an abstract are looked up in
  [Europe PMC](https://europepmc.org/RestfulWebService) — free, no key, one
  batched request per run, abstracts for the biomedical literature. Its
  HTML markup (section headings, italics) is reduced to plain text, and
  `abstract_source` in `2-enrichment.json` records where the abstract came
  from. Papers served from the enrichment cache and a run resumed with
  `--resume` get the same fallback; when Europe PMC is down the run goes on
  and `report.md` carries a data warning.
- full texts (the default `hybrid` text mode, critique stage): the first
  critique pass reads abstracts; then the studies the verdict will rest on —
  *pivotal* — get their full texts and a deep re-critique, before the single
  synthesis. Pivotal means direct studies first (best design quality, then
  most Scite support/contradiction activity), up to `HYBRID_PIVOTAL_MAX`;
  slots the direct studies leave free go to *indirect* ones (same ranking),
  never to tangential or unknown ones. An indirect study is often one whose
  abstract just does not report the asked outcome (a secondary outcome lives
  in the full text only), and without its full text it could never be
  re-judged; the deep critique replaces the abstract one — relevance and
  direction included — so such a study can come out direct. The full text
  comes from Europe PMC when the paper is an open-access PMC article: the
  JATS XML is turned into plain text with every table first (caption, then
  one row per line, cells separated by ` | `; tables a publisher keeps
  outside the body are included), then the results, the methods and the
  rest — the critic reads only the head of the text, and the secondary
  outcomes are in the tables and the results, so a long Results section
  must not push the tables out of the window. The reference list, formulas
  and front matter are dropped. The stored text is capped at 60,000
  characters (`fullTextMaxChars`), and the critic reads the first 30,000 of
  it (`critiqueFullTextChars`; the abstract keeps its 3,500). Both limits are
  constants, not settings: the window decides the input size of every
  critique batch, and a changed window changes the critique cache key. Europe
  PMC requests are retried like every other adapter's (network errors, 429
  and 5xx, four attempts with exponential backoff and `Retry-After`; other
  4xx are final). Papers outside PMC fall back to the OA PDF
  link (a landing page or a bot-protected publisher page yields nothing);
  `full_text_source` in `2-enrichment.json` / `3-critique.json` records
  `europepmc` or `pdf`. When the XML is used the critic still reads the XML
  text, and the OA PDF (if the paper has a link) is downloaded as well and
  saved in `fulltext/` for the chart check; its text is not extracted. A
  failed PDF download then is only logged, not recorded on the paper — drop
  one in with `verdict addpdf` if the charts matter.

**Limits.** Its search is "by words" (occurrences in titles/abstracts), not
"by meaning" — it misses on convoluted questions; hence modes B and C.

### 2.2 Consensus.app — search "by meaning" (the default discovery; not to
be confused with the verdict's "consensus" field above)

**Who.** Consensus (consensus.app) — an AI academic search engine over
220M+ papers: free web UI with copyable lists, a CSV export with DOIs and
study-type tags, and honest null answers ("no papers met the relevance
bar").

**How it works.** Semantic search: for "does creatine improve cognitive
performance" it will find "Dietary supplementation and executive function:
a randomized trial" — a paper that contains none of your words — because
it understands meaning rather than matching strings. Each result carries
a study-type tag (RCT, systematic review, case report...) and an
AI-generated takeaway (a convenience to skim, never evidence — the
pipeline judges papers itself).

**What it returns.** A ranked list of papers: title, authors, year,
journal, citations, study type, abstract. Caveat: the web UI shows a
top-~50 slice of the ranking ("Load more" caps there) — everything you
see is a ranked cut, which is why the workflow asks for 2-3
reformulations of the question and pairs the paste with the citation
snowball (rank-free recall).

**Where it fits.** Mode B (the default with an API key): the Go client
(`internal/consensus`) walks `GET /v1/search` pages with `x-api-key`; every
request plausibly costs one plan use (500/mo on the Pro $12 tier), so the
walker caps pages (`CONSENSUS_PAGES`, 3 per query) and records the spend —
engine, queries, pages, truncation — in `1-discovery.json`. The page cap
mirrors the UI's ~50-row ranked cut, so recall still comes from query
reformulations (`--queries`, drafted by `verdict pico`). Mode C (the free
web UI: you run the queries, scroll to the end, paste back) remains the
semantic path when no key is set; an MCP server exists too (the same
search over a remote connection) but the API is what the pipeline uses.
Consensus does nothing else in the pipeline — its niche is discovery.

**Why this engine — the 2026-09-08 bake-off (the one place the retired
engine is named).** Elicit was the original discovery engine here (mode B
API client + mode C over its web UI). It was removed from the codebase
completely after a head-to-head on a real question ("is adapalene
effective for keratosis pilaris?") with a ground-truth corpus assembled
from every route we had (~50 known on-topic papers). Candidates and
criteria:

| Criterion | Consensus | Elicit (retired) | SciSpace (Lite Deep Research) |
|---|---|---|---|
| Free tier: copy/export of the paper list | full-page copy + CSV with DOI, study type, SJR quartile, abstracts | copy only the displayed rows; export marginal | answer text + 10 references; **CSV export is paid-only** |
| Usable deliverable for a DOI pipeline | 53 rows × 11 columns, DOIs ready | ranked top-N without DOIs | none (references as prose) |
| Recall vs ground truth | top-40 captured the core; the ~13-row tail still held ≥4 new true positives | 3-5 final sources; the 15-per-query pools were never reachable without manual per-list copying | agent searched 152 records, cited 10 — half of them book chapters and off-topic |
| Null-question honesty | explicit "no papers met the relevance bar" | short answer, needs interpretation | correct TL;DR on weak citations |
| Corpus size | 220M+ | 138M | 280M+ |
| Paid automation | Pro $12/mo incl. 500 API & MCP uses | Pro $49/mo | paid tiers higher; Lite tested |
| Ranked-list ceiling | ~50 rows in the UI (recall recovers via 2-3 query reformulations) | similar display caps | 152 records internal, 10 visible |

Decision: Consensus for discovery in every mode; the Elicit Go client was
deleted (not left dormant); SciSpace was not pursued further. Runs dated
before 2026-09-08 were discovered through the retired engine's web UI
(their artifacts carry no engine references — provenance note for future
readers of runs/). OpenAlex
stays as the free keyword fallback and the snowball/metadata engine — it
occupies a different slot (citation graph, not semantic ranking).

### 2.3 Scite — "who supported, who refuted"

**Who.** A company that built something nobody else has: a classification
of HOW papers are cited.

**How it collects.** Regular databases know the fact of a reference. Scite
got access to the full texts of millions of papers (publisher agreements +
open copies), found every reference to another paper inside them, and with
an automatic text classifier (validated against human annotation)
determined what the citing author does with the cited work — compare two
sentences from real full texts:

- "Our findings are consistent with Smith et al. …" — **supporting**: the
  author leans on the work as confirmation;
- "In contrast to Smith et al., we observed no effect …" —
  **contradicting**: the author could not reproduce it or got the opposite.

About a billion citations are classified this way.

**What it returns.** For a paper's DOI — counters ("tallies"): total /
supporting / contradicting / mentioning. E.g. `{total: 412, supporting:
233, contradicting: 9, mentioning: 170}` — "of 412 citing papers, 233 used
it as confirmation, 9 as refuted". The paid tier also exposes the citations
themselves, with the quoted sentence (who exactly said what) — with a
Scite key the pipeline fetches the DOIs of the contradicting papers and
hands them to the judge.

**Where it fits.** The enrichment stage: one batched request covers all the
run's DOIs — these numbers are the answer to "who confirmed, who refuted"
from the original problem statement. The free tier is plenty at our volume.

**Honest limits.** Only citations from texts Scite has access to are
classified: about a fresh or very niche paper it may "know nothing" — which
means "no data", not "nobody refuted it", and the report says exactly that.
The classifier is sometimes wrong; a contradicting citation does not
automatically mean the paper is refuted — context decides, which is why
the judge weighs it instead of the counter.

### 2.4 The LLM — the critic and the judge

**Who.** A language model — here via the Claude Code tariff (a wrapper such
as `claude-max`), at no extra cost for this project. Each stage has a role
with its own model: a cheaper one for the per-paper critic, a stronger one
for the judge.

**Why it is needed.** After the data collection there is a pile of facts,
and "how much all of it together proves the claim" cannot be computed by an
algorithm — it is a judgment. But there is an iron rule: **the LLM is not a
source of facts**. The model does not know which papers exist or who
refuted whom; asked directly it will produce a plausible invention. So the
databases collect the facts, and the model — already holding them — does
two jobs:

- **Critic** — one paper at a time (calls run in parallel): reads the
  abstract + design tags + citation tallies → a structured assessment:
  design quality, sample adequacy, N (only if actually stated in the
  abstract — inventing is forbidden), strengths, concerns, relevance to the
  question (direct / indirect / tangential), and `study_design` — the design
  of this paper as its text states it (`rct`, `cluster_rct`, `crossover_rct`,
  `nonrandomized_trial`, `cohort`, `case_control`, `cross_sectional`,
  `case_series`, `systematic_review`, `meta_analysis`, `narrative_review`,
  `guideline`, `other`, `unclear`; a meta-analysis of RCTs is `meta_analysis`,
  not `rct`). An unknown word becomes `unclear` with a warning.
- **Judge** — a single final call: all the data + the GRADE rubric → the
  evidence matrix, the verdict, the rationale with concrete paper
  references, the consensus with its status, the contradictions and gaps.
  Numbers must be copied from the input verbatim — recomputing or inventing
  them is forbidden by the prompt.

**Effect numbers and pooling.** A meta-analysis does not judge studies one
by one: three trials that are each non-significant can add up to a
significant pooled effect. The pipeline does the same, with a strict split
of duties. The **critic extracts**: for the question's outcome it copies
from the text the numbers the study reports — events and totals per arm
(binary outcome; a percentage of the arm with its total, `percent` and
`total`, when the text gives no count), mean, SD and n per arm with the unit
(continuous), or an estimate with its interval and the interval's level
(`ci_level`, in percent: 90, 95, 97.2; 95 when absent) for `RR`, `OR`, `HR`,
`MD`, `SMD` — never computed, never invented, and omitted when the text has
none. A reported estimate is copied as printed; when the paper compares the
comparator against the intervention (a "HR for carboplatin vs cisplatin" for a
question about cisplatin), the critic sets `reversed: true` and leaves the
numbers alone (the field applies only to a reported estimate, not to the arms).
Counts are the default source; a reported estimate comes first for
time-to-event outcomes (the hazard ratio, because counts ignore follow-up
time; medians alone are not poolable and go into the strengths or concerns)
and for cluster-randomized trials (the adjusted estimate, because raw counts
overstate precision). **Go computes** (`internal/meta`): each direct study's
effect goes on a common scale — the log risk ratio from counts (events from
a percentage are `round(percent / 100 × total)`, computed in Go and logged as
derived; a 0.5 correction for a zero cell, studies without events in both
arms are skipped), the mean difference from means when every study states
the same unit and Hedges' g otherwise, the log of a reported ratio or the
difference with the standard error taken from its interval at the z of its
level (`z = Φ⁻¹(1 − (1 − level/100) / 2)`; 1.9600 for 95%, 1.6449 for 90%).
A `reversed` estimate is flipped first, in Go, and the flip is logged: a ratio
becomes `1/value` with the interval `1/ci_high … 1/ci_low`, a difference
becomes `−value` with `−ci_high … −ci_low`.
A `ci_level` outside (50, 99.9], a percent outside 0–100 or a percentage
without a total is ignored with a warning, like any other bad number. The
direct, non-retracted primary studies of one measure are combined with the
DerSimonian-Laird random-effects model: pooled estimate, 95% CI,
number of studies, participants when known, I². A single study is shown with
its own interval and marked "not pooled".

**Which studies are pooled.** Counts give an odds ratio as well as a risk
ratio (`meta.LogOR` and `meta.LogRR` share the zero-cell rule), so a study
from counts is a candidate for both an RR pool and an OR pool; a study that
reports only an OR can join the studies with counts in the OR pool. The
candidate pools are RR, OR, HR (time-to-event, never merged with RR or OR),
SMD, and MD per unit. The pool with the most participants wins (a study of
unknown size counts 0), so two small pilot trials do not outweigh one large
trial on another measure; ties go to more studies, then to the order RR, OR,
HR, SMD, MD, then to the unit. A study from counts is never in two pools at
once. Every direct study with usable numbers that is not in the winning pool
is listed in `pooled_estimate.left_out` as `doi (MEASURE)` (a study from
counts is labelled RR) and logged; the judge weighs those studies as
individual results, and the report's pooled line says how many were not
pooled. The **judge reads** the result
(`pooled_estimate` in its input) and may not recompute it; `report.md`
prints the numbers from Go. The method is checked against the BCG vaccine
example of the metafor package. A pooled mean difference also carries `sd`,
the participant-weighted mean of the studies' pooled SDs, when every study
gave its arms' SDs and sizes (a reported estimate has no SD).

**Reviews are not pooled.** A systematic review or meta-analysis repeats the
data of the trials it includes, so pooling both would count that data twice
and make the interval falsely narrow (and the imprecision rating, computed
from it, falsely good). Studies whose design is `systematic_review`,
`meta_analysis`, `narrative_review` or `guideline` are skipped, each with an
info line in the log; the judge is told to treat them as a cross-check, not
as independent evidence. The design is resolved from, in order: the critic's
`study_design`; the discovery tag (Consensus `study_type`: `rct`,
`meta-analysis`, `systematic review`, `literature review`, `cohort study`
and so on, mapped onto the same set; its catch-all `other` says nothing); the
title ("systematic review", "meta-analysis", "pooled analysis", "umbrella
review", "Cochrane review"). A study whose design stays unknown remains in the
pool, as before. The same resolved design fills the Design column when the
paper has no discovery tag (`--dois`, OpenAlex discovery). Critiques saved
before the field existed load as they are and fall back to the tag and the
title.

**Imprecision and effect direction.** A model's judgments of imprecision and
of the direction of the effect vary between runs and mix up "no effect" with
"not enough data", so both come from the pooled estimate by GRADE rules,
computed in Go (`internal/grade`, pure arithmetic):
`imprecision` (`levels` 0-2, `target`, the band, a `reason` sentence with the
numbers) goes next to `pooled_estimate` in the judge's input, and the judge
uses its `levels` exactly instead of judging imprecision again. The null is 1
for ratios and 0 for differences. The thresholds are the defaults for when no
minimal important difference is known: a ratio of 0.75 to 1.25, an SMD of ±0.2,
an MD of ±0.2 SD (only when the SD is known).

- *The interval includes the null.* The point estimate against the band gives
  the `target`: inside it is `little_or_no_difference`, below `lower`, above
  `higher`. An interval entirely inside the band is precise (0 levels);
  otherwise each band edge strictly inside the interval costs one level, at
  most two. An MD without an SD has no band: nothing tells little or no
  difference from a small effect, so it costs one level and the `target` is
  `undetermined` (no direction is implied).
- *The interval excludes the null.* The `target` is the side of the null, and
  the null is the threshold. For a ratio, an interval whose upper limit is at
  least 3 times the lower (2.5 for an OR) is wide, few data behind a large
  effect: one level. For a mean difference the optimal information size of
  about 800 participants applies: under 240 two levels, under 800 one; with
  unknown participants no level, and `size_checked` is false.

The judge's own `effect_direction` (`supports`, `no_effect`, `opposite` or
`unclear`) is separate from the verdict (how certain that direction is) and
the consensus status (how much the studies agree), and it has to agree with
the consensus text and the rationale; an unknown value is replaced by `unclear`
with a warning. When the rating exists, the judge also returns `claim_change`
(`lower` or `higher`): the change the claim implies for the outcome as the
pooled estimate measures it (`pooled_estimate.outcome`, the critics' outcome
text, because a critic often extracts the complement of the claim's outcome:
a claim of more pain relief is pooled as "children still in pain", so its
`claim_change` is `lower`). Go then sets the direction: `no_effect` for the target
`little_or_no_difference`, `supports` when the target equals `claim_change`,
`opposite` otherwise. If that differs from the judge's word, the code wins
and a report warning records both. Without the rating, with the target
`undetermined`, or with a missing or invalid `claim_change` (logged), the
judge's direction stays.

Both answers are JSON (machine-readable and checkable); everything is saved
into stage artifacts. If the LLM is unavailable, the pipeline honestly says
"critique skipped" and refuses to synthesize.

## Part 3. A worked example: from question to report

Question: "Does vitamin D help you catch fewer colds?" (based on a real
scientific dispute; details simplified.)

**Stage 1 — discovery** (mode A, free). OpenAlex returns 15 papers,
including: a meta-analysis of 25 trials (BMJ, 2017); a giant trial on
~25,000 people (2019, found no effect); a review; a couple of mouse studies
and small trials.

**Stage 2 — enrichment** (in parallel, one request per paper + one batched
Scite request). For the meta-analysis Scite says: `total 412, supporting
233, contradicting 9`. For the mega-trial: `supporting 61, contradicting
24` — the pressure on it is noticeably higher. For one small paper OpenAlex
answers `is_retracted: true` — it goes into the report with a flag and is
excluded from the supporting side.

**Stage 3 — critique** (15 parallel LLM calls). For the meta-analysis:

```json
{"doi": "10.1136/…", "design_quality": "high", "sample_adequacy": "adequate",
 "sample_size_reported": 11321, "relevance": "direct", "study_design": "meta_analysis",
 "strengths": ["aggregates 25 RCTs"],
 "concerns": ["included trials vary in dosage"]}
```

For the mice: `design_quality: "low"`, `relevance: "indirect"` — barely
affects the verdict but stays in the matrix for honesty.

**Stage 4 — the judge** (one call). It sees: a strong meta-analysis "for",
a giant trial "null/against", small studies "for", one retraction excluded.
The result in `report.md`:

- **Verdict: moderate** — "there is supporting evidence (the 2017 BMJ
  meta-analysis and follow-ups), but the largest general-population trial
  found no effect; the effect likely concentrates in people with baseline
  vitamin D deficiency";
- **Consensus: contested** — the field genuinely disagrees;
- **Evidence matrix**: 15 rows — study, year, design, N, supported/
  refuted, quality, note;
- **Contradictions**: "mega-trial vs meta-analysis — probably populations
  and dosages differ";
- **Gaps**: "recent papers have almost no citations yet — Scite is silent
  about them".

Which is exactly the answer to the original "how well-proven is this fact
and by which papers" — with full tracing back to each stage's artifacts.

## Part 4. Who hands what to whom

```
question ──> [Consensus API] | [OpenAlex search] | [you + the free Consensus web UI]
                        │
                        v
                   a list of DOIs
                        │
        ┌───────────────┴───────────────┐
        v                               v
 [OpenAlex]: retracted? PDF?       [Scite]: supporting /
 abstract? citations               contradicting / mentioning
        └───────────────┬───────────────┘
                        v
     [LLM critic] × N in parallel: design, sample, concerns
                        v
     [LLM judge]: matrix + GRADE verdict + consensus + gaps
                        v
        runs/<question>-<date>/report.md + matrix.csv
```

## Part 5. Reading the report, and honest limits

**In the report:** the Verdict + the effect direction + the pooled estimate
and its computed imprecision when direct studies reported numbers (rendered
by Go) + Consensus (the
field's position and its status); the Rationale (which DOIs drive the conclusion); the matrix
(the Sup/Contra column = supported/refuted); Contradictions (empty ≠
"everyone agrees"); Gaps; Data warnings (what could not be collected).

**What the tool honestly cannot do:**
- full texts only where a legal copy exists: `--text-mode lean` runs on
  abstracts alone, the default `hybrid` reads full texts for the pivotal
  studies the verdict rests on (direct first, indirect ones in the free
  slots), `full` for everybody with an OA copy — a
  paywalled paper still gets an abstracts-only critique (drop its PDF into
  `fulltext/` yourself); hidden methodology otherwise yields an honest
  "unclear";
- it knows no more than Scite: recent papers have almost no data; absence
  of refutations ≠ confirmation — the rule is baked into the judge, but
  keep it in mind yourself;
- it does not catch subtle chart manipulations (v2);
- the verdict is about "the body of evidence", not the truth: weak means
  "little evidence", not "the claim is false".
