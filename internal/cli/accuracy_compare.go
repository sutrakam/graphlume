package cli

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/sutrakam/graphlume/internal/bench"
	"github.com/sutrakam/graphlume/internal/registry"
)

// toolAccuracy captures the precision, recall, and F1 of an external tool
// over compiler-adjudicated call sites.
type toolAccuracy struct {
	Label             string  `json:"label"`
	Adjudicated       int64   `json:"adjudicated_call_sites"`
	Answered          int64   `json:"answered_call_sites"`
	Missed            int64   `json:"missed_call_sites"`
	TruePositives     int64   `json:"true_positives"`
	FalsePositives    int64   `json:"false_positives"`
	Precision         float64 `json:"precision"`
	Recall            float64 `json:"recall"`
	F1                float64 `json:"f1"`
	PrecisionMeasured bool    `json:"precision_measured"`
	Measured          bool    `json:"measured"`
}

// languageComparison compares GraphLume and a candidate tool for a single language
// or for all languages combined.
type languageComparison struct {
	Language  string                 `json:"language"`
	GraphLume bench.LanguageAccuracy `json:"graphlume"`
	Candidate toolAccuracy           `json:"candidate"`
}

// accuracyComparison carries the side-by-side evaluation between GraphLume and an
// external tool.
type accuracyComparison struct {
	Label     string               `json:"label"`
	CSVFile   string               `json:"csv_file"`
	Overall   languageComparison   `json:"overall"`
	Languages []languageComparison `json:"languages"`
}

type compareRow struct {
	CallerFile string
	CallerLine int
	CalleeName string
	CalleeFile string
}

func parseCompareCSV(r io.Reader) ([]compareRow, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1 // Allow variable columns (at least 3)
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read comparison CSV: %w", err)
	}

	var rows []compareRow
	for i, rec := range records {
		if len(rec) < 3 {
			continue
		}
		callerFile := strings.TrimSpace(rec[0])
		lineStr := strings.TrimSpace(rec[1])
		calleeName := strings.TrimSpace(rec[2])

		line, err := strconv.Atoi(lineStr)
		if err != nil {
			// Skip header row if line number is not an integer
			if i == 0 {
				continue
			}
			return nil, fmt.Errorf("line %d: invalid line number %q: %w", i+1, lineStr, err)
		}

		calleeFile := ""
		if len(rec) >= 4 {
			calleeFile = strings.TrimSpace(rec[3])
		}

		normCaller := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(callerFile, "./")))
		normCallee := ""
		if calleeFile != "" {
			normCallee = filepath.ToSlash(filepath.Clean(strings.TrimPrefix(calleeFile, "./")))
		}

		rows = append(rows, compareRow{
			CallerFile: normCaller,
			CallerLine: line,
			CalleeName: calleeName,
			CalleeFile: normCallee,
		})
	}
	return rows, nil
}

func loadCompareCSV(path string) ([]compareRow, error) {
	f, err := os.Open(filepath.Clean(path)) //nolint:gosec // user-specified path to comparison CSV
	if err != nil {
		return nil, fmt.Errorf("open compare file: %w", err)
	}
	defer func() { _ = f.Close() }()
	return parseCompareCSV(f)
}

func matchesCalleeFile(candFile, truthFile string) bool {
	if candFile == "" || truthFile == "" {
		return true
	}
	cf := filepath.ToSlash(filepath.Clean(candFile))
	tf := filepath.ToSlash(filepath.Clean(truthFile))
	if cf == tf {
		return true
	}
	if strings.HasSuffix(tf, "/"+cf) || strings.HasSuffix(cf, "/"+tf) {
		return true
	}
	return false
}

