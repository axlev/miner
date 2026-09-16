// Package cohort builds evaluator-facing selection reports and verifies a chosen
// cohort by actually exporting it.
//
// Everything here is evaluator-facing. Reports intentionally show retrospective
// signal strength and ranking, which is legitimate for benchmark construction
// (docs/export-contract.md §7 risk 2) but makes them unfit for any engine-visible
// path. Nothing in this package writes to a prospective bundle.
package cohort

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"miner/internal/gitx"
	"miner/internal/model"
)

// Row is one candidate PR as it appears in a selection report.
type Row struct {
	PR            int
	Repository    string
	Title         string
	MergedAt      string
	StatefulScore float64
	Category      string
	RankScore     float64
	Strong        int
	Medium        int
	Weak          int
	FilesChanged  int
	Additions     int
	Deletions     int
	Subsystem     string
	SnapshotFiles int
	SnapshotBytes int64
	Exportable    bool
	ExportBlocker string
}

// SignalTier summarizes the strongest retrospective evidence tier present.
func (r Row) SignalTier() string {
	switch {
	case r.Strong > 0:
		return fmt.Sprintf("strong x%d", r.Strong)
	case r.Medium > 0:
		return fmt.Sprintf("medium x%d", r.Medium)
	case r.Weak > 0:
		return fmt.Sprintf("weak x%d", r.Weak)
	default:
		return "none"
	}
}

// genericContainers are directory names that group code without describing it. A
// Go repository puts everything under internal/, so grouping on the first component
// alone would file every candidate under one heading and defeat the purpose. These
// are stepped through to the next component instead.
//
// lib/ is deliberately absent: in some C projects (FRR among them) it is a real
// subsystem, not a wrapper.
var genericContainers = map[string]bool{
	"internal": true, "src": true, "pkg": true, "cmd": true, "lib64": true, "source": true,
}

// SubsystemRule names the rule PrimarySubsystem applies, for the manifest.
const SubsystemRule = "code-first"

// ancillaryGroups are grouping keys that describe a change's tests, documentation
// or tooling rather than the code it changes. A PR that fixes one line of OSPF and
// adds twenty topotest files is a change to ospfd, not to "tests": keying it by
// file count would match it against test-only negatives and make the history
// baseline score the test tree. They are skipped when a change touches any code,
// and used as the key only when a change touches nothing else.
var ancillaryGroups = map[string]bool{
	"tests": true, "test": true, "doc": true, "docs": true, "tools": true,
	".github": true, "ci": true, ".ci": true, "m4": true,
}

// IsAncillaryGroup reports whether a grouping key describes tests, docs, tooling,
// or a repository-level build file rather than a code subsystem.
func IsAncillaryGroup(name string) bool {
	if ancillaryGroups[strings.ToLower(name)] {
		return true
	}
	// A key with no separator that looks like a filename is a top-level build or
	// repository file (configure.ac, Makefile.am, .gitignore), not a subsystem.
	if !strings.Contains(name, "/") && (strings.Contains(name, ".") || strings.EqualFold(name, "Makefile")) {
		return true
	}
	return false
}

// subsystemOf returns the change's primary subsystem, weighted by changed lines
// under the code-first rule.
func subsystemOf(files []model.ChangedFile) string {
	return PrimarySubsystem(WeighFiles(files))
}

// WeighFiles buckets changed files by grouping key with their changed lines.
func WeighFiles(files []model.ChangedFile) map[string]int {
	w := map[string]int{}
	for _, f := range files {
		w[SubsystemOfPaths([]string{f.Path})] += f.Additions + f.Deletions
	}
	return w
}

// PrimarySubsystem returns the one subsystem a change belongs to: the code group
// with the most changed lines, ignoring ancillary groups (tests, docs, tooling,
// repository-level build files). A change that touches nothing but ancillary paths
// keys by the largest of those instead, so it is still grouped somewhere. Ties
// break on name, so the answer never depends on map order.
//
// Weighting is by changed lines, not by file count: the file-count rule keyed a
// one-line ospfd fix with twenty new topotests as "tests" (found 2026-09-16 by the
// evaluator read of the first H1 cohort). Both the sampler's matching key and the
// history baseline's subsystem use this, so the two always agree.
func PrimarySubsystem(weights map[string]int) string {
	pick := func(ancillary bool) string {
		best, bestN := "", -1
		for name, n := range weights {
			if IsAncillaryGroup(name) != ancillary {
				continue
			}
			if n > bestN || (n == bestN && name < best) {
				best, bestN = name, n
			}
		}
		return best
	}
	if code := pick(false); code != "" {
		return code
	}
	if anc := pick(true); anc != "" {
		return anc
	}
	return "(none)"
}

