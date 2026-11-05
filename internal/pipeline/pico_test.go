package pipeline

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPICOFromQuestion(t *testing.T) {
	c := &scriptCompleter{replies: []string{`{
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
	}`}}

	p, err := PICOFromQuestion(context.Background(), c, "creatine for the brain??")
	require.NoError(t, err)

	require.Equal(t, "healthy adults", p.Population)
	require.Len(t, p.SemanticQueries, 2)

	out := RenderPICO(p)
	require.Contains(t, out, "creatine cognition randomized", "render must carry both query flavors")
	require.Contains(t, out, "does creatine supplementation improve memory in healthy adults",
		"render must carry both query flavors")
}
