package cli

import (
	stdctx "context"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sutrakam/graphlume/internal/capability/move"
	"github.com/sutrakam/graphlume/internal/capability/rename"
	"github.com/sutrakam/graphlume/internal/edit"
	"github.com/sutrakam/graphlume/pkg/model"
)

// Exit codes are rename's: 2 always means "nothing was written".
const (
	moveExitRefused    = renameExitRefused
	moveExitUnverified = renameExitUnverified
)

// moveLimits is printed with every move: what it does not do.
const moveLimits = "move relocates one declaration between two files of one Go package and corrects both " +
	"import lists. It edits no other file, because inside a package no reference changes. " +
	"It does not take a type's methods along, and it does not copy a file's header comment into a new file. " +
	"The build check compiles for this machine's platform only; files built under other constraints are " +
	"compared, never compiled. After an apply the index still describes the files as they were: " +
	"run 'graphlume index'."

type moveOptions struct {
	Symbol, From, To string
	Apply            bool
	NoBuild          bool
}

// moveReport is the --json shape and the source of the text rendering.
type moveReport struct {
	Symbol      string          `json:"symbol"`
	From        string          `json:"from"`
	To          string          `json:"to"`
	Declaration *move.Decl      `json:"declaration,omitempty"`
	Sites       []move.Site     `json:"sites"`
	Refusals    []renameRefusal `json:"refusals"`
	Applied     bool            `json:"applied"`
	Files       []string        `json:"files_changed,omitempty"`
	Created     string          `json:"created,omitempty"`
	Build       moveBuild       `json:"build"`
	Notes       []string        `json:"notes,omitempty"`
	Limits      string          `json:"limits"`
}

// Build check states.
const (
	buildPass   = "pass"    // the compiler accepted the result
	buildNotRun = "not-run" // nothing compiled it; Detail says why
)

