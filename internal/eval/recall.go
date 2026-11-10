package eval

import (
	"strconv"

	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/pipeline"
)

// Recall is how many of the review's studies a search run found, at each
// stage of the pipeline. Total is the number of the review's studies that have
// a DOI (a study without one cannot be matched); every other field counts
// those studies.
type Recall struct {
	Total int
	// Pool counts the studies anywhere in the discovery candidate pool.
	Pool int
	// Screened counts the studies the screening let through (Candidate.Eligible:
	// an unscreened Consensus hit stays in, as in the selection).
	Screened int
	// Corpus counts the studies in the final corpus given to the critic.
	Corpus int
}

// recallOf measures the search of a finished run against the review's
// studies. It reads only the state: the pool and its screening decisions of
// the discovery artifact, and the corpus (Papers). DOIs are compared
// normalized, so the form a source wrote (case, URL prefix) does not matter.
func recallOf(q *Question, st *pipeline.State) Recall {
	want := map[string]bool{}

	for _, d := range q.DOIs() {
		want[d] = true
	}

	r := Recall{Total: len(want)}
	pool, screened := map[string]bool{}, map[string]bool{}

	if st.Discovery != nil {
		for i := range st.Discovery.Pool {
			c := &st.Discovery.Pool[i]

			d := model.NormalizeDOI(c.DOI)
			if !want[d] {
				continue
			}

			pool[d] = true

			if c.Eligible() {
				screened[d] = true
			}
		}
	}

	corpus := map[string]bool{}

	for i := range st.Papers {
		if d := model.NormalizeDOI(st.Papers[i].DOI); want[d] {
			corpus[d] = true
		}
	}

	r.Pool, r.Screened, r.Corpus = len(pool), len(screened), len(corpus)

	return r
}

// add sums the counts of another recall: the pooled recall of an eval.
func (r *Recall) add(o Recall) {
	r.Total += o.Total
	r.Pool += o.Pool
	r.Screened += o.Screened
	r.Corpus += o.Corpus
}

// cells are the three stage counts as "found/total" for the summary tables.
func (r *Recall) cells() []string {
	f := func(n int) string { return strconv.Itoa(n) + "/" + strconv.Itoa(r.Total) }

	return []string{f(r.Pool), f(r.Screened), f(r.Corpus)}
}
