package pipeline

import (
	"testing"

	"github.com/roman-redl/verdict/internal/model"
)

func TestDirectionTally(t *testing.T) {
	mk := func(relevance, direction string) model.Critique {
		return model.Critique{Relevance: relevance, ResultDirection: direction}
	}
	for _, tc := range []struct {
		name string
		in   []model.Critique
		want string
	}{
		{"no critiques", nil, ""},
		{"no direct studies", []model.Critique{mk(relevanceIndirect, directionPositive)}, ""},
		{"only direct counted", []model.Critique{
			mk(relevanceDirect, directionPositive), mk(relevanceIndirect, directionNegative),
			mk(relevanceDirect, directionNull), mk(relevanceDirect, directionNegative),
		}, "1 positive / 1 null / 1 negative"},
		{"missing direction is unclear", []model.Critique{
			mk(relevanceDirect, ""), mk(relevanceDirect, directionPositive),
		}, "1 positive / 1 unclear"},
		{"mixed shown when present", []model.Critique{
			mk(relevanceDirect, directionMixed), mk(relevanceDirect, directionMixed),
		}, "2 mixed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := directionTally(tc.in); got != tc.want {
				t.Fatalf("directionTally = %q, want %q", got, tc.want)
			}
		})
	}
}
