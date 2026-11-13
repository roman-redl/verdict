package main

import (
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/roman-redl/verdict/internal/config"
	"github.com/roman-redl/verdict/internal/model"
)

// --lang is a flag of `run`, and an unknown language is refused before any
// client is built.
func TestRunLangFlag(t *testing.T) {
	var f runFlags

	cmd := &cobra.Command{Use: "run"}
	addRunFlags(cmd, &f)

	require.NoError(t, cmd.ParseFlags([]string{"--lang", "ru"}))
	assert.Equal(t, "ru", f.langFlag)

	c := &cli{}
	err := c.execute(t.Context(), &runFlags{langFlag: "de"})
	require.ErrorContains(t, err, "en | ru")
}

// `eval` has no --lang and ignores REPORT_LANG: its scoring parses the report,
// which stays English.
func TestEvalAlwaysReportsInEnglish(t *testing.T) {
	cmd := (&cli{}).evalCmd()
	assert.Nil(t, cmd.Flags().Lookup("lang"))

	f := evalFlags{set: filepath.Join(t.TempDir(), "missing.yaml"), modeFlag: "lean"}
	f.opts.Language = model.LangRU

	c := &cli{cfg: &config.Config{ReportLang: model.LangRU}}
	require.Error(t, c.evaluate(t.Context(), &f), "the question set does not exist: nothing is run")
	assert.Equal(t, model.LangEN, f.opts.Language)
}
