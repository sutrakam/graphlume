package indexer

import (
	"context"
	"sort"

	"github.com/sutrakam/graphlume/internal/config"
)

// SuggestMinFiles is how many files with definitions a language needs in the
// index before a missing indexer for it is worth a line of output. A repository
// with three Python scripts beside a Go service is not asking for scip-python,
// and a hint printed for every stray file would be noise people learn to skip.
const SuggestMinFiles = 10

// LanguageFiles is how many indexed files with definitions one language has, as
// the index names the language.
type LanguageFiles struct {
	Language string
	Files    int64
}

// Suggestion says that a language in the index would gain compiler-proven
// answers from an indexer that is not installed, and what to run about it.
// Producing one installs nothing.
type Suggestion struct {
	Language Language `json:"language"`
	Indexer  Indexer  `json:"indexer"`
	Files    int64    `json:"files"`
	// Adds is what the indexer would add, in a sentence.
	Adds string `json:"adds"`
	// Command is what to run: `graphlume indexers install` where GraphLume
	// has a pin it can verify, the tool's own install line otherwise.
	Command string `json:"command"`
	// Pinned says which of the two Command is.
	Pinned bool `json:"pinned"`
}

// indexerLanguage maps the language a file is indexed as to the language of the
// indexer that reads it. Go is absent on purpose: the type checker built into
// this binary reads it, so nothing is ever missing.
var indexerLanguage = map[string]Language{
	"typescript": LangTypeScript, "tsx": LangTypeScript, "javascript": LangTypeScript, "jsx": LangTypeScript,
	"python": LangPython,
	"java":   LangJava, "kotlin": LangJava, "scala": LangJava,
	"c": LangCPP, "cpp": LangCPP,
	"rust":   LangRust,
	"csharp": LangCSharp, "c_sharp": LangCSharp,
	"ruby": LangRuby,
}

// Suggest lists, for each language with at least SuggestMinFiles files with
// definitions whose indexer is not installed, what installing it would add.
//
// It asks only whether the binary can be found (Provisioner.Available): no
// download, no prompt, no build. A language the user excluded in
// accuracy.indexers.languages is left out, since they said they do not want
// it, and accuracy fast returns nothing, since it promises no compiler pass.
func Suggest(ctx context.Context, r Runner, cfg config.Config, langs []LanguageFiles) []Suggestion {
	if cfg.Accuracy == config.AccuracyFast {
		return nil
	}
	if r == nil {
		r = ExecRunner{}
	}
	files := map[Language]int64{}
	for _, l := range langs {
		if il, ok := indexerLanguage[l.Language]; ok {
			files[il] += l.Files
		}
	}
	allowed := languageFilter(cfg.IndexerLanguages)
	runner := cfg.IndexerRunner
	if runner == "" {
		runner = RunnerNative
	}
	prov := &Provisioner{Runner: r, Policy: cfg.ProvisionPolicy, RunnerKind: runner}

	var out []Suggestion
	for il, n := range files {
		if n < SuggestMinFiles || (allowed != nil && !allowed[il]) {
			continue
		}
		pin, ok := PinFor(il)
		if !ok {
			continue
		}
		if suggestInstalled(ctx, prov, r, pin, runner) {
			continue
		}
		s := Suggestion{Language: il, Indexer: pin.Indexer, Files: n, Adds: addsFor(il)}
		switch {
		case pin.Channel == ChannelUser:
			s.Command = pin.InstallHint
		case pin.Channel == ChannelDocker && runner != RunnerDocker:
			// Installed only as a container image; natively it is the user's.
			s.Command = "set accuracy.indexers.runner to docker in your user config, then run " +
				"`graphlume indexers install`, or install scip-java yourself (https://sourcegraph.github.io/scip-java/)"
		case pin.verifiable() != nil:
			s.Command = "install " + pin.Binary + " yourself"
		default:
			s.Command, s.Pinned = "graphlume indexers install", true
			if il == LangCSharp {
				// The install alone proves nothing: the run is what needs the
				// opt-in, and `indexers install` installs and runs.
				s.Command = "graphlume indexers install --allow-repo-build"
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Language < out[j].Language })
	return out
}

func addsFor(l Language) string {
	what := "compiler-proven callers and references, and proof that a name is a use of something else"
	switch l {
	case LangTypeScript:
		return what + " for TypeScript and JavaScript"
	case LangPython:
		return what + " for Python"
	case LangJava:
		return what + " for Java, Kotlin and Scala"
	case LangCPP:
		return what + " for C and C++ (needs a compile_commands.json, and --allow-repo-build to run, since it executes the compilers that file names)"
	case LangRust:
		return what + " for Rust (without --allow-repo-build it runs without the repository's build scripts and procedural macros, so code they generate stays graded)"
	case LangCSharp:
		return what + " for C# (.NET) (needs --allow-repo-build to run, since it loads the project through MSBuild and restores its packages)"
	case LangRuby:
		return what + " for Ruby"
	}
	return what
}

// suggestInstalled says whether the indexer is usable, asking as little as
// possible: finding the binary on PATH is enough, so `status` does not start a
// Node program per language just to read a version it will not print. Only a
// container image has to be asked about by the daemon.
func suggestInstalled(ctx context.Context, prov *Provisioner, r Runner, pin Pin, runner string) bool {
	if runner == RunnerDocker && pin.Channel == ChannelDocker {
		_, ok := prov.Available(ctx, pin)
		return ok
	}
	_, err := r.LookPath(pin.Binary)
	return err == nil
}
