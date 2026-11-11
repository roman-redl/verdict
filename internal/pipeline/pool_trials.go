package pipeline

import "fmt"

// dropRepeatReports keeps one study per trial in the group: reports of one
// trial (12 months, 5 years, an HTA report) share their participants, and
// pooling them as separate studies counts the same people twice. The study
// kept is the latest report (reportRank). The studies dropped come back keyed
// by their position in the poolBest input, each with the left_out text that
// names the kept report and the registration number that ties them.
func (g *poolGroup) dropRepeatReports() map[int]string {
	kept := map[int]int{} // trial -> position in g.items of the report to keep

	for pos := range g.items {
		it := &g.items[pos]
		if it.trial == 0 {
			continue
		}

		if cur, seen := kept[it.trial]; !seen || it.rank().keptOver(g.items[cur].rank()) {
			kept[it.trial] = pos
		}
	}

	var (
		items   []pooledStudy
		idx     []int
		dropped map[int]string
	)

	for pos := range g.items {
		it := g.items[pos]

		if keep, ok := kept[it.trial]; ok && keep != pos {
			if dropped == nil {
				dropped = map[int]string{}
			}

			winner := &g.items[keep]
			dropped[g.idx[pos]] = fmt.Sprintf("%s (same trial as %s, %s)",
				it.doi, winner.doi, sharedRegistration(it.regs, winner.regs))

			continue
		}

		items, idx = append(items, it), append(idx, g.idx[pos])
	}

	g.items, g.idx = items, idx

	return dropped
}
