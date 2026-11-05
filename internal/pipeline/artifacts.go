package pipeline

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slug is the filesystem-safe form of free text: lowercase letters and digits
// separated by single dashes.
func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// topicSlug is the filesystem-safe form of a topic name (shared by the run
// directory layout and the enrichment cache path).
func topicSlug(t string) string { return slug(t) }

// prepareDir returns the run's artifact directory, creating it: an explicit
// RunDir, else runs/<topic>/<slug of the question>-<time>.
func (p *Pipeline) prepareDir(opts Options) (string, error) {
	if opts.RunDir != "" {
		if err := os.MkdirAll(opts.RunDir, 0o750); err != nil {
			return "", fmt.Errorf("creating %s: %w", opts.RunDir, err)
		}

		return opts.RunDir, nil
	}

	name := slug(cmp.Or(opts.Question, opts.DOIFile, "run"))
	if len(name) > 40 {
		name = name[:40]
	}

	if name == "" {
		name = "run"
	}

	parts := []string{"runs"}
	if topic := topicSlug(opts.Topic); topic != "" {
		parts = append(parts, topic)
	}

	dir := filepath.Join(append(parts, name+"-"+time.Now().Format("20060102-150405"))...)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}

	return dir, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func saveJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", path, err)
	}

	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

func loadJSON(path string, v any) error {
	//nolint:gosec // the path is an internal run artifact (a stage JSON), not external input
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}

	return nil
}
