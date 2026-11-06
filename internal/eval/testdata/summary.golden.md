# verdict eval (MedEvidence)

- Set: `evals/medevidence.yaml`, 3 question(s), 2 repeat(s) each
- Date: 2026-11-06 12:30
- Text mode: hybrid
- Models: llm-model=sonnet

## Totals

- Outcome match: 3/4 (75.0%)
- Strength exact (runs with an expert certainty): 1/3 (33.3%)
- Strength within one level (runs with an expert certainty): 2/3 (66.7%)
- Failed runs (no verdict, outside the figures above): 1 of 5
- Questions with the same verdict outcome in all 2 repeats: 1/3
- LLM calls: 12, input tokens: 1200000, output tokens: 15000

## Expected outcome by verdict outcome

Rows: the outcome the expert answer means for the claim. Columns: the outcome of verdict's report.

| Expected \ Verdict | works | no_effect | opposite | not_proven |
|---|---|---|---|---|
| works | 2 | 0 | 0 | 0 |
| no_effect | 1 | 0 | 0 | 0 |
| opposite | 0 | 0 | 0 | 0 |
| not_proven | 0 | 0 | 0 | 1 |

## Runs

| ID | Repeat | Specialty | Claim | Expert answer | Expected | Verdict outcome | Match | Direct tally | Effect direction | Consensus status | Certainty | Verdict level | Strength exact | Strength ±1 | Studies given | Studies in report | LLM calls | Input tokens | Output tokens | Report | Error |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 10 | 1 | Oncology & Hematology | In women with dense breasts, screening with mammography plus ultrasonography de… | higher | works | works | yes | direct 3: positive 2, null 1 | supports | broad_agreement | high | moderate | no | yes | 4 | 4 | 4 | 400000 | 5000 | 10-1/run/report.md |  |
| 10 | 2 | Oncology & Hematology | In women with dense breasts, screening with mammography plus ultrasonography de… | higher | works | works | yes | direct 2: positive 2 | - | emerging | high | strong | yes | yes | 4 | 4 | 4 | 400000 | 5000 | 10-2/run/report.md |  |
| 30 | 1 | Psychiatry & Neurology | In people with Parkinson's disease, exercise reduces fatigue severity. | no difference | no_effect | works | no | direct 1: positive 1 | supports | unknown | low | strong | no | no | 2 | 2 | 4 | 400000 | 5000 | 30-1/run/report.md |  |
| 30 | 2 | Psychiatry & Neurology | In people with Parkinson's disease, exercise reduces fatigue severity. | no difference | no_effect | - | - | - | - | - | low | - | - | - | 2 | - | - | - | - | - | stage 2-enrichment: HTTP 503 \| retry later second line |
| 230 | 1 | Psychiatry & Neurology | In cognitively healthy older people, computerised cognitive training improves g… | insufficient data | not_proven | not_proven | yes | direct 0 | unclear | insufficient_data | - | insufficient | - | - | 1 | 1 | - | - | - | 230-1/run/report.md |  |
