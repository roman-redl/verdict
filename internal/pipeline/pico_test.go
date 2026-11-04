package pipeline

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/llm"
)

type fakeCompleter struct{ answer string }

func (f *fakeCompleter) Name() string { return "fake" }
func (f *fakeCompleter) Complete(_ context.Context, _, _ string, _ llm.Options) (string, llm.Usage, error) {
	return f.answer, llm.Usage{}, nil
}

func TestPICOFromQuestion(t *testing.T) {
	c := &fakeCompleter{answer: `{
		"question": "Does creatine improve cognitive performance in healthy adults?",
		"population": "healthy adults",
		"intervention": "creatine supplementation",
		"comparison": "placebo",
		"outcome": "cognitive performance (memory, executive function)",
		"keyword_query": "creatine cognition randomized",
		"semantic_queries": [
			"does creatine supplementation improve memory in healthy adults",
			"creatine and executive function: randomized trials"
		],
		"notes": "sleep-deprived populations show larger effects — maybe a separate run"
	}`}

	p, err := PICOFromQuestion(context.Background(), c, "creatine for the brain??")
	require.NoError(t, err)

	require.Equal(t, "healthy adults", p.Population)
	require.Len(t, p.SemanticQueries, 2)

	out := RenderPICO(p)
	require.Contains(t, out, "creatine cognition randomized", "render must carry both query flavors")
	require.Contains(t, out, "does creatine supplementation improve memory in healthy adults",
		"render must carry both query flavors")
}
