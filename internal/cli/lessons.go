package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// # Lessons
//
// A lesson is what one task taught about the index: the code an agent needed
// and was not given (missed), the code that helped (used), and a note. They are
// appended to .graphlume/lessons.jsonl, a plain file beside the index:
//
//   - it survives `index --full`, which rebuilds the database;
//   - it stays on this machine, and .graphlume/ is already ignored by git, so a
//     task's text is never committed by accident;
//   - a person can read, edit or delete a line with any editor.
//
// `lesson report` aggregates them: the symbols and files missed again and again
// are where the index (or its context ranking) is weakest.

// lessonsFile is the file name inside the index directory.
const lessonsFile = "lessons.jsonl"

// lesson is one recorded outcome.
type lesson struct {
	Time   string   `json:"time"`
	Task   string   `json:"task"`
	Missed []string `json:"missed,omitempty"`
	Used   []string `json:"used,omitempty"`
	Note   string   `json:"note,omitempty"`
}

// lessonCount is a name and how many lessons it appears in.
type lessonCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// lessonSummary is the aggregate of all lessons.
type lessonSummary struct {
	Lessons    int           `json:"lessons"`
	WithMisses int           `json:"lessons_with_misses"`
	TopMissed  []lessonCount `json:"top_missed"`
	TopUsed    []lessonCount `json:"top_used"`
	Notes      []string      `json:"recent_notes"`
}

// appendLesson adds one lesson as a line of JSON.
func appendLesson(path string, l lesson) error {
	if strings.TrimSpace(l.Task) == "" {
		return errors.New("a lesson needs a --task: what the work was")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("lesson: %w", err)
	}
	line, err := json.Marshal(l)
	if err != nil {
		return err
	}
	//nolint:gosec // the path is the project's own .graphlume directory
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("lesson: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("lesson: %w", err)
	}
	// A failed close can mean the line never reached the disk.
	if err := f.Close(); err != nil {
		return fmt.Errorf("lesson: %w", err)
	}
	return nil
}

// readLessons reads every lesson, skipping a line that is not one: the file is
// meant to be editable by hand, and one bad line must not hide the rest.
func readLessons(path string) ([]lesson, error) {
	//nolint:gosec // the path is the project's own .graphlume directory
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lessons: %w", err)
	}
	defer func() { _ = f.Close() }()
	var out []lesson
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var l lesson
		if json.Unmarshal(sc.Bytes(), &l) == nil && strings.TrimSpace(l.Task) != "" {
			out = append(out, l)
		}
	}
	return out, sc.Err()
}

// summarizeLessons counts, for each name, the lessons it appears in (once per
// lesson, however often it is written there), most first, then by name.
func summarizeLessons(ls []lesson, top int) lessonSummary {
	s := lessonSummary{Lessons: len(ls), TopMissed: []lessonCount{}, TopUsed: []lessonCount{}, Notes: []string{}}
	missed, used := map[string]int{}, map[string]int{}
	for _, l := range ls {
		if len(l.Missed) > 0 {
			s.WithMisses++
		}
		for _, n := range uniqueNames(l.Missed) {
			missed[n]++
		}
		for _, n := range uniqueNames(l.Used) {
			used[n]++
		}
		if n := strings.TrimSpace(l.Note); n != "" {
			s.Notes = append(s.Notes, n)
		}
	}
	s.TopMissed = topCounts(missed, top)
	s.TopUsed = topCounts(used, top)
	if len(s.Notes) > 5 {
		s.Notes = s.Notes[len(s.Notes)-5:]
	}
	return s
}

func uniqueNames(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, n := range in {
		n = strings.TrimSpace(n)
		if _, dup := seen[n]; n == "" || dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

func topCounts(m map[string]int, top int) []lessonCount {
	out := make([]lessonCount, 0, len(m))
	for n, c := range m {
		out = append(out, lessonCount{Name: n, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if top > 0 && len(out) > top {
		out = out[:top]
	}
	return out
}

func writeLessonSummary(w io.Writer, s lessonSummary) {
	if s.Lessons == 0 {
		fmt.Fprintln(w, "No lessons recorded yet. Add one with: graphlume lesson add --task \"...\" --missed Name")
		return
	}
	fmt.Fprintf(w, "%d lesson(s); %d recorded something the context missed.\n", s.Lessons, s.WithMisses)
	if len(s.TopMissed) > 0 {
		fmt.Fprintln(w, "\nMissed most often:")
		for _, c := range s.TopMissed {
			fmt.Fprintf(w, "  %3d  %s\n", c.Count, c.Name)
		}
	}
	if len(s.TopUsed) > 0 {
		fmt.Fprintln(w, "\nUsed most often:")
		for _, c := range s.TopUsed {
			fmt.Fprintf(w, "  %3d  %s\n", c.Count, c.Name)
		}
	}
	if len(s.Notes) > 0 {
		fmt.Fprintln(w, "\nRecent notes:")
		for _, n := range s.Notes {
			fmt.Fprintf(w, "  - %s\n", n)
		}
	}
}

func newLessonCmd(g *Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lesson",
		Short: "Record what a task taught about the index, and report what keeps being missed",
		Long: `Lessons are a local memory of outcomes. After a task, record the code the context
did not give you (--missed) and the code that helped (--used); 'lesson report'
then shows what is missed again and again, which is where the index or the
context ranking is weakest.

Lessons are kept in .graphlume/lessons.jsonl: they survive a full re-index,
stay on this machine, and are plain lines you can edit.

  graphlume lesson add --task "add idempotency to callbacks" --missed RetryPolicy --missed ledger_entries
  graphlume lesson report`,
	}
	var l lesson
	add := &cobra.Command{
		Use:   "add",
		Short: "Record one task's outcome",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l.Time = time.Now().UTC().Format(time.RFC3339)
			path := filepath.Join(g.Config().RepoRoot, IndexDir, lessonsFile)
			if err := appendLesson(path, l); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Recorded in %s.\n", path)
			return nil
		},
	}
	add.Flags().StringVar(&l.Task, "task", "", "what the work was (required)")
	add.Flags().StringArrayVar(&l.Missed, "missed", nil, "a symbol or file the context should have given (repeatable)")
	add.Flags().StringArrayVar(&l.Used, "used", nil, "a symbol or file that helped (repeatable)")
	add.Flags().StringVar(&l.Note, "note", "", "anything worth remembering")

	var top int
	report := &cobra.Command{
		Use:   "report",
		Short: "What the lessons say keeps being missed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ls, err := readLessons(filepath.Join(g.Config().RepoRoot, IndexDir, lessonsFile))
			if err != nil {
				return err
			}
			sum := summarizeLessons(ls, top)
			if g.JSON {
				return emitJSON(cmd.OutOrStdout(), sum)
			}
			writeLessonSummary(cmd.OutOrStdout(), sum)
			return nil
		},
	}
	report.Flags().IntVar(&top, "top", 10, "how many names to list in each ranking")
	cmd.AddCommand(add, report)
	return cmd
}
