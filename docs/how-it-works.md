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
- mode B, review reference mining: for the systematic reviews and
  meta-analyses among the Consensus hits, the reference list (`referenced_works`)
  is read and resolved to titles and DOIs — the trials a review cites join the
  discovery pool (§2.2);
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
- enrichment, trial registration numbers: several publications of one
  trial (the 12-month result, the five-year result, the HTA report) must not
  count as separate studies, and the only key that tells they are one trial is
  a shared registration number. After the abstracts, each paper gets
  `trial_registrations` in `2-enrichment.json`: the numbers its abstract
  names (ClinicalTrials.gov NCT, ISRCTN, ACTRN, ChiCTR, EudraCT, DRKS, CTRI,
  UMIN, IRCT, NTR/NL, PACTR — regular expressions, no model; PROSPERO CRD
  numbers register reviews and are not read), normalized to uppercase without
  spaces. For a primary study whose abstract names none, PubMed's record is
  asked: the accession numbers of its data banks (`DataBankList`). That is one
  `esearch` per 20 DOIs plus one `efetch` per 200 records (about three free
  requests for a 40-paper corpus, through the same NCBI client and rate limit
  as the PubMed channel); a PubMed failure is a log warning and the numbers
  from the abstracts stay. Artifacts written before the field existed load
  as they are, and the abstracts are read for the numbers on the fly.
