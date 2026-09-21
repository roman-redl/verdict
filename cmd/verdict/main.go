// verdict — an orchestrator for evidence assessment of scientific claims:
// discovery (Consensus paste / OpenAlex) → smart citations and retraction
// checks (Scite/OpenAlex) → LLM verdict with an evidence matrix.
package main

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"

	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/ergochat/readline"
	"github.com/spf13/cobra"

	"github.com/roman-redl/verdict/internal/app"
	"github.com/roman-redl/verdict/internal/config"
	"github.com/roman-redl/verdict/internal/db"
	"github.com/roman-redl/verdict/internal/llm"
	"github.com/roman-redl/verdict/internal/model"
	"github.com/roman-redl/verdict/internal/openalex"
	"github.com/roman-redl/verdict/internal/pipeline"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	var opts pipeline.Options
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Full pipeline for a research question or a list of DOIs",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Load()
			clients, err := app.BuildClients(cfg, log, opts)
			if err != nil {
				return err
			}

			st, dir, err := pipeline.New(cfg, clients, log).Run(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if st.Report != nil {
				fmt.Printf("Verdict: %s\nReport: %s/report.md\nMatrix: %s/matrix.csv\n",
					st.Report.Verdict, dir, dir)
				recordRun(dir, st, paperCount(st))
			} else {
				fmt.Printf("Stage artifacts: %s\n", dir)
			}
			return nil
		},
	}
	runCmd.Flags().StringVarP(&opts.Question, "question", "q", "",
		"research question, e.g.: does creatine improve cognitive performance")
	runCmd.Flags().StringVar(&opts.DOIFile, "dois", "",
		"file with a list of DOIs (one per line) — the manual-corpus mode")
	runCmd.Flags().StringVar(&opts.Topic, "topic", "",
		"group the run under runs/<topic>/ (one topic per research problem)")
	runCmd.Flags().StringVar(&opts.RunDir, "run-dir", "",
		"artifact directory (overrides --topic and the default runs/<topic>/<slug>-<time>)")
	runCmd.Flags().BoolVar(&opts.Resume, "resume", false,
		"resume a failed run from the stage artifacts")
	runCmd.Flags().StringVar(&opts.StopAfter, "stop-after", "",
		"stop after a stage: 1..4 or the full name (e.g. 2-enrichment)")
	runCmd.Flags().IntVar(&opts.MaxPapers, "max-papers", 0,
		"how many papers to take from discovery (defaults to MAX_PAPERS from env)")
	runCmd.Flags().IntVar(&opts.Concurrency, "concurrency", 0,
		"enrichment parallelism (defaults to CONCURRENCY from env)")
	runCmd.Flags().IntVar(&opts.LLMConcurrency, "llm-concurrency", 0,
		"LLM stage parallelism (defaults to LLM_CONCURRENCY from env)")
	runCmd.Flags().StringVar(&opts.LLMCommand, "llm-command", "",
		"claude-cli wrapper for this run (default comes from .env)")
	runCmd.Flags().StringVar(&opts.LLMModel, "llm-model", "",
		"exact model name for both LLM stages (empty = wrapper default)")
	runCmd.Flags().StringVar(&opts.ModelCritique, "model-critique", "",
		"critique-stage model override, exact name (cheap critic + strong judge)")
	runCmd.Flags().StringVar(&opts.ModelSynthesis, "model-synthesis", "",
		"synthesis-stage model override, exact name")
	runCmd.Flags().StringVar(&opts.ChallengeNote, "challenge-note", "",
		"counterargument to weigh and answer in the verdict rationale (not data)")
	runCmd.Flags().BoolVar(&opts.FullText, "fulltext", true,
		"download OA PDFs so the critic reads full texts where a legal copy exists (on by default; --fulltext=false to skip)")

	root := &cobra.Command{
		Use:   "verdict",
		Short: "Evidence assessment for scientific claims: discovery → Scite/OpenAlex → LLM",
	}
	root.AddCommand(runCmd, webCmd(), doisCmd(), resolveCmd(), snowballCmd(), addpdfCmd(), askCmd(), briefCmd(), picoCmd(), indexCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// webCmd — discovery mode C (the free bridge): open the Consensus web UI
// with the question ready to paste, pick papers by hand (scroll to the end
// of the list), copy them and feed the pipeline via dois/resolve.
func webCmd() *cobra.Command {
	var question string
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Open the Consensus web UI with the question ready to paste (free: manual paper selection)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if question == "" {
				return fmt.Errorf("--question/-q is required")
			}
			fmt.Println("Run this question in Consensus (consensus.app):")
			fmt.Println()
			fmt.Println("  " + question)
			fmt.Println()
			opener := "xdg-open"
			if runtime.GOOS == "darwin" {
				opener = "open"
			}
			if err := exec.Command(opener, "https://consensus.app/").Start(); err != nil {
				fmt.Println("(the browser did not open — open consensus.app yourself)")
			}
			fmt.Println("Next steps:")
			fmt.Println("  1) scroll the results to the end (\"Load more\" caps at ~50), then")
			fmt.Println("     select and copy the whole list — raw paste back is fine")
			fmt.Println("  2) pbpaste | bin/verdict dois > dois.txt        (DOIs present)")
			fmt.Println("     pbpaste | bin/verdict resolve > dois.txt    (titles only)")
			fmt.Println("  3) bin/verdict run --dois dois.txt -q \"" + question + "\"")
			return nil
		},
	}
	cmd.Flags().StringVarP(&question, "question", "q", "", "research question")
	return cmd
}

