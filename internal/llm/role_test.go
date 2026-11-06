package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRole(t *testing.T) {
	for _, r := range []Role{RolePlan, RoleCritique, RoleDeep, RoleSynthesis, RoleAsk} {
		got, err := ParseRole(string(r))
		require.NoError(t, err)
		assert.Equal(t, r, got)
	}

	for _, bad := range []string{"", "judge", "Plan"} {
		_, err := ParseRole(bad)
		require.ErrorContains(t, err, "unknown LLM role")
	}
}

//nolint:goconst // the literals are the spec table under test: a test against the constants would be a tautology
func TestRoleDefaults(t *testing.T) {
	tests := []struct {
		role   Role
		deep   bool
		model  string
		effort string
	}{
		{RolePlan, false, "sonnet", "low"},
		{RoleCritique, false, "opus", "low"},
		{RoleDeep, false, "opus", "high"},
		{RoleSynthesis, false, "opus", "high"},
		{RoleSynthesis, true, "fable", "high"},
		{RoleAsk, false, "sonnet", "medium"},
		// the --deep flag lifts only the synthesis
		{RolePlan, true, "sonnet", "low"},
		{RoleCritique, true, "opus", "low"},
		{RoleDeep, true, "opus", "high"},
		{RoleAsk, true, "sonnet", "medium"},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			assert.Equal(t, tt.model, tt.role.DefaultModel(tt.deep))
			assert.Equal(t, tt.effort, tt.role.DefaultEffort())
		})
	}
}

func TestFallbackModel(t *testing.T) {
	assert.Equal(t, "sonnet", FallbackModel("opus"))
	assert.Equal(t, "sonnet", FallbackModel("fable"))
	assert.Empty(t, FallbackModel("sonnet"))
	assert.Empty(t, FallbackModel("haiku"))
	assert.Empty(t, FallbackModel("glm-5.3"))
	assert.Empty(t, FallbackModel(""))
}