func evaluateComparison(label, csvPath string, truthSites []registry.TruthCallSite, glRep accuracyReport, rows []compareRow) accuracyComparison {
	type siteKey struct {
		file string
		line int
	}

	predsBySite := make(map[siteKey][]compareRow)
	for _, r := range rows {
		k := siteKey{file: r.CallerFile, line: r.CallerLine}
		predsBySite[k] = append(predsBySite[k], r)
	}

	sitesByLang := make(map[string][]registry.TruthCallSite)
	for _, s := range truthSites {
		lang := s.Language
		if lang == "" {
			lang = "unknown"
		}
		sitesByLang[lang] = append(sitesByLang[lang], s)
	}

	calcToolAccuracy := func(lbl string, sites []registry.TruthCallSite) toolAccuracy {
		var tp, fp, missed int64
		adjudicated := int64(len(sites))

		for _, s := range sites {
			normCaller := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(s.CallerFile, "./")))
			k := siteKey{file: normCaller, line: s.CallerLine}
			preds, found := predsBySite[k]
			if !found || len(preds) == 0 {
				missed++
				continue
			}

			matched := false
			for _, p := range preds {
				if p.CalleeName == s.CalleeName && matchesCalleeFile(p.CalleeFile, s.CalleeFile) {
					matched = true
					break
				}
			}
			if matched {
				tp++
			} else {
				fp++
			}
		}

		answered := tp + fp
		precMeasured := answered > 0
		measured := adjudicated > 0

		var prec, rec, f1 float64
		if precMeasured {
			prec = float64(tp) / float64(answered)
		}
		if measured {
			rec = float64(tp) / float64(adjudicated)
		}
		if prec > 0 && rec > 0 {
			f1 = 2 * prec * rec / (prec + rec)
		}

		return toolAccuracy{
			Label:             lbl,
			Adjudicated:       adjudicated,
			Answered:          answered,
			Missed:            missed,
			TruePositives:     tp,
			FalsePositives:    fp,
			Precision:         prec,
			Recall:            rec,
			F1:                f1,
			PrecisionMeasured: precMeasured,
			Measured:          measured,
		}
	}

	overallCand := calcToolAccuracy(label, truthSites)
	overallComp := languageComparison{
		Language:  bench.AllLanguages,
		GraphLume: glRep.Overall,
		Candidate: overallCand,
	}

	glByLang := make(map[string]bench.LanguageAccuracy)
	for _, l := range glRep.Languages {
		glByLang[l.Language] = l
	}

	var langComps []languageComparison
	langs := make([]string, 0, len(sitesByLang))
	for l := range sitesByLang {
		langs = append(langs, l)
	}
	sort.Strings(langs)

	for _, l := range langs {
		sites := sitesByLang[l]
		cand := calcToolAccuracy(label, sites)
		gl := glByLang[l]
		langComps = append(langComps, languageComparison{
			Language:  l,
			GraphLume: gl,
			Candidate: cand,
		})
	}

	sort.SliceStable(langComps, func(i, j int) bool {
		if langComps[i].Candidate.Adjudicated != langComps[j].Candidate.Adjudicated {
			return langComps[i].Candidate.Adjudicated > langComps[j].Candidate.Adjudicated
		}
		return langComps[i].Language < langComps[j].Language
	})

	return accuracyComparison{
		Label:     label,
		CSVFile:   csvPath,
		Overall:   overallComp,
		Languages: langComps,
	}
}

func renderAccuracyComparison(out io.Writer, comp accuracyComparison) {
	if len(comp.Languages) > 1 {
		renderLanguageComparison(out, "TOOL COMPARISON: ALL LANGUAGES", comp.Overall)
	}
	for _, l := range comp.Languages {
		renderLanguageComparison(out, "TOOL COMPARISON: "+strings.ToUpper(l.Language), l)
	}
}

func renderLanguageComparison(out io.Writer, title string, lc languageComparison) {
	_, _ = fmt.Fprintf(out, "\n%s\n", title)
	if !lc.Candidate.Measured && !lc.GraphLume.Measured {
		_, _ = fmt.Fprintf(out, "  NOT MEASURED — no call site adjudicated for it.\n")
		return
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "  TOOL\tSITES\tRIGHT\tWRONG\tPRECISION\tRECALL\tF1")

	glHigh := tierOf(lc.GraphLume, "high")
	_, _ = fmt.Fprintf(w, "  GraphLume (high)\t%s\t%s\t%s\t%s\t%s\t%s\n",
		humanCount(glHigh.Retrieved), humanCount(glHigh.TruePositives),
		humanCount(glHigh.FalsePositives),
		bench.Rate(glHigh.Precision, glHigh.PrecisionMeasured),
		bench.Rate(glHigh.Recall, glHigh.Measured),
		bench.Rate(glHigh.F1, glHigh.Measured))

	glAll := lc.GraphLume.All
	_, _ = fmt.Fprintf(w, "  GraphLume (all)\t%s\t%s\t%s\t%s\t%s\t%s\n",
		humanCount(glAll.Retrieved), humanCount(glAll.TruePositives),
		humanCount(glAll.FalsePositives),
		bench.Rate(glAll.Precision, glAll.PrecisionMeasured),
		bench.Rate(glAll.Recall, glAll.Measured),
		bench.Rate(glAll.F1, glAll.Measured))

	cand := lc.Candidate
	_, _ = fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\n",
		cand.Label, humanCount(cand.Answered), humanCount(cand.TruePositives),
		humanCount(cand.FalsePositives),
		bench.Rate(cand.Precision, cand.PrecisionMeasured),
		bench.Rate(cand.Recall, cand.Measured),
		bench.Rate(cand.F1, cand.Measured))

	_ = w.Flush()
	_, _ = fmt.Fprintf(out, "  Evaluated over %s adjudicated call site(s) from compiler ground truth.\n",
		humanCount(cand.Adjudicated))
}