// doisCmd — clean arbitrary copied text down to a list of DOIs.
func doisCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dois [file]",
		Short: "Extract DOIs from arbitrary text (clipboard dump, links, a table) into a clean list",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input, err := readInput(cmd, args)
			if err != nil {
				return err
			}
			dois := pipeline.ExtractDOIs(string(input))
			if len(dois) == 0 {
				return fmt.Errorf("no DOIs found")
			}
			for _, d := range dois {
				fmt.Println(d)
			}
			return nil
		},
	}
}

// resolveCmd — titles → DOIs via OpenAlex title search: the fallback for
// pastes that carry no DOIs (mode C without the clipboard luck). Lines that
// already contain DOIs pass through; every match prints its own title so the
// result is verifiable at a glance.
func resolveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resolve [file]",
		Short: "Resolve paper titles to DOIs via OpenAlex (for DOI-less pastes)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input, err := readInput(cmd, args)
			if err != nil {
				return err
			}
			oa := openalex.New(config.Load().OpenAlexEmail)
			seen := map[string]bool{}
			resolved := 0
			for _, line := range strings.Split(string(input), "\n") {
				line = stripBullet(line)
				if line == "" || seen[line] {
					continue
				}
				seen[line] = true
				if len(pipeline.ExtractDOIs(line)) > 0 {
					fmt.Println(line) // already has a DOI — pass through
					continue
				}
				if len([]rune(line)) < 12 {
					continue // junk line, not a title
				}
				w, err := oa.SearchTitle(cmd.Context(), line)
				switch {
				case err != nil:
					fmt.Printf("UNRESOLVED (%v): %s\n", err, line)
				case w == nil:
					fmt.Printf("UNRESOLVED: %s\n", line)
				default:
					marker := "approx"
					if strings.EqualFold(w.DisplayName, line) {
						marker = "exact"
					}
					resolved++
					fmt.Printf("%s\t%d\t%s\t%s\n", model.NormalizeDOI(w.DOI), w.PublicationYear, marker, w.DisplayName)
				}
			}
			fmt.Fprintf(os.Stderr, "resolved %d title(s); UNRESOLVED lines need a manual lookup\n", resolved)
			return nil
		},
	}
}

