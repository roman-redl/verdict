package pipeline

// Restoring a missing SD (Cochrane Handbook 6.5.2.2, 6.5.2.3). A study that
// prints the SE of an arm's mean, or the 95% interval of it, instead of the SD
// still gives a usable arm. The critic only copies that statistic; Go derives
// the SD. The statistic must be the arm's own: the SE of the difference between
// the arms is never turned into an arm SD (the Handbook forbids it), and an SD
// is never imputed from other studies, so a study that gives none of these keeps
// the missing SD and drops out as before (6.5.2.7: do not impute when most
// studies lack SDs).

import (
	"github.com/roman-redl/verdict/internal/meta"
	"github.com/roman-redl/verdict/internal/model"
)

// Where a restored SD came from, as the log names it.
const (
	sdFromSE = "SE of the arm mean"
	sdFromCI = "95% CI of the arm mean"
)

// restoreSDs returns the effect with the SD of each arm restored that gives a
// mean and a size but no SD. The effect passed in is never modified: it is
// returned as it is when nothing was restored, else a copy is. Each restored SD
// is logged.
func (p *Pipeline) restoreSDs(doi string, ef *model.Effect) *model.Effect {
	if ef.Intervention == nil || ef.Control == nil {
		return ef
	}

	i, restoredI := p.restoreArmSD(doi, "intervention", ef.Intervention)
	c, restoredC := p.restoreArmSD(doi, "control", ef.Control)

	if !restoredI && !restoredC {
		return ef
	}

	out := *ef
	out.Intervention, out.Control = i, c

	return &out
}

// restoreArmSD is the arm with its SD restored, and whether that was done. An
// arm that states its SD, or lacks the mean or the size, or gives neither the SE
// nor the interval of its mean, stays as it is; so does an arm whose statistic
// cannot be an SE or an interval (logged).
func (p *Pipeline) restoreArmSD(doi, label string, a *model.Arm) (*model.Arm, bool) {
	out, from, err := restoredArm(a)
	if err != nil {
		p.log.Warn("arm SD not restored: unusable statistic", "doi", doi, "arm", label, "from", from, "err", err)

		return a, false
	}

	if out == a {
		return a, false
	}

	p.log.Info("arm SD derived", "doi", doi, "arm", label, "from", from, "n", *a.N, "sd", *out.SD)

	return out, true
}

// restoredArm is the arm with its SD restored, or the arm itself (the same
// pointer) when nothing was restored; from names the statistic the SD came
// from. An unusable statistic is an error and leaves the arm as it is.
func restoredArm(a *model.Arm) (out *model.Arm, from string, err error) {
	if a.SD != nil || a.Mean == nil || a.N == nil {
		return a, "", nil
	}

	sd, from, err := armSD(a)
	if err != nil || from == "" {
		return a, from, err
	}

	restored := *a
	restored.SD = &sd

	return &restored, from, nil
}

// withRestoredSDs is restoreSDs without the log, for the callers that only ask
// whether the numbers are usable: an arm whose statistic cannot be an SD
// stays without one, as in restoreSDs.
func withRestoredSDs(ef *model.Effect) *model.Effect {
	if ef.Intervention == nil || ef.Control == nil {
		return ef
	}

	// the errors are dropped on purpose: such an arm stays without an SD, and
	// deriveEffect then rejects the numbers
	i, _, _ := restoredArm(ef.Intervention)
	c, _, _ := restoredArm(ef.Control)

	out := *ef
	out.Intervention, out.Control = i, c

	return &out
}

// armSD derives the SD of an arm from its own SE, else from the 95% interval of
// its mean; from is empty when the arm gives neither.
func armSD(a *model.Arm) (sd float64, from string, err error) {
	switch {
	case a.SE != nil:
		sd, err = meta.SDFromSE(*a.SE, *a.N)

		return sd, sdFromSE, err
	case a.CILow != nil && a.CIHigh != nil:
		sd, err = meta.SDFromCI(*a.CILow, *a.CIHigh, *a.N)

		return sd, sdFromCI, err
	}

	return 0, "", nil
}