// moveBuild is what the compiler said about the result.
type moveBuild struct {
	Status  string `json:"status"`
	Command string `json:"command,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

func newMoveCmd(g *Global) *cobra.Command {
	var o moveOptions
	cmd := &cobra.Command{
		Use:   "move <symbol> <from-file> <to-file>",
		Short: "Move a Go declaration to another file of its package",
		Long: `Moves one top-level Go declaration (a function, type, constant or variable,
with its doc comment) from one file to another file of the same package, and
corrects the import lists of both. A method is named Type.Method.

A dry run by default: it prints every change it would make, each with its
confidence, and whether the result builds. Nothing is written without --apply.

Inside a package a move changes no reference, so the whole edit is syntax the
Go standard parser reports exactly. --apply writes only if ALL of this holds:
  - both files are Go files in one directory, in one package, built under the
    same constraints (the same //go:build line and file-name suffix), and
    neither is generated or uses cgo
  - both are byte-for-byte what was indexed (otherwise re-index), inside the
    project root, regular and writable; a target that does not exist is
    created with the right package clause
  - the name is declared exactly once in the source file, on lines of its
    own, and the target does not already declare it
  - every import the moved code uses has a package name that is known: an
    explicit one, a standard-library one, or one 'go list' reports
  - the move cannot change start-up behaviour: it is not an init function,
    not a member of an iota group, and not a variable with an initialiser in
    a package where some initialiser runs code
  - with go on PATH, the package builds with the planned files in place
    (checked before writing, through go build's -overlay)
If anything fails, nothing is written, every reason is listed, and the exit
code is 2. Other languages and moves to another package are refused.

The target is written first and the source second, each atomically; if the
second write fails the first is undone. With go on PATH the package is built
once more after writing, and both files are restored if that build fails.

Exit codes: 0 would apply / applied and the build passed; 1 error, or a
rollback that could not restore a file; 2 refused, nothing written; 3
applied, but no build checked it (--no-build, no go on PATH, or files this
platform does not build).`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.Symbol, o.From, o.To = args[0], cleanRel(args[1]), cleanRel(args[2])
			return runMove(cmd, g, o)
		},
	}
	cmd.Flags().BoolVar(&o.Apply, "apply", false, "write the edits (default is a dry run)")
	cmd.Flags().BoolVar(&o.NoBuild, "no-build", false,
		"do not compile the result; it is then checked by parsing only, and an apply exits 3")
	return cmd
}

// cleanRel puts a path argument in the index's spelling: cleaned, forward
// slashes. An absolute path is left for the editor to refuse.
func cleanRel(p string) string { return filepath.ToSlash(filepath.Clean(p)) }

func runMove(cmd *cobra.Command, g *Global, o moveOptions) error {
	ctx := cmd.Context()
	s, err := openQuery(ctx, g)
	if err != nil {
		return err
	}
	defer s.close()

	rep := moveReport{Symbol: o.Symbol, From: o.From, To: o.To,
		Sites: []move.Site{}, Refusals: []renameRefusal{}, Limits: moveLimits,
		Build: moveBuild{Status: buildNotRun}}
	refused := func() error { return finishMove(cmd, g, rep, moveExitRefused) }
	refuse := func(p *edit.Problem) {
		rep.Refusals = append(rep.Refusals, renameRefusal{Path: p.Path, Line: p.Line,
			Reason: strings.TrimPrefix(p.Reason, "edit: ")})
	}

	// What the request alone rules out — another language, another package —
	// is said before any file is read or compared with the index.
	if r := move.CheckRequest(move.Request{Symbol: o.Symbol, From: o.From, To: o.To}); r != nil {
		rep.Refusals = append(rep.Refusals, renameRefusal{Path: r.Path, Line: r.Line, Reason: r.Reason})
		return refused()
	}

	// The editor reads both files under the rules it writes by: inside the
	// root, no symlinked component, regular, bounded.
	ed := edit.New(g.Config().RepoRoot)
	src, prob, err := ed.Load(o.From)
	if err != nil {
		return fmt.Errorf("move: %w", err)
	}
	if prob != nil {
		refuse(prob)
	} else if !src.Exists {
		rep.Refusals = append(rep.Refusals, renameRefusal{Path: o.From, Reason: "no such file"})
	}
	dst, prob, err := ed.Load(o.To)
	if err != nil {
		return fmt.Errorf("move: %w", err)
	}
	if prob != nil {
		refuse(prob)
	}
	if len(rep.Refusals) > 0 {
		return refused()
	}
	rep.From, rep.To = src.Path, dst.Path

	// Both files must be the bytes the index describes, as for a rename: the
	// user (or the agent) decided on this move from what the index showed.
	indexed, err := indexedHashes(ctx, s)
	if err != nil {
		return err
	}
	for _, f := range []edit.File{src, dst} {
		if f.Exists {
			if reason := staleReason(indexed, f); reason != "" {
				rep.Refusals = append(rep.Refusals, renameRefusal{Path: f.Path, Reason: reason})
			}
		}
	}

	in := move.Input{Source: src.Data, Target: dst.Data, TargetExists: dst.Exists}
	dir := path.Dir(src.Path)
	if dir == path.Dir(dst.Path) {
		siblings, problems, err := ed.LoadDir(dir, goSourceName)
		if err != nil {
			return fmt.Errorf("move: %w", err)
		}
		for i := range problems {
			// A file of the package that cannot be read safely leaves the
			// package's declarations unknown.
			problems[i].Reason = "a file of the package, and it " + problems[i].Reason
			refuse(&problems[i])
		}
		for _, f := range siblings {
			if f.Path != src.Path && f.Path != dst.Path {
				in.Siblings = append(in.Siblings, move.File{Name: path.Base(f.Path), Data: f.Data})
			}
		}
	}
	if len(rep.Refusals) > 0 {
		return refused()
	}

	tool := newGoTool(g.Config().RepoRoot, dir)
	if tool != nil {
		in.PackageNames = tool.packageNames(ctx, move.UnnamedImports(src.Data, dst.Data))
	}

	plan := move.Compute(move.Request{Symbol: o.Symbol, From: src.Path, To: dst.Path}, in)
	if plan.Decl.Name != "" {
		rep.Declaration = &plan.Decl
	}
	for _, r := range plan.Refusals {
		rep.Refusals = append(rep.Refusals, renameRefusal{Path: r.Path, Line: r.Line, Reason: r.Reason})
	}
	if !plan.OK() {
		return refused()
	}
	rep.Sites, rep.Notes = plan.Sites, plan.Notes

	// What Commit will be handed, prepared now so that a dry run refuses
	// exactly what an apply would: a read-only file, a result over the limit.
	var (
		prepared []edit.Prepared
		target   edit.Prepared
	)
	if plan.CreatesTarget {
		target, prob = ed.Create(dst, plan.TargetAfter, src)
	} else {
		target, prob = ed.Rewrite(dst, plan.TargetAfter)
	}
	if prob != nil {
		refuse(prob)
	}
	source, prob := ed.Rewrite(src, plan.SourceAfter)
	if prob != nil {
		refuse(prob)
	}
	if len(rep.Refusals) > 0 {
		rep.Sites = []move.Site{}
		return refused()
	}
	// Target first: an interruption between the two writes then leaves the
	// declaration in both files (a compile error), never in neither.
	prepared = append(prepared, target, source)

	// The compiler's verdict on the planned bytes, before any are written.
	var check func(stdctx.Context) error
	switch {
	case o.NoBuild:
		rep.Build.Detail = "--no-build: the result was checked by parsing only"
	case tool == nil:
		rep.Build.Detail = "no go command on PATH: the result was checked by parsing only"
	default:
		scratch, cleanup, err := moveScratch(g.Config().RepoRoot)
		if err != nil {
			return fmt.Errorf("move: %w", err)
		}
		defer cleanup()
		verdict, err := tool.verify(ctx, plan, src.Path, dst.Path, scratch)
		if err != nil {
			return fmt.Errorf("move: %w", err)
		}
		if verdict.refusal != "" {
			rep.Sites = []move.Site{}
			rep.Refusals = append(rep.Refusals, renameRefusal{Reason: verdict.refusal})
			return refused()
		}
		rep.Build = verdict.build
		check = verdict.recheck
	}
	grade(&rep)

	if !o.Apply {
		return finishMove(cmd, g, rep, 0)
	}

	applied, code, msg := moveCommitOutcome(ed.CommitChecked(ctx, prepared, check))
	switch {
	case !applied && code == moveExitRefused:
		rep.Sites = []move.Site{}
		rep.Refusals = append(rep.Refusals, renameRefusal{Reason: msg})
		return refused()
	case !applied:
		return fmt.Errorf("move: %s", msg)
	case msg != "":
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", msg)
	}
	rep.Applied = true
	rep.Files = []string{dst.Path, src.Path}
	if plan.CreatesTarget {
		rep.Created = dst.Path
	}
	if check != nil {
		// CommitChecked returned nil, so the build of the written files passed.
		rep.Build.Status, rep.Build.Detail = buildPass, "built from the files on disk after writing"
		grade(&rep)
		return finishMove(cmd, g, rep, 0)
	}
	return finishMove(cmd, g, rep, moveExitUnverified)
}

// grade sets every site's confidence from the build check. Only a compiler
// makes a claim exact (tests/architecture, TestOnlyTheCompilerProducesExact);
// without one the sites are what the Go parser located, which is high.
func grade(rep *moveReport) {
	confidence := model.ConfidenceHigh
	if rep.Build.Status == buildPass {
		confidence = model.ConfidenceExact
	}
	for i := range rep.Sites {
		rep.Sites[i].Confidence = confidence
	}
}

// goSourceName: a file name the go command would consider part of a package.
func goSourceName(name string) bool {
	return strings.HasSuffix(name, ".go") && !strings.HasPrefix(name, "_") && !strings.HasPrefix(name, ".")
}

// indexedHashes maps every indexed path to the content hash recorded for it.
func indexedHashes(ctx stdctx.Context, s *session) (map[string]string, error) {
	files, err := s.reg.Files(ctx, s.runID)
	if err != nil {
		return nil, fmt.Errorf("move: list indexed files: %w", err)
	}
	out := make(map[string]string, len(files))
	for _, f := range files {
		out[f.Path] = f.ContentHash
	}
	return out, nil
}

// staleReason says why f is not the file the index describes, or "".
func staleReason(indexed map[string]string, f edit.File) string {
	want, ok := indexed[f.Path]
	switch {
	case !ok:
		return "file is not in the index, so it cannot be checked against what was indexed; " +
			"run 'graphlume index' and try again"
	case want != f.Hash():
		return "changed since it was indexed; run 'graphlume index' and try again"
	}
	return ""
}

// moveCommitOutcome classifies what edit.CommitChecked returned, as
// commitOutcome does for a rename: applied reports whether the new text is in
// place; when it is not, code is moveExitRefused if every file holds what it
// held before and 1 if a rollback could not restore one.
func moveCommitOutcome(err error) (applied bool, code int, msg string) {
	if err == nil {
		return true, 0, ""
	}
	rest, lockErr := []error{err}, error(nil)
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		rest = rest[:0]
		for _, e := range j.Unwrap() {
			if errors.Is(e, edit.ErrLockRelease) {
				lockErr = e
				continue
			}
			rest = append(rest, e)
		}
	}
	if len(rest) == 0 {
		return true, 0, fmt.Sprintf("the move was written, but %v", lockErr)
	}
	cause := errors.Join(rest...)
	switch {
	case errors.Is(cause, edit.ErrRollbackIncomplete):
		msg = fmt.Sprintf("the move failed part-way and the rollback was incomplete; the files named here may "+
			"hold the declaration twice or not at all — check them with your version control before anything else: %v", cause)
		code = 1
	case errors.Is(cause, edit.ErrCheckFailed):
		msg = fmt.Sprintf("the package no longer built once the files were written, so both were restored (%v)", cause)
		code = moveExitRefused
	case errors.Is(cause, edit.ErrLocked):
		msg = fmt.Sprintf("nothing was written: %v", cause)
		code = moveExitRefused
	case errors.Is(cause, edit.ErrStale):
		msg = fmt.Sprintf("a file changed while the move was being written; every file already written "+
			"was restored. Run 'graphlume index' and try again (%v)", cause)
		code = moveExitRefused
	default:
		msg = fmt.Sprintf("a write failed; every file already written was restored (%v)", cause)
		code = moveExitRefused
	}
	if lockErr != nil {
		msg += fmt.Sprintf("; also, %v", lockErr)
	}
	return false, code, msg
}

func finishMove(cmd *cobra.Command, g *Global, rep moveReport, code int) error {
	if g.JSON {
		if err := emitJSON(cmd.OutOrStdout(), rep); err != nil {
			return err
		}
	} else if err := renderMove(cmd.OutOrStdout(), rep); err != nil {
		return err
	}
	if code == 0 {
		return nil
	}
	// Silent: the report is the explanation; the code is the verdict.
	return &ExitCodeError{Code: code, Silent: true, Err: fmt.Errorf("move: exit %d", code)}
}

func renderMove(w io.Writer, rep moveReport) error {
	what := rep.Symbol
	if d := rep.Declaration; d != nil {
		what = d.Kind + " " + rep.Symbol
	}
	fmt.Fprintf(w, "Move %s: %s -> %s", rename.TerminalSafe(what), rename.TerminalSafe(rep.From), rename.TerminalSafe(rep.To))
	if d := rep.Declaration; d != nil {
		fmt.Fprintf(w, "  (%s:%d-%d)", rename.TerminalSafe(d.Path), d.Line, d.EndLine)
	}
	fmt.Fprintln(w)
	switch {
	case len(rep.Refusals) > 0:
		fmt.Fprintln(w, "REFUSED: nothing was written.")
	case rep.Applied && rep.Build.Status != buildPass:
		fmt.Fprintf(w, "APPLIED to %d file(s), but NOT BUILD-CHECKED: %s.\n", len(rep.Files), rep.Build.Detail)
	case rep.Applied:
		fmt.Fprintf(w, "APPLIED to %d file(s).\n", len(rep.Files))
	default:
		fmt.Fprintln(w, "DRY RUN: nothing was written. Re-run with --apply to make these edits.")
		if rep.Build.Status != buildPass {
			fmt.Fprintln(w, "  No build checks this move, so --apply would exit 3.")
		}
	}

	if len(rep.Sites) > 0 {
		fmt.Fprintln(w, "\nSITES")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, s := range rep.Sites {
			loc := fmt.Sprintf("%s:%d", rename.TerminalSafe(s.Path), s.Line)
			if s.EndLine > s.Line {
				loc += fmt.Sprintf("-%d", s.EndLine)
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", s.Confidence, loc, s.Action, rename.TerminalSafe(s.Detail))
		}
		if err := tw.Flush(); err != nil {
			return fmt.Errorf("render move: %w", err)
		}
	}
	if len(rep.Refusals) > 0 {
		fmt.Fprintln(w, "\nREASONS")
		for _, r := range rep.Refusals {
			fmt.Fprintf(w, "  %s\n", r)
		}
	}
	if len(rep.Refusals) == 0 {
		if rep.Build.Status == buildPass {
			fmt.Fprintf(w, "\nBUILD: PASS  %s\n", rep.Build.Command)
			if rep.Build.Detail != "" {
				fmt.Fprintf(w, "  %s\n", rep.Build.Detail)
			}
		} else {
			fmt.Fprintf(w, "\nBUILD: NOT RUN  %s\n", rep.Build.Detail)
			fmt.Fprintln(w, "  Sites are `high`, not `exact`: the Go parser located them and no compiler has checked the result. Build and test.")
		}
	}
	if len(rep.Notes) > 0 {
		fmt.Fprintln(w, "\nNOTES")
		for _, n := range rep.Notes {
			fmt.Fprintf(w, "  %s\n", rename.TerminalSafe(n))
		}
	}
	fmt.Fprintf(w, "\nLIMITS: %s\n", rep.Limits)
	return nil
}
