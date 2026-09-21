package pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/roman-redl/verdict/internal/llm"
)

type fakeCompleter struct{ answer string }

func (f *fakeCompleter) Name() string { return "fake" }
func (f *fakeCompleter) Complete(_ context.Context, _, _ string, _ llm.Options) (string, error) {
	return f.answer, nil
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
	if err != nil {
		t.Fatal(err)
	}
	if p.Population != "healthy adults" || len(p.SemanticQueries) != 2 {
		t.Fatalf("bad parse: %+v", p)
	}
	out := RenderPICO(p)
	if !strings.Contains(out, "creatine cognition randomized") ||
		!strings.Contains(out, "does creatine supplementation improve memory in healthy adults") {
		t.Fatalf("render must carry both query flavors:\n%s", out)
	}
}
