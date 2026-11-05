package llm

import "fmt"

// Role is the job an LLM call does; every role has its own default model and
// reasoning effort, so a cheap model reads the abstracts and a strong one
// writes the verdict.
type Role string

const (
	// RolePlan turns a messy description into questions (brief, pico).
	RolePlan Role = "plan"
	// RoleCritique is the per-paper methodology review.
	RoleCritique Role = "critique"
	// RoleDeep re-critiques the pivotal studies once their full texts are in.
	RoleDeep Role = "deep"
	// RoleSynthesis is the judge that writes the verdict.
	RoleSynthesis Role = "synthesis"
	// RoleAsk answers questions about a finished run.
	RoleAsk Role = "ask"
)

// The model aliases resolve in every wrapper: the GLM and DeepSeek wrappers
// map them through ANTHROPIC_DEFAULT_*_MODEL, claude-max uses them as is.
const (
	ModelSonnet = "sonnet"
	ModelOpus   = "opus"
	ModelFable  = "fable"
)

// Reasoning effort levels of the claude-max wrapper.
const (
	effortLow    = "low"
	effortMedium = "medium"
	effortHigh   = "high"
)

// ParseRole parses a role name at the boundary.
func ParseRole(s string) (Role, error) {
	r := Role(s)

	switch r {
	case RolePlan, RoleCritique, RoleDeep, RoleSynthesis, RoleAsk:
		return r, nil
	}

	return "", fmt.Errorf("unknown LLM role %q (want plan | critique | deep | synthesis | ask)", s)
}

// DefaultModel is the model alias of the role when nothing overrides it.
// deep (the run's --deep flag) lifts the synthesis to the strongest model.
func (r Role) DefaultModel(deep bool) string {
	switch r {
	case RolePlan, RoleCritique, RoleAsk:
		return ModelSonnet
	case RoleDeep:
		return ModelOpus
	case RoleSynthesis:
		if deep {
			return ModelFable
		}

		return ModelOpus
	}

	// unreachable: a Role comes from the constants or ParseRole
	return ModelSonnet
}

// DefaultEffort is the reasoning effort of the role; only claude-max takes it.
func (r Role) DefaultEffort() string {
	switch r {
	case RolePlan, RoleCritique:
		return effortLow
	case RoleAsk:
		return effortMedium
	case RoleDeep, RoleSynthesis:
		return effortHigh
	}

	// unreachable: a Role comes from the constants or ParseRole
	return effortMedium
}

// FallbackModel is the model the CLI falls back to when the strong one is
// overloaded; the cheaper alias keeps a run going instead of failing it.
func FallbackModel(model string) string {
	if model == ModelOpus || model == ModelFable {
		return ModelSonnet
	}

	return ""
}
