package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRecordAndRuns(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "verdict.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	base := Row{
		Topic: "keratosis", Run: "kp-light-full", Dir: "runs/keratosis/kp-light-full",
		Question: "does light therapy work", Verdict: "moderate", Consensus: "broad_agreement",
		Papers: 10, GeneratedAt: stamp(time.Now()),
	}
	if err := d.Record(base); err != nil {
		t.Fatal(err)
	}
	// a re-record of the same dir is an update, not a duplicate
	base.Verdict = "weak"
	if err := d.Record(base); err != nil {
		t.Fatal(err)
	}
	if err := d.Record(Row{Topic: "creatine", Run: "r2", Dir: "runs/creatine/r2",
		Verdict: "strong", GeneratedAt: stamp(time.Now())}); err != nil {
		t.Fatal(err)
	}

	all, err := d.Runs("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2 rows, got %d", len(all))
	}
	if all[0].Topic != "creatine" {
		t.Fatalf("newest first: %+v", all[0])
	}

	one, err := d.Runs("keratosis")
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].Verdict != "weak" || one[0].Papers != 10 {
		t.Fatalf("topic filter / upsert: %+v", one)
	}
}
