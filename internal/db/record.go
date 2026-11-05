package db

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/roman-redl/verdict/internal/pipeline"
)

// RecordRun indexes a finished run: the topic is the name of the directory the
// run lives in (runs/<topic>/<run>). A run without a report has no verdict to
// index.
func (db *DB) RecordRun(ctx context.Context, dir string, st *pipeline.State, now time.Time) error {
	if st.Report == nil {
		return errors.New("the run has no report to index")
	}

	return db.Record(ctx, Row{
		Topic:       filepath.Base(filepath.Dir(dir)),
		Run:         filepath.Base(dir),
		Dir:         dir,
		Question:    st.Question,
		Verdict:     st.Report.Verdict,
		Consensus:   st.Report.ConsensusStatus,
		Papers:      st.PaperCount(),
		GeneratedAt: now.UTC().Format(time.RFC3339),
	})
}