var bulletRe = regexp.MustCompile(`^\s*(?:[-*•]|\d+[.)])\s+`)

func stripBullet(line string) string {
	return strings.TrimSpace(bulletRe.ReplaceAllString(line, ""))
}

// snowballCmd — citation-chaining corpus expansion: finds works connected to
// several seed papers at once (cited by them or citing them). Prints
// candidates for curation; nothing is added to any corpus automatically.
func snowballCmd() *cobra.Command {
	var minShared, maxOut int
	cmd := &cobra.Command{
		Use:   "snowball [file]",
		Short: "Expand a DOI list via citation chaining (co-citation candidates for curation)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input, err := readInput(cmd, args)
			if err != nil {
				return err
			}
			dois := pipeline.ExtractDOIs(string(input))
			if len(dois) < 2 {
				return fmt.Errorf("snowball needs at least 2 seed DOIs, got %d", len(dois))
			}
			oa := openalex.New(config.Load().OpenAlexEmail)
			cands, err := pipeline.Snowball(cmd.Context(), oa, dois, minShared, maxOut)
			if err != nil {
				return err
			}
			if len(cands) == 0 {
				fmt.Println("no candidates shared by several seeds — the corpus looks complete")
				return nil
			}
			fmt.Println("doi\tyear\tcited-by-seeds\tcites-seeds\ttitle")
			for _, c := range cands {
				fmt.Printf("%s\t%d\t%d\t%d\t%s\n", c.DOI, c.Year, c.CitedBySeeds, c.CitesSeeds, c.Title)
			}
			fmt.Fprintln(os.Stderr, "curate manually: shared-citation counts are a signal, not a relevance verdict")
			return nil
		},
	}
	cmd.Flags().IntVar(&minShared, "min-shared", 2, "how many seeds a candidate must connect to")
	cmd.Flags().IntVar(&maxOut, "max", 10, "how many candidates to print")
	return cmd
}

// readInput reads the command input: a file path, "-" or no argument =
// stdin (the shared convention of dois/resolve/brief).
func readInput(cmd *cobra.Command, args []string) ([]byte, error) {
	if len(args) == 1 && args[0] != "-" {
		return os.ReadFile(args[0])
	}
	return io.ReadAll(cmd.InOrStdin())
}

