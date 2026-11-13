package pipeline

import (
	"fmt"

	"github.com/roman-redl/verdict/internal/model"
)

// english is the wording the report has always had; its output is pinned by
// the golden files.
func english() lexicon {
	studies := two("study", "studies")

	return lexicon{
		lang:      model.LangEN,
		formIndex: englishForm,

		reportTitle: "Evidence Report", question: "Question", verdict: "Verdict",
		effectDirection: "Effect direction", imprecision: "Imprecision (computed)",
		blindingBias: "Risk of bias from lack of blinding (computed)",
		comparable:   "Studies with numbers on the outcome", evidenceBase: "Evidence base",
		consensus: "Scientific consensus", directTally: "Direction of direct studies", generated: "Generated",
		rationale: "Rationale", matrix: "Evidence matrix", contradictions: "Contradictions",
		gaps: "Gaps and limitations", warnings: "Data warnings",
		matrixHeader: "| Study | Year | Design | N | Sup/Contra | Direction | Quality | Relevance | Note |",
		fullTexts:    "Full texts not obtained",
		fullTextsIntro: "These direct studies were critiqued from the abstract: no open-access full text was " +
			"obtained (in the hybrid mode only the pivotal studies are tried). To read one in full, download its PDF " +
			"and save it in the run directory as `fulltext/<file>` (or run `verdict addpdf --run-dir <run> <file.pdf>`, " +
			"which reads the DOI from the PDF itself), then rerun with `--resume`.",

		verdictNames: map[string]string{
			verdictStrong:       "STRONG — well-established evidence",
			verdictModerate:     "MODERATE — limited but consistent evidence",
			verdictWeak:         "WEAK — sparse or low-quality evidence",
			verdictInsufficient: "INSUFFICIENT — not enough evidence to judge",
		},
		consensusNames: map[string]string{
			consensusBroad:        "broad agreement",
			consensusContested:    "contested",
			consensusEmerging:     "emerging",
			consensusNone:         "no consensus",
			consensusInsufficient: "insufficient data for consensus",
		},
		effectNames: map[model.EffectDirection]string{
			model.EffectSupports: "supports the claim",
			model.EffectNoEffect: "no meaningful effect",
			model.EffectOpposite: "opposite effect",
			model.EffectUnclear:  "cannot be stated",
		},
		tallyNames: map[string]string{
			resultPositive: "positive", resultNull: "null", resultNegative: "negative",
			resultMixed: "mixed", valueUnclear: valueUnclear,
		},
		item: "%[1]d %[2]s",
		blindingNames: map[model.Blinding]string{
			model.BlindingDouble: "double-blind", model.BlindingSingle: "single-blind", model.BlindingOpen: "open-label",
			model.BlindingUnclear: "blinding not stated", "": "blinding not assessed",
		},

		pooledSingle: "Effect of the one direct study with numbers (not pooled)",
		pooledPlain:  "Pooled effect of the direct studies (random effects, DerSimonian-Laird)",
		pooledConverted: "Pooled effect of the direct studies (random effects, DerSimonian-Laird; " +
			"measures converted to one standardized scale)",
		ci: "95% CI", ciTo: " to ",
		studies: studies, levels: two("level", "levels"),
		participants: fixed("participants"), events: fixed("events"),
		design: "design", outcome: "outcome", notPooled: "not pooled", andMore: ", and %d more",
		strata: map[model.Stratum]string{
			model.StratumRandomized: "randomized", model.StratumNonRandomized: "non-randomized",
		},
		notRatedDown: "not rated down", ratedDown: "rated down", serious: "serious",

		sentence: englishSentence(studies),
		list:     englishList(),
	}
}

func englishSentence(studies forms) evidenceWords {
	return evidenceWords{
		reviewed: func(n int) string { return fmt.Sprintf("%d %s reviewed", n, plural(n, "paper", "papers")) },
		directHead: func(n int) string {
			return fmt.Sprintf("%d %s the question directly", n, plural(n, "answers", "answer"))
		},
		repeat: func(n int) string {
			return fmt.Sprintf(" (%d %s of the same trials counted once)", n, plural(n, "repeat report", "repeat reports"))
		},
		noneDirect:    "none answer the question directly.",
		randomized:    two("randomized trial", "randomized trials"),
		observational: two("observational study", "observational studies"),
		reviews:       two("systematic review or meta-analysis", "systematic reviews or meta-analyses"),
		other:         two("other publication", "other publications"),
		otherNote:     " (narrative reviews, guidelines, other or unclear designs)",
		sizeNone:      "size not stated", sizeNoneAll: "size not stated for all %d", sizeNoneFor: "size not stated for %d",
		median: "median", largest: "largest",
		noPool:       "Not pooled: no direct study with usable numbers on the outcome.",
		notPooledOne: "Not pooled: one study with numbers", pooledPrefix: "Pooled: ",
		studies: studies, trials: two("trial", "trials"),
		retracted: "Retracted, left out", indirect: "Indirect or off-topic", notAssessed: "Not assessed (critique failed)",
	}
}

func englishList() listWords {
	return listWords{
		header:      "Direct studies by number of participants (bar: log scale)",
		moreFmt:     "… and %d more",
		review:      "review",
		notAssessed: "not assessed",
		pooled:      "pooled",
		designs: map[model.StudyDesign]string{
			model.DesignRCT: "RCT", model.DesignClusterRCT: "cluster RCT", model.DesignCrossoverRCT: "crossover RCT",
			model.DesignNonrandomizedTrial: "non-randomized trial", model.DesignCohort: "cohort",
			model.DesignCaseControl: "case-control", model.DesignCrossSectional: "cross-sectional",
			model.DesignCaseSeries: "case series", model.DesignSystematicReview: "systematic review",
			model.DesignMetaAnalysis: "meta-analysis", model.DesignNarrativeReview: "narrative review",
			model.DesignGuideline: "guideline", model.DesignOther: "other design", model.DesignUnclear: "design unclear",
		},
		directions: map[model.StudyDirection]string{
			model.DirectionSupports: "supports", model.DirectionNull: "no effect", model.DirectionOpposite: "opposite",
			model.DirectionMixed: "mixed", model.DirectionUnclear: "unclear",
		},
	}
}