- full texts (the default `hybrid` text mode, critique stage): the first
  critique pass reads abstracts; then the studies the verdict will rest on —
  *pivotal* — get their full texts and a deep re-critique, before the single
  synthesis. Pivotal means direct studies first (best design quality, then
  largest sample size — a larger trial weighs more in the pooled estimate,
  while citation counts measure popularity; an unknown size sorts last in its
  tier), up to `HYBRID_PIVOTAL_MAX` (default 7); slots the direct studies
  leave free go to *indirect* ones (same ranking), never to tangential or
  unknown ones. The cap counts studies that *have* a full text: a candidate
  whose text cannot be obtained (paywalled, no open-access copy) passes its
  slot to the next one in this order, and a study that already has its text
  (a PDF the user placed, an earlier pass) counts without a fetch. A candidate
  whose trial (a shared registration number) already has a full text among the
  pivotal studies is skipped the same way: a second report of one trial adds
  no new study to read. The walk
  is bounded at three fetch attempts per slot, so a run of paywalled papers
  cannot fetch the whole corpus; the log line `hybrid: pivotal full texts`
  gives the cap and the tried and obtained counts. An indirect study is often one whose
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
  4xx are final).

  Where Europe PMC has nothing, the sources are tried in this order, and the
  first usable text wins (a PDF already in `<run>/fulltext/` beats all of
  them):

  1. Europe PMC JATS XML (an open-access PMCID is needed);
  2. PubMed Central through NCBI: the PMCID is the one Europe PMC or an
     OpenAlex PMC location gave, else the NCBI ID converter's answer for the
     DOI; `efetch` returns the XML, which counts only when it has a body of
     real text (for an article the publisher does not allow to download it
     carries the front matter alone);
  3. the PDF, from every location OpenAlex lists (the enrichment keeps all
     of them in `oa_locations`: PDF and landing URLs with the host type):
     repository PDFs first, then the publisher's, then the open-access link,
     each tried once; a response that is not a PDF (a landing page) fails
     that address only;
  4. the [Jina Reader](https://jina.ai/reader) on the PMC page (the current
     `pmc.ncbi.nlm.nih.gov` form: the old address meets a reCAPTCHA), then the
     landing pages, then the PDF addresses, at most four pages per paper.
     A doi.org address is resolved to the publisher's page through the
     doi.org handle API first: the reader refuses doi.org.

  Every plain `http://` address is fetched as `https://`: publishers redirect
  anyway, and in the working environment the session's local proxy answers
  plain http with 503. The reader's text must pass a content check: the page
  is rejected when its first 4,000 characters carry a challenge, error or
  paywall marker ("Just a moment", "Performing security verification",
  captcha, Anubis, "HTTP 403", "Access this article", "Buy this article",
  "Skip to main navigation", "Your privacy, your choice", Ovid's logo, ...; the
  reader answers a challenge with HTTP 200, so the status says nothing), when
  it is shorter than 10,000 characters, or when it has no Discussion heading
  or none of Introduction / Background / Methods. The Discussion requirement
  is what separates an article from a publisher's record page: a structured
  abstract (Background / Methods / Results / Conclusion) never has one. Links
  and images are stripped from the markdown; pipe tables stay. The reader is a third-party service: it sees the addresses of
  the papers it reads, never personal data. It is limited to 20 requests a
  minute without a key (the adapter spreads them evenly); `JINA_API_KEY`
  (a free key, `.env` only, never logged) raises that to 500.

  `full_text_source` in `2-enrichment.json` / `3-critique.json` records
  `europepmc`, `pmc`, `pdf` or `jina:<host>`. When an XML source is used the
  critic still reads the XML text, and the first candidate PDF is downloaded
  as well and saved in `fulltext/` for the chart check; its text is not
  extracted, and a failed download then is only noted in the log — drop one
  in with `verdict addpdf` if the charts matter.

  One Info line per study ("full text obtained" / "full text not obtained")
  names the source and lists, compactly, why every source that did not
  deliver was skipped or failed, so a later audit of a run needs only its
  log; when nothing delivered, the failures are also recorded on the paper.
  The report ends with a "Full texts not obtained" section (hybrid and full
  modes): the direct studies read from the abstract only, each with its DOI,
  the best page to open and the file name to save the PDF under
  (`<run>/fulltext/<doi with / as _>.pdf`, or `verdict addpdf` to have the DOI
  read from the PDF); a rerun with `--resume` picks the files up.

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
(`internal/consensus`) walks `GET /v1/search` pages with `x-api-key`. The
request carries `page_size` (`CONSENSUS_PAGE_SIZE`, default 100); the plan
caps it (Free 20, Pro and Teams 300, Deep 750) and the response says which
size was applied — a smaller one than requested is logged at once. Pages
after the first are a paid feature: a Free key gets 403 on them, which ends
that query's walk with a warning, not a failed run. The quota unit is the API
call, one per 100 papers a page returns (rounded up); `1-discovery.json`
records the engine, queries, pages, `api_calls`, the applied `page_size` and
truncation. The walker takes up to `CONSENSUS_PAGES` pages per query
(default 1) and does not stop when the pool is large: the pool is screened
and cut afterwards. One page of 100 per query is enough: on 38 MedEvidence
questions (81 studies, the source review excluded) page 1 found 63 of the 81,
and page 2 found 2 more of the 18 misses, both already found by the review
references. Mode C (the free web UI: you run the queries, scroll to
the end, paste back) remains the semantic path when no key is set; an MCP
server exists too (the same search over a remote connection) but the API is
what the pipeline uses. Consensus does nothing else in the pipeline — its
niche is discovery.

**From the ranking to the corpus (mode B).** A search ranking is the wrong
place to cut a corpus: it puts reviews and case series next to trials, and a
trial that one review cites may rank low for every query. And one source is
not enough: measured, Consensus alone finds under half of the trials of the
reference reviews on real topics. The Cochrane Handbook (ch. 4) and the
Cochrane Rapid Reviews guidance (2023) ask for at least two sources for a
trial search, so mode B has three channels — Consensus, PubMed and the
reference lists of reviews — and the recall of each is measured separately.
Discovery keeps a *pool* and chooses the corpus in four steps:

