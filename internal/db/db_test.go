package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRecordAndRuns(t *testing.T) {
	d, err := Open(context.Background(), filepath.Join(t.TempDir(), "verdict.db"))
	require.NoError(t, err)

	defer func() { _ = d.Close() }()

	base := Row{
		Topic: "keratosis", Run: "kp-light-full", Dir: "runs/keratosis/kp-light-full",
		Question: "does light therapy work", Verdict: "moderate", Consensus: "broad_agreement",
		Papers: 10, GeneratedAt: stamp(time.Now()),
	}
	require.NoError(t, d.Record(context.Background(), base))

	// a re-record of the same dir is an update, not a duplicate
	base.Verdict = "weak"
	require.NoError(t, d.Record(context.Background(), base))

	require.NoError(t, d.Record(context.Background(), Row{
		Topic: "creatine", Run: "r2", Dir: "runs/creatine/r2",
		Verdict: "strong", GeneratedAt: stamp(time.Now()),
	}))

	all, err := d.Runs(context.Background(), "")
	require.NoError(t, err)

	require.Len(t, all, 2)
	require.Equal(t, "creatine", all[0].Topic, "newest first")

	one, err := d.Runs(context.Background(), "keratosis")
	require.NoError(t, err)

	require.Len(t, one, 1, "topic filter")
	require.Equal(t, "weak", one[0].Verdict, "upsert")
	require.Equal(t, 10, one[0].Papers, "upsert")
}