// askCmd — Q&A with an analyst holding the run's context: the same LLM
// backend the pipeline used (by default the Claude Code tariff). REPL or a
// single question.
func askCmd() *cobra.Command {
	var runDir, question string
	cmd := &cobra.Command{
		Use:   "ask",
		Short: "Ask an analyst about a finished run (papers, citations, verdict)",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := slog.New(slog.NewTextHandler(os.Stderr, nil))
			cfg := config.Load()
			completer, err := app.BuildCompleter(cfg, log, "")
			if err != nil {
				return err
			}
			var runs []pipeline.RunState
			runs, err = pipeline.LoadRuns(runDir)
			if err != nil {
				return err
			}
			system := pipeline.BuildAskSystem(runs)
			fmt.Println("Loaded runs:")
			for _, r := range runs {
				fmt.Printf("  - %s (%d papers) — %s\n", r.Name, paperCount(r.State), shorten(r.State.Question, 80))
			}
			if question != "" {
				ans, err := completer.Complete(cmd.Context(), system, question, llm.Options{})
				if err != nil {
					return err
				}
				fmt.Println(renderAnswer(ans))
				return nil
			}
			fmt.Println("Ask anything about the runs. exit / Ctrl-D to quit; empty Enter just re-prompts.")
			transcript := ""
			rl, err := readline.NewEx(&readline.Config{
				Prompt:      "❯ ",
				HistoryFile: filepath.Join(os.TempDir(), "verdict-ask-history"),
			})
			if err != nil {
				return err
			}
			defer rl.Close()
			for {
				line, err := rl.Readline()
				if err != nil { // Ctrl-D (io.EOF) or a reader failure
					break
				}
				q := strings.TrimSpace(line)
				if q == "" {
					continue
				}
				if q == "exit" || q == "quit" {
					break
				}
				user := transcript + "\n\nQuestion: " + q
				fmt.Println("…thinking (up to a couple of minutes)…")
				ans, err := completer.Complete(cmd.Context(), system, user, llm.Options{})
				if err != nil {
					fmt.Fprintln(os.Stderr, "call failed:", err)
					fmt.Fprintln(os.Stderr, "continuing — please repeat the question")
					continue
				}
				fmt.Println("\n" + renderAnswer(ans) + "\n")
				transcript = user + "\nAnswer: " + ans
				// the dialogue history is carried into the next questions;
				// the tail is capped so it does not grow forever
				if len(transcript) > 24000 {
					transcript = transcript[len(transcript)-24000:]
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&runDir, "run-dir", "",
		"a topic directory (loads all its runs, newest first) or a single run directory")
	_ = cmd.MarkFlagRequired("run-dir")
	cmd.Flags().StringVarP(&question, "question", "q", "", "a single question, no interactive mode")
	return cmd
}

func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// recordRun indexes the finished run in the SQLite run database (best
// effort — the stage artifacts on disk remain the source of truth).
func recordRun(dir string, st *pipeline.State, papers int) {
	d, err := db.Open(filepath.Join("runs", "verdict.db"))
	if err != nil {
		return
	}
	defer d.Close()
	_ = d.Record(db.Row{
		Topic:       filepath.Base(filepath.Dir(dir)),
		Run:         filepath.Base(dir),
		Dir:         dir,
		Question:    st.Question,
		Verdict:     st.Report.Verdict,
		Consensus:   st.Report.ConsensusStatus,
		Papers:      papers,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// addpdfCmd — drop a paper PDF (e.g. straight from ~/Downloads) into a
// run's fulltext/ slot: the DOI is read from the PDF itself, so no manual
// renaming. The next run over that directory gets the full text.
func addpdfCmd() *cobra.Command {
	var runDir string
	cmd := &cobra.Command{
		Use:   "addpdf <file.pdf> [more.pdf...]",
		Short: "Drop paper PDF(s) into a run (the DOI is read from the PDF itself)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := filepath.Join(runDir, "fulltext")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			for _, f := range args {
				b, err := os.ReadFile(f)
				if err != nil {
					return err
				}
				if !bytes.HasPrefix(b, []byte("%PDF")) {
					return fmt.Errorf("%s is not a PDF", f)
				}
				dois := pipeline.PDFDOIs(b)
				if len(dois) == 0 {
					return fmt.Errorf("%s: no DOI found inside the text — place it manually as %s/<doi>.pdf", f, dir)
				}
				name := strings.ReplaceAll(model.NormalizeDOI(dois[0]), "/", "_") + ".pdf"
				dst := filepath.Join(dir, name)
				if err := os.WriteFile(dst, b, 0o644); err != nil {
					return err
				}
				fmt.Printf("%s -> %s (%s)\n", f, dst, dois[0])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&runDir, "run-dir", "", "the run directory (runs/<topic>/<run>)")
	_ = cmd.MarkFlagRequired("run-dir")
	return cmd
}

// indexCmd — the run index: topics, verdicts and paper counts without
// walking runs/ on disk.
func indexCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "index [topic]",
		Short: "List indexed runs (topics, verdicts, paper counts)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			topic := ""
			if len(args) == 1 {
				topic = args[0]
			}
			d, err := db.Open(filepath.Join("runs", "verdict.db"))
			if err != nil {
				return err
			}
			defer d.Close()
			rows, err := d.Runs(topic)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				fmt.Println("no indexed runs")
				return nil
			}
			fmt.Println("topic\trun\tverdict\tconsensus\tpapers\tquestion")
			for _, r := range rows {
				fmt.Printf("%s\t%s\t%s\t%s\t%d\t%s\n",
					orDash(r.Topic), r.Run, orDash(r.Verdict), orDash(r.Consensus), r.Papers, shorten(r.Question, 60))
			}
			return nil
		},
	}
}

// picoCmd — question normalization before discovery: a messy question
// becomes PICO components plus ready-to-run queries — a PubMed-style
// keyword query (mode A) and 1-3 semantic queries (mode C, Consensus).
func picoCmd() *cobra.Command {
	var question string
	cmd := &cobra.Command{
		Use:   "pico",
		Short: "Normalize a question into PICO + ready keyword/semantic queries",
		RunE: func(cmd *cobra.Command, args []string) error {
			if question == "" {
				return fmt.Errorf("--question/-q is required")
			}
			log := slog.New(slog.NewTextHandler(os.Stderr, nil))
			cfg := config.Load()
			completer, err := app.BuildCompleter(cfg, log, "")
			if err != nil {
				return err
			}
			p, err := pipeline.PICOFromQuestion(cmd.Context(), completer, question)
			if err != nil {
				return err
			}
			fmt.Println(pipeline.RenderPICO(p))
			return nil
		},
	}
	cmd.Flags().StringVarP(&question, "question", "q", "", "the research question (any form)")
	_ = cmd.MarkFlagRequired("question")
	return cmd
}

// briefCmd — triage a messy problem description: agent-solvable tasks stay
// with the agent, scientific questions are handed to `verdict run`.
func briefCmd() *cobra.Command {
	var outDir string
	cmd := &cobra.Command{
		Use:   "brief [file]",
		Short: "Turn a messy problem description into agent tasks + evidence questions",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			log := slog.New(slog.NewTextHandler(os.Stderr, nil))
			cfg := config.Load()
			completer, err := app.BuildCompleter(cfg, log, "")
			if err != nil {
				return err
			}
			input, err := readInput(cmd, args)
			if err != nil {
				return err
			}
			b, err := pipeline.BriefFromText(cmd.Context(), completer, string(input))
			if err != nil {
				return err
			}
			if outDir == "" {
				outDir = "briefs"
			}
			dir := filepath.Join(outDir, slugify(b.Problem)+"-"+time.Now().Format("20060102-150405"))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			briefPath := filepath.Join(dir, "brief.md")
			if err := os.WriteFile(briefPath, []byte(pipeline.RenderBrief(b)), 0o644); err != nil {
				return err
			}
			fmt.Printf("Brief: %s\n", briefPath)
			if len(b.Questions) > 0 {
				fmt.Println("\nSuggested evidence questions (discovery default = mode C: pick papers in the free Consensus web UI):")
				for _, q := range b.Questions {
					fmt.Printf("  # %s\n  bin/verdict web -q %q\n", orDashQ(q.Priority), q.Question)
				}
				fmt.Println(`  after picking papers in the browser:
    pbpaste | bin/verdict dois > dois.txt && bin/verdict run --dois dois.txt -q "<question>"`)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&outDir, "out", "", "output directory (defaults to briefs/)")
	return cmd
}

var mainSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	slug := strings.Trim(mainSlugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(slug) > 40 {
		slug = slug[:40]
	}
	if slug == "" {
		slug = "brief"
	}
	return slug
}

func orDashQ(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// paperCount avoids double counting: Enriched and Papers hold the same
// studies after the enrichment stage.
func paperCount(st *pipeline.State) int {
	if n := len(st.Enriched); n > 0 {
		return n
	}
	return len(st.Papers)
}

// renderAnswer pretty-prints markdown answers in a terminal; falls back to
// the raw text when stdout is piped or the renderer is unavailable.
func renderAnswer(s string) string {
	fi, err := os.Stdout.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return s
	}
	r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(100))
	if err != nil {
		return s
	}
	out, err := r.Render(s)
	if err != nil {
		return s
	}
	return out
}
