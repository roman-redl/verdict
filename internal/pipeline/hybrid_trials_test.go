package pipeline

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two reports of one trial need one full text: once the first has it, the
// second's slot passes on to the next candidate.
func TestFetchPivotalTextsSkipsAnotherReportOfAnObtainedTrial(t *testing.T) {
	const doiD = "10.1/d"

	for _, tc := range []struct {
		name         string
		texts        map[string]string // Europe PMC: the DOIs that have an open text
		placed       []int             // studies that already have their text
		reports      map[int][]string  // the registration numbers of the studies, by position
		wantTried    int
		wantObtained int
		wantFetched  int
		wantAsked    []string
	}{
		{
			name:         "the second report of an obtained trial is skipped",
			texts:        map[string]string{doiA: "A", doiB: "B", doiC: "C", doiD: "D"},
			reports:      map[int][]string{0: {regReflux}, 1: {regReflux}},
			wantTried:    2, // a and c: b shares a's trial
			wantObtained: 2, wantFetched: 2,
			wantAsked: []string{doiA, doiC},
		},
		{
			name:         "a trial whose first report is paywalled still gets its second report read",
			texts:        map[string]string{doiB: "B", doiC: "C"},
			reports:      map[int][]string{0: {regReflux}, 1: {regReflux}},
			wantTried:    3, // a (paywalled), b, then c
			wantObtained: 2, wantFetched: 2,
			wantAsked: []string{doiA, doiB, doiC},
		},
		{
			name:         "a text that is already there counts for its trial",
			texts:        map[string]string{doiA: "A", doiB: "B", doiC: "C"},
			placed:       []int{0},
			reports:      map[int][]string{0: {regReflux}, 1: {regReflux}},
			wantTried:    1, // c only: a is placed, b repeats a's trial
			wantObtained: 2, wantFetched: 1,
			wantAsked: []string{doiC},
		},
		{
			name:         "different registration numbers are different trials",
			texts:        map[string]string{doiA: "A", doiB: "B", doiC: "C"},
			reports:      map[int][]string{0: {regNCT1}, 1: {regNCT2}},
			wantTried:    2,
			wantObtained: 2, wantFetched: 2,
			wantAsked: []string{doiA, doiB},
		},
		{
			name:         "papers without registration numbers are fetched as before",
			texts:        map[string]string{doiA: "A", doiB: "B", doiC: "C"},
			wantTried:    2,
			wantObtained: 2, wantFetched: 2,
			wantAsked: []string{doiA, doiB},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xml := &fakeFullTexts{texts: tc.texts}
			st := fetchTestState(doiA, doiB, doiC, doiD) // the sizes put them in this order

			for i, ids := range tc.reports {
				st.Enriched[i].TrialRegistrations = ids
			}

			for _, i := range tc.placed {
				st.Enriched[i].FullText = "PLACED"
			}

			p := newTest(t, Settings{}, Clients{FullTexts: xml}, quietLog())

			got, err := p.fetchPivotalTexts(t.Context(), params(Options{RunDir: t.TempDir()}), st, 2)

			require.NoError(t, err)
			assert.Equal(t, pivotalFetch{tried: tc.wantTried, obtained: tc.wantObtained, fetched: tc.wantFetched}, got)

			for _, doi := range []string{doiA, doiB, doiC, doiD} {
				want := 0
				if slices.Contains(tc.wantAsked, doi) {
					want = 1
				}

				assert.Equal(t, want, xml.calls.get("FullText:"+doi), "Europe PMC lookups for %s", doi)
			}
		})
	}
}