1. **Pool.** Every hit of every channel, deduplicated by normalized DOI. A
   paper keeps *every* channel that found it, each with its own rank, in
   `sources` (`1-discovery.json`, `discovery.pool`):

   ```json
   "sources": [
     {"source": "consensus", "query": 1, "rank": 12},
     {"source": "pubmed", "search": "rct_filter", "rank": 3},
     {"source": "review_refs", "citing_reviews": 2}
   ]
   ```

   A rank is the position in that channel's ranking across pages, from 1;
   Consensus gives one entry per query (its 1-based number in `query`), PubMed
   one per search (`rct_filter` | `reviews`), the review references one entry
   with the number of reviews that list the paper and no rank. The candidate
   also carries the design tag (Consensus's, or derived from PubMed's
   publication types), a short snippet (the takeaway, else the abstract
   start) and, for PubMed hits, the PMID. The whole pool, dropped candidates
   included, is saved, so the depth at which a known trial appears can be
   measured per channel. Artifacts written before `sources` existed still
   load (their `source`, `query`, `rank` and `cited_by_reviews` fields are
   read into it). `--discovery-sources` (or `DISCOVERY_SOURCES`) runs any
   subset of `consensus,pubmed,review_refs`, taken literally.

   **Which channels run by default.** Consensus and the review references
   always; PubMed only when needed. Measured on the same 81 studies: Consensus
   page 1 found 63, the review references 62, PubMed's trial filter 43, all
   three together 72 — and PubMed found nothing the other two missed (nor on
   four GERD topics), while adding about 350 candidates to screen (about 7
   `plan` calls). So the order is: walk Consensus, count the systematic
   reviews and meta-analyses in its pool (the design resolved from the tag,
   else the title, as for the mining), and run PubMed only when there are
   fewer than 2 of them — then the reference mining has little to read and
   PubMed is the fallback channel; then the reference lists of the reviews of
   every channel are mined. `--pubmed on` forces the channel, `--pubmed off`
   disables it (`PUBMED_DISCOVERY`); a channel list names PubMed literally.
   The decision (`mode`, `consensus_reviews`, `enabled`) is in
   `discovery.pubmed_decision` and the log.

   **The PubMed channel** (free, NCBI E-utilities, `internal/pubmed`). The
   `plan` model writes the topic query once per run: from the question it
   names the concepts — the population or condition, the intervention, and
   the comparator only when it is a specific drug or procedure, never the
   outcome — and for each concept the PubMed terms: MeSH headings
   (`"Heading"[Mesh]`), free-text synonyms (`[tiab]`) with generic and brand
   names, abbreviations, spelling variants and safe truncation
   (`fundoplicat*[tiab]`). Go validates the terms (balanced parentheses and
   quotes, non-empty) and joins them itself: OR inside a concept, AND between
   concepts; the model's own join of the query is kept in the artifact for the
   log only. Two searches follow, both composed in Go: (a) the topic AND the
   Cochrane Highly Sensitive Search Strategy for randomized trials (the
   sensitivity-maximizing version, 2008 revision, PubMed format — about 0.96
   sensitivity for trials with a good topic query), at most 300 hits in
   relevance order; (b) the topic AND `systematic[sb] OR meta-analysis[pt]`,
   at most 50. If (a) finds nothing it is repeated once without the
   comparator concept, and (b) then uses the broader topic. `esummary` gives
   each PMID's title, year, publication types, DOI and PMCID. Publication types
   become the design tag: "Randomized Controlled Trial" and "Clinical Trial"
   (any phase) → `rct`; "Systematic Review" and "Meta-Analysis" → a review.
   A record with no DOI stays in the pool for the audit (with its PMID) but
   cannot be selected — the pipeline is keyed by DOI; the count is logged and
   warned. A PubMed failure is a warning, not a failed run (the Consensus
   quota is already spent), unless PubMed is the only search channel. The
   query-writing call is accounted in the `cost` record as `query_writing`;
   `--pubmed-query "<query>"` supplies a ready topic query and skips it.
   `NCBI_API_KEY` is optional: 3 requests a second without it, 10 with it.
2. **Review reference mining** (free, OpenAlex). For the systematic reviews and
   meta-analyses of the pool — those Consensus and PubMed found, recognized by
   the tag or the title (there is no critique yet) — at most 8 in all, best
   ranked first, the reference list is read (at most 150 references per
   review) and the referenced works with a DOI join the pool as `review_refs`
   candidates, with the number of reviews citing each (at most 300 new
   candidates, the most cited first); a work already in the pool gains the
   `review_refs` source. A failing lookup is a warning. Without OpenAlex or the
   screening model this step is skipped (with `--no-screen` the model is not
   needed). The manual `verdict snowball` stays as an optional check:
   it finds works tied to several seeds, mining takes what even one review cites.
