package pipeline

// The English wording under test, spelled as the plain functions the tests of
// the report used before the wording moved into the lexicon: each test of a
// sentence or a label reads the English lexicon, and
// report_ru_test.go covers the other language.

import "github.com/roman-redl/verdict/internal/model"

// noPoolText is the English summary line when the report has no pooled estimate.
const noPoolText = "Not pooled: no direct study with usable numbers on the outcome."

// evidenceBaseEN builds an evidence base with its English summary.
func evidenceBaseEN(st *State, pe *model.PooledEstimate) *model.EvidenceBase {
	return evidenceBaseFor(st, pe, model.LangEN)
}

func directionTally(st *State) string { return english().directionTally(st) }

func blindingText(g *model.EvidenceGroup) string { return english().blindingText(g) }

func sizeText(g *model.EvidenceGroup) []string { return english().sizeText(g) }

func pooledSentence(pe *model.PooledEstimate) string { return english().pooledSentence(pe) }

func evidenceText(eb *model.EvidenceBase, pe *model.PooledEstimate) string {
	return english().evidenceText(eb, pe)
}

func pooledText(pe *model.PooledEstimate) string { return english().pooledText(pe) }

func pooledLine(pe *model.PooledEstimate) string { return english().pooledLine(pe) }

func pooledLabel(pe *model.PooledEstimate) string { return english().pooledLabel(pe) }

func blindingBiasText(bb *model.BlindingBias) string { return english().blindingBiasText(bb) }

func imprecisionText(imp *model.Imprecision) string { return english().imprecisionText(imp) }

func effectDirectionLabel(d model.EffectDirection) string { return english().effectDirectionLabel(d) }

func effectDirectionSentence(d model.EffectDirection) string {
	return english().effectDirectionSentence(d)
}

func verdictLabel(v string) string { return english().verdictLabel(v) }

func consensusLabel(s string) string { return english().consensusLabel(s) }
