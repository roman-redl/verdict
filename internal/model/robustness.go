package model

// Robustness is the leave-one-out check of the pooled conclusion, computed by
// the pipeline: the pool is made again without each study in turn and the
// effect direction is derived the same way as for the whole pool. On thin
// evidence the critic's labels vary between runs, and one study entering or
// leaving the pool can change the conclusion; this shows how far it depends on
// any single one. It is information for the reader and is not a GRADE domain.
type Robustness struct {
	// Checked is the number of studies in the pool, each of which was left out in
	// turn (one for a single-study pool, which has nothing to leave out).
	Checked int `json:"checked"`
	// Direction is the effect direction of the whole pool the check compares
	// with; empty for a single-study pool. The report words an unclear one
	// differently: there is no direction to hold.
	Direction EffectDirection `json:"direction,omitempty"`
	// Decisive lists the studies whose absence changes the effect direction, in
	// the order of the pool; empty when the direction holds whichever study is
	// left out.
	Decisive []DecisiveStudy `json:"decisive,omitempty"`
	// SingleStudy is true when the pool is one study: the conclusion rests on
	// it by definition.
	SingleStudy bool `json:"single_study,omitempty"`
}

// DecisiveStudy is a study without which the effect direction changes.
type DecisiveStudy struct {
	// Study is the pool's name of the study (Studies of the pooled estimate).
	Study string `json:"study"`
	Title string `json:"title,omitempty"`
	// Participants is the size the study list shows (the critic's count, else the
	// metadata's), zero when the size is not stated; not the pool's size of the
	// compared arms.
	Participants int `json:"participants,omitempty"`
	// Weight is the study's share of the pooled estimate (0-1).
	Weight float64 `json:"weight"`
	// DirectionWithout is the effect direction of the pool without the study;
	// "unclear" also when the interval then excludes no effect but the direction
	// against the claim cannot be stated for want of a claim_change.
	DirectionWithout EffectDirection `json:"direction_without"`
}
