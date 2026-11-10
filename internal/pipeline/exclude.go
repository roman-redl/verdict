package pipeline

// Excluded DOIs (run --exclude-dois): papers a run must not see. Measuring
// the search recall against a systematic review needs the review itself out of
// the pool — otherwise mining its reference list finds its own included
// studies and the recall is circular.

import (
	"regexp"
	"slices"
	"strings"

	"github.com/roman-redl/verdict/internal/model"
)

// cochraneDOIRe matches a Cochrane review DOI in its normalized (lowercase)
// form: the CD number is group 1, the .pubN version suffix is optional.
var cochraneDOIRe = regexp.MustCompile(`^10\.1002/14651858\.(cd\d+)(?:\.pub\d+)?$`)

// doiExclusion is the set of DOIs a run drops. A paper matches by normalized
// DOI equality; a Cochrane review DOI excludes every version of its CD number
// (any .pubN, or none), because the pool may hold another version than the one
// the user named. It also records which candidates it dropped. A nil
// *doiExclusion excludes nothing; it is not safe for concurrent use (the pool
// is built on one goroutine).
type doiExclusion struct {
	listed   []string        // the requested DOIs, normalized, in the given order
	exact    map[string]bool // normalized DOIs
	cochrane map[string]bool // CD numbers, lowercase
	dropped  []string        // distinct DOIs of the dropped candidates, in the order dropped
	seen     map[string]bool
}

// newDOIExclusion builds the exclusion from flag values: each is one DOI
// (any prefix or URL form) or a comma-separated list of them. It returns nil
// when no usable value is given.
func newDOIExclusion(values []string) *doiExclusion {
	e := &doiExclusion{exact: map[string]bool{}, cochrane: map[string]bool{}, seen: map[string]bool{}}

	for _, v := range values {
		for part := range strings.SplitSeq(v, ",") {
			doi := model.NormalizeDOI(part)
			if doi == "" || e.exact[doi] {
				continue
			}

			e.exact[doi] = true
			e.listed = append(e.listed, doi)

			if m := cochraneDOIRe.FindStringSubmatch(doi); m != nil {
				e.cochrane[m[1]] = true
			}
		}
	}

	if len(e.listed) == 0 {
		return nil
	}

	return e
}

// matches reports whether the DOI is excluded.
func (e *doiExclusion) matches(doi string) bool {
	if e == nil {
		return false
	}

	doi = model.NormalizeDOI(doi)
	if doi == "" {
		return false
	}

	if e.exact[doi] {
		return true
	}

	if m := cochraneDOIRe.FindStringSubmatch(doi); m != nil {
		return e.cochrane[m[1]]
	}

	return false
}

// drop reports whether the candidate with this DOI is excluded and, when it
// is, records it (once per DOI) for the discovery artifact.
func (e *doiExclusion) drop(doi string) bool {
	if !e.matches(doi) {
		return false
	}

	doi = model.NormalizeDOI(doi)
	if !e.seen[doi] {
		e.seen[doi] = true
		e.dropped = append(e.dropped, doi)
	}

	return true
}

// recordExclusion writes the exclusion into the discovery artifact and logs
// what it dropped; a run without --exclude-dois leaves both untouched.
func (p *Pipeline) recordExclusion(info *DiscoveryInfo, excl *doiExclusion) {
	if excl == nil {
		return
	}

	excl.record(info)
	p.log.Info("excluded DOIs dropped from the discovery", "requested", len(info.ExcludeDOIs),
		"dropped", info.ExcludedCount, "dois", info.ExcludedDOIs)
}

// dropExcludedDOIs removes the excluded DOIs from a user-supplied list. A DOI
// both given and excluded is excluded, with a warning: the user most likely
// pasted the excluded review into the list by mistake.
func (p *Pipeline) dropExcludedDOIs(dois []string, excl *doiExclusion) []string {
	return slices.DeleteFunc(dois, func(doi string) bool {
		if !excl.drop(doi) {
			return false
		}

		p.log.Warn("a DOI of the input list is also excluded by --exclude-dois: excluding it", "doi", doi)

		return true
	})
}

// record writes the requested list, the dropped DOIs and their count into the
// discovery artifact; it writes nothing for a nil exclusion.
func (e *doiExclusion) record(info *DiscoveryInfo) {
	if e == nil {
		return
	}

	info.ExcludeDOIs = e.listed
	info.ExcludedDOIs = e.dropped
	info.ExcludedCount = len(e.dropped)
}