// SubsystemOfPaths is the grouping key for a set of paths: the most common leading
// path component, stepping through a generic container directory when it finds one.
// It answers "which bucket", not "which subsystem is this change about" — that is
// PrimarySubsystem, which weighs the buckets code-first.
func SubsystemOfPaths(paths []string) string {
	if len(paths) == 0 {
		return "(none)"
	}
	counts := map[string]int{}
	for _, raw := range paths {
		p := strings.TrimPrefix(path.Clean(raw), "./")
		segs := strings.Split(p, "/")
		top := segs[0]
		if len(segs) > 2 && genericContainers[strings.ToLower(top)] {
			top = top + "/" + segs[1]
		}
		if top == "" {
			top = "(root)"
		}
		counts[top]++
	}
	best, bestN := "", -1
	for k, n := range counts {
		// Ties break on name so the report is deterministic.
		if n > bestN || (n == bestN && k < best) {
			best, bestN = k, n
		}
	}
	return best
}

// Analyze builds one report row per record. When repo is non-empty it additionally
// predicts whether each case would export, by resolving the merge-base and walking
// the head tree — both read-only. A prediction is not a proof: only Verify, which
// performs the real export and the engine's own validation, establishes that.
func Analyze(ctx context.Context, records []model.PRCandidateRecord, repo string) []Row {
	var rows []Row
	var r *gitx.Repository
	if repo != "" {
		r = gitx.OpenRepository(repo)
	}
	for _, rec := range records {
		row := Row{
			PR:            rec.Original.Number,
			Repository:    rec.Original.Repository,
			Title:         rec.Original.Title,
			StatefulScore: rec.Stateful.Score,
			Category:      rec.Stateful.Category,
			RankScore:     rec.Retrospective.SummaryRankScore,
			Strong:        len(rec.Retrospective.StrongSignals),
			Medium:        len(rec.Retrospective.MediumSignals),
			Weak:          len(rec.Retrospective.WeakSignals),
			FilesChanged:  len(rec.Original.ChangedFiles),
			Subsystem:     subsystemOf(rec.Original.ChangedFiles),
			Exportable:    true,
		}
		if !rec.Original.MergedAt.IsZero() {
			row.MergedAt = rec.Original.MergedAt.UTC().Format("2006-01-02")
		}
		for _, f := range rec.Original.ChangedFiles {
			row.Additions += f.Additions
			row.Deletions += f.Deletions
		}
		if r != nil {
			row.Exportable, row.ExportBlocker, row.SnapshotFiles, row.SnapshotBytes = probe(ctx, r, rec)
		}
		rows = append(rows, row)
	}
	return rows
}