3. **Screening** (an LLM, the cheap `plan` role). The model reads title, year,
   design tag and snippet — batches of 50 candidates with a DOI — and says per
   candidate: include or not, a design guess (`rct`, `other_primary`,
   `review`, `other`) and a short reason. It includes studies that test the
   question's intervention against its comparator in the question's
   population, any design, and reviews of the question; it excludes the
   off-topic, animal and in-vitro work, editorials and protocols; when the
   title does not let it tell, it includes. A failed call leaves its batch
   unscreened: Consensus hits of it stay eligible, candidates found only by
   PubMed or by review references do not (hundreds of papers chosen by a loose
   filter or by citation alone must not enter unjudged). The calls are
   accounted in the `cost` record as the `screening` stage. `--no-screen`
   (or `NO_SCREEN=true`) skips the model: every candidate with a DOI counts as
   included, the selection still runs — an audit switch, with
   `--stop-after 1-discovery` a run spends no LLM call (give `--pubmed-query`
   to skip the query writer too).
4. **Selection.** Eligible candidates in three groups — trials (design `rct`
   by the tag or the screening guess), other primary studies, reviews (at most
   3) — each ordered by the *best rank across the channels* (a position in a
   ranked list means the same in Consensus and in PubMed); ties go to the
   paper more channels found, then to the lower Consensus query number, then to
   the one more reviews cite. Papers no ranking placed (found only by review
   references) follow the ranked ones of their group, more citing reviews
   first. The corpus is the first `MAX_PAPERS` (default 40) papers in that
   order: it caps what goes to the critic, not what discovery looks at. After
   the screening 12-44 on-topic papers per question remain (measured), so a
   lower cap cuts off studies.

Runs on a DOI list (mode C, `--dois`) skip all of this: the user's list is the
corpus as given. Keyword discovery (mode A) takes the top of the OpenAlex
ranking as before.

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
  abstract — inventing is forbidden; for a randomized trial the number
  randomized into the compared arms, not the screened or the non-randomized
  preference or registry participants, and for a cluster-randomized trial the
  randomized unit named in the concerns), strengths, concerns, relevance to the
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

**One report per trial.** Several publications of one trial (12-month
results, 5-year results, an HTA report) share their participants, so pooling
them as separate studies counts the same people twice: in a real run the
12-month and the 5-year report of one trial were pooled as k=2 and the
information size looked sufficient. Papers that share a trial registration
number (§2.1, `trial_registrations`) form one trial; a chain counts too
(report A names number X, B names X and Y, C names Y: one trial). Authors and
sample sizes are not used: matching by them was measured to merge different
trials. Reviews never join a trial (they are skipped from pooling anyway),
and a paper without a registration number is never merged with another.
Inside every candidate pool, before the pools are compared by their
participants, one report per trial stays: the latest by publication year, then
the larger by participants, then the lower DOI (so the choice does not depend
on the corpus order). The others go to `pooled_estimate.left_out` as
`doi (same trial as <kept doi>, <registration number>)` and the log. The same
rule applies where trials are counted: the hybrid full-text walk skips a
candidate whose trial already has a text, and the direction tally of the
direct studies counts each trial once with the direction of its latest report.
The synthesis input carries each study's `trial_registrations`, and the judge
is told that reports sharing a number are one trial, not replications.

The sample size the pool and the matrix use follows from the same concern: the
critic records, for a randomized trial, the number randomized into the compared
arms (never the screened participants or the non-randomized preference,
observational or registry arms — in the REFLUX trial the 453 preference
patients next to the 357 randomized), and the arm totals of the extracted
effect cover the compared groups only. For a non-randomized study it is the
number analysed in the compared groups. The pool's participants come from the
effect's own arm counts when it has them, from this field otherwise.

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