// probe answers, without writing anything, whether prospective-export would get as
// far as materializing this case: the two commits must be present, the merge-base
// must resolve, and the head tree must contain nothing inadmissible.
func probe(ctx context.Context, r *gitx.Repository, rec model.PRCandidateRecord) (ok bool, blocker string, files int, bytes int64) {
	base, head := rec.Original.BaseSHA, rec.Original.HeadSHA
	if base == "" || head == "" {
		return false, "record has no base_sha/head_sha", 0, 0
	}
	if _, err := r.MergeBase(ctx, base, head); err != nil {
		return false, "merge-base does not resolve (commits missing from the local clone?)", 0, 0
	}
	entries, err := r.ListTree(ctx, head)
	if err != nil {
		return false, firstLine(err.Error()), 0, 0
	}
	for _, e := range entries {
		bytes += e.Size
	}
	return true, "", len(entries), bytes
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// SortRows orders rows for reading: by subsystem, then strongest evidence first,
// then PR number. Grouping by subsystem rather than ranking globally is the point —
// a list ranked purely by signal strength tends to be one subsystem repeated, which
// cannot discriminate across the axes a pilot cohort has to cover.
func SortRows(rows []Row) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Subsystem != b.Subsystem {
			return a.Subsystem < b.Subsystem
		}
		if a.RankScore != b.RankScore {
			return a.RankScore > b.RankScore
		}
		if a.StatefulScore != b.StatefulScore {
			return a.StatefulScore > b.StatefulScore
		}
		return a.PR < b.PR
	})
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func humanBytes(n int64) string {
	switch {
	case n <= 0:
		return "-"
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.0f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// RenderMarkdown writes a selection report grouped by subsystem.
func RenderMarkdown(rows []Row, probed bool) string {
	SortRows(rows)
	var sb strings.Builder
	sb.WriteString("# Cohort selection report\n\n")
	sb.WriteString("**Evaluator-facing.** This report shows retrospective signal strength and ranking,\n")
	sb.WriteString("which is legitimate for choosing benchmark cases and must never be given to a\n")
	sb.WriteString("reviewer or wired into an engine-visible path.\n\n")

	exportable, blocked := 0, 0
	subsystems := map[string]bool{}
	for _, r := range rows {
		subsystems[r.Subsystem] = true
		if r.Exportable {
			exportable++
		} else {
			blocked++
		}
	}
	fmt.Fprintf(&sb, "**Candidates**: %d across %d %s  \n", len(rows), len(subsystems), plural(len(subsystems), "subsystem", "subsystems"))
	if probed {
		fmt.Fprintf(&sb, "**Export precheck**: %d look exportable, %d blocked  \n", exportable, blocked)
		sb.WriteString("\n> The precheck is a prediction, not a proof. Confirm a shortlist with\n")
		sb.WriteString("> `miner cohort-verify`, which performs the real export and runs the engine's\n")
		sb.WriteString("> own boundary validator. No case should enter a cohort on this column alone.\n")
	} else {
		sb.WriteString("**Export precheck**: not run (pass `--repo` to enable)  \n")
	}
	sb.WriteString("\n")

	sb.WriteString("Pick *across* subsystems rather than down the list. A cohort drawn from the top of a\n")
	sb.WriteString("global ranking tends to be one bug class in one subsystem, which cannot distinguish\n")
	sb.WriteString("discovery from substantiation from false-positive suppression.\n\n---\n\n")

	current := ""
	for _, r := range rows {
		if r.Subsystem != current {
			current = r.Subsystem
			fmt.Fprintf(&sb, "\n## `%s`\n\n", current)
			sb.WriteString("| PR | Title | Merged | Score | Signals | Files | +/- | Snapshot | Exportable |\n")
			sb.WriteString("| ---: | :--- | :--- | :--- | :--- | ---: | :--- | ---: | :--- |\n")
		}
		link := strconv.Itoa(r.PR)
		if r.Repository != "" {
			link = fmt.Sprintf("[%d](https://github.com/%s/pull/%d)", r.PR, r.Repository, r.PR)
		}
		snapshot := "-"
		if r.SnapshotFiles > 0 {
			snapshot = fmt.Sprintf("%d f / %s", r.SnapshotFiles, humanBytes(r.SnapshotBytes))
		}
		exportable := "yes"
		if !r.Exportable {
			exportable = "**NO** — " + truncate(r.ExportBlocker, 90)
		}
		fmt.Fprintf(&sb, "| %s | %s | %s | %.2f %s | %s | %d | +%d/-%d | %s | %s |\n",
			link, truncate(r.Title, 60), r.MergedAt, r.StatefulScore, r.Category,
			r.SignalTier(), r.FilesChanged, r.Additions, r.Deletions, snapshot, exportable)
	}
	return sb.String()
}

// RenderCSV writes the same rows as CSV, for sorting in a spreadsheet.
func RenderCSV(w *os.File, rows []Row) error {
	SortRows(rows)
	c := csv.NewWriter(w)
	if err := c.Write([]string{
		"pr", "repository", "subsystem", "title", "merged_at", "stateful_score", "category",
		"rank_score", "strong", "medium", "weak", "files_changed", "additions", "deletions",
		"snapshot_files", "snapshot_bytes", "exportable", "export_blocker",
	}); err != nil {
		return err
	}
	for _, r := range rows {
		if err := c.Write([]string{
			strconv.Itoa(r.PR), r.Repository, r.Subsystem, r.Title, r.MergedAt,
			strconv.FormatFloat(r.StatefulScore, 'f', 4, 64), r.Category,
			strconv.FormatFloat(r.RankScore, 'f', 4, 64),
			strconv.Itoa(r.Strong), strconv.Itoa(r.Medium), strconv.Itoa(r.Weak),
			strconv.Itoa(r.FilesChanged), strconv.Itoa(r.Additions), strconv.Itoa(r.Deletions),
			strconv.Itoa(r.SnapshotFiles), strconv.FormatInt(r.SnapshotBytes, 10),
			strconv.FormatBool(r.Exportable), r.ExportBlocker,
		}); err != nil {
			return err
		}
	}
	c.Flush()
	return c.Error()
}
