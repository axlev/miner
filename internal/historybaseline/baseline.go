// Package historybaseline is arm H of the H1 experiment: a zero-LLM baseline that
// flags a change RISKY when the subsystem it touches has, over the 24 months before
// the case's cutoff, a top-tercile combination of strong-signal defect density and
// churn — computed from the repository alone, from pre-cutoff history only.
//
// Output is evaluator-only. It is not oracle material (it reads no fix, no label),
// but it is an arm's verdict and must never reach a reviewer arm.
package historybaseline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"miner/internal/buildinfo"
	"miner/internal/cohort"
	"miner/internal/correlator"
	"miner/internal/gitx"
	"miner/internal/model"
	"miner/internal/prospectiveexport"
)

// SchemaVersion identifies the per-case file.
const SchemaVersion = "history-baseline/v1"

// Frozen parameters. They are recorded verbatim in every file's Rule.
const (
	WindowMonths = 24
	MinCommits   = 20
)

// Rule is the frozen definition, written into every file so a reader never has to
// look it up.
type Rule struct {
	WindowMonths     int      `json:"window_months"`
	SignalTiers      []string `json:"signal_tiers"`
	RiskyIf          string   `json:"risky_if"`
	DateField        string   `json:"date_field"`
	MinCommits       int      `json:"min_commits"`
	Score            string   `json:"score"`
	TieRule          string   `json:"tie_rule"`
	PrimarySubsystem string   `json:"primary_subsystem"`
	History          string   `json:"history"`
	Attribution      string   `json:"attribution"`
	Percentile       string   `json:"percentile"`
	Tercile          string   `json:"tercile"`
}

var frozenRule = Rule{
	WindowMonths:     WindowMonths,
	SignalTiers:      []string{"strong"},
	RiskyIf:          "top tercile",
	DateField:        "committer",
	MinCommits:       MinCommits,
	Score:            "mean percentile rank of density and churn_lines",
	TieRule:          "lower tercile",
	PrimarySubsystem: "most changed lines",
	History:          "all ancestors of <merge_commit>^1 with committer date in [cutoff - 24 months, cutoff)",
	Attribution:      "a corrective commit counts against the subsystem of the commit it fixes when that SHA resolves in the mirror (attributed_by=fixed_commit), else of the merge commit 'Merge pull request #N' for a fixed PR number found in the same walk (fixed_pr_merge), else its own primary subsystem (self)",
	Percentile:       "(count strictly below + 0.5 * count equal) / n over subsystems with commits >= min_commits",
	Tercile:          "p = fraction of scored subsystems strictly below; p < 1/3 -> 1, p < 2/3 -> 2, else 3",
}

// Subsystem is one subsystem's features over the window.
type Subsystem struct {
	Name        string   `json:"name"`
	Commits     int      `json:"commits"`
	ChurnLines  int      `json:"churn_lines"`
	Defects     int      `json:"defects"`
	Density     *float64 `json:"density"`
	Score       *float64 `json:"score"`
	Tercile     int      `json:"tercile,omitempty"`
	LowActivity bool     `json:"low_activity,omitempty"`
}

// Defect is one corrective commit and where it was attributed.
type Defect struct {
	SHA          string `json:"sha"`
	Subsystem    string `json:"subsystem"`
	AttributedBy string `json:"attributed_by"` // fixed_commit | fixed_pr_merge | self
	FixedRef     string `json:"fixed_ref,omitempty"`
}

// Touched is a subsystem of the case's own diff with its changed lines.
type Touched struct {
	Name         string   `json:"name"`
	ChangedLines int      `json:"changed_lines"`
	Score        *float64 `json:"score"`
	Tercile      int      `json:"tercile,omitempty"`
}

// Result is one case's file.
type Result struct {
	SchemaVersion string   `json:"schema_version"`
	CaseID        string   `json:"case_id"`
	Risky         *bool    `json:"risky"`
	Score         *float64 `json:"score"`
	Subsystem     string   `json:"subsystem,omitempty"`
	Tercile       int      `json:"tercile,omitempty"`
	Rule          Rule     `json:"rule"`
	Provenance    struct {
		ProducedBy  string `json:"produced_by"`
		ProducedAt  string `json:"produced_at"`
		InputSHA256 string `json:"input_sha256"`
	} `json:"provenance"`
	// Extra keys the engine tolerates and ignores.
	Reason      string      `json:"reason,omitempty"`
	MergeBase   string      `json:"merge_base,omitempty"`
	Window      [2]string   `json:"window"`
	Cutoff      string      `json:"cutoff"`
	Repository  string      `json:"repository"`
	PR          int         `json:"pr"`
	Touched     []Touched   `json:"touched"`
	Detail      []Subsystem `json:"detail"`
	Defects     []Defect    `json:"defects"`
	WalkCommits int         `json:"walk_commits"`
	// ScoredSubsystems is how many subsystems cleared min_commits and were ranked;
	// Tercile3Count how many of them landed in the top tercile. When the latter is
	// 0 (ties at the top), H could not have said RISKY for any subsystem of this
	// case, and an aggregate reader needs to see that per case.
	ScoredSubsystems int `json:"scored_subsystems"`
	Tercile3Count    int `json:"tercile3_count"`
}

// Case is the subset of a cohort case the baseline reads: identity, cutoff, the
// merge commit, and the PR's own diff stats. Nothing retrospective is read.
type Case struct {
	SchemaVersion string `json:"schema_version"`
	Repository    string `json:"repository"`
	PR            int    `json:"pr"`
	Record        struct {
		Original model.OriginalPR `json:"original"`
	} `json:"record"`
}

// Options control one run.
type Options struct {
	Cutoff      time.Time // overrides merged_at
	InputSHA256 string
}

// Build computes one case's verdict from the mirror. It fails only on a case that
// cannot be identified; an unresolvable history yields risky: null with a reason.
func Build(ctx context.Context, repo *gitx.Repository, c Case, opt Options) (*Result, error) {
	if c.PR <= 0 || c.Repository == "" {
		return nil, fmt.Errorf("case lacks repository or PR number")
	}
	cutoff := opt.Cutoff
	if cutoff.IsZero() {
		cutoff = c.Record.Original.MergedAt
	}
	if cutoff.IsZero() {
		return nil, fmt.Errorf("PR #%d: no cutoff and the record has no merged_at", c.PR)
	}
	cutoff = cutoff.UTC()
	since := cutoff.AddDate(0, -WindowMonths, 0)
	r := &Result{
		SchemaVersion: SchemaVersion,
		CaseID:        prospectiveexport.GenerateCaseID(c.Repository, c.PR, cutoff),
		Rule:          frozenRule,
		Cutoff:        cutoff.Format(time.RFC3339Nano),
		Window:        [2]string{since.Format(time.RFC3339Nano), cutoff.Format(time.RFC3339Nano)},
		Repository:    c.Repository,
		PR:            c.PR,
		Touched:       []Touched{},
		Detail:        []Subsystem{},
		Defects:       []Defect{},
	}
	r.Provenance.ProducedBy = buildinfo.MinerVersion()
	r.Provenance.ProducedAt = time.Now().UTC().Format(time.RFC3339)
	r.Provenance.InputSHA256 = opt.InputSHA256

	// The case's own touched subsystems, from its diff stats.
	touchedLines := map[string]int{}
	for _, f := range c.Record.Original.ChangedFiles {
		touchedLines[cohort.SubsystemOfPaths([]string{f.Path})] += f.Additions + f.Deletions
	}
	if len(touchedLines) == 0 {
		r.Reason = "case has no changed_files; no subsystem to score"
		return r, nil
	}

	// Pre-cutoff history: ancestors of the merge commit's first parent.
	merge := c.Record.Original.MergeCommitSHA
	if merge == "" {
		r.Reason = "record has no merge_commit_sha; base branch at merge cannot be identified"
		return r, nil
	}
	base, err := repo.ResolveCommit(ctx, merge+"^1")
	if err != nil {
		r.Reason = fmt.Sprintf("merge commit first parent does not resolve in the mirror: %v", err)
		return r, nil
	}
	r.MergeBase = base
	walk, err := repo.HistoryWalk(ctx, base, since, cutoff)
	if err != nil {
		r.Reason = fmt.Sprintf("history walk failed: %v", err)
		return r, nil
	}
	r.WalkCommits = len(walk)

	// Features per subsystem, and the lookups attribution needs.
	feats := map[string]*Subsystem{}
	get := func(name string) *Subsystem {
		s, ok := feats[name]
		if !ok {
			s = &Subsystem{Name: name}
			feats[name] = s
		}
		return s
	}
	primaryOf := map[string]string{} // sha -> primary subsystem
	prMerge := map[int]string{}      // PR number -> merge commit sha, from "Merge pull request #N"
	for _, cm := range walk {
		perSub := map[string]int{}
		for path, ad := range cm.Files {
			perSub[cohort.SubsystemOfPaths([]string{path})] += ad[0] + ad[1]
		}
		for name, lines := range perSub {
			s := get(name)
			s.Commits++
			s.ChurnLines += lines
		}
		primaryOf[cm.SHA] = primary(perSub, nil)
		if n, ok := mergePRNumber(cm.Subject); ok {
			if _, dup := prMerge[n]; !dup {
				prMerge[n] = cm.SHA
			}
		}
	}
	for _, cm := range walk {
		shas, prs, matched := correlator.StrongCorrectiveRefs(cm.Message)
		if !matched {
			continue
		}
		d := Defect{SHA: cm.SHA, AttributedBy: "self", Subsystem: primaryOf[cm.SHA]}
	attribute:
		for _, ref := range shas {
			if full, err := repo.ResolveCommit(ctx, ref); err == nil {
				if sub, ok := primaryOf[full]; ok && sub != "" {
					d.AttributedBy, d.Subsystem, d.FixedRef = "fixed_commit", sub, full
					break attribute
				}
			}
		}
		if d.AttributedBy == "self" {
			for _, n := range prs {
				if msha, ok := prMerge[n]; ok {
					if sub := primaryOf[msha]; sub != "" {
						d.AttributedBy, d.Subsystem, d.FixedRef = "fixed_pr_merge", sub, fmt.Sprintf("#%d", n)
						break
					}
				}
			}
		}
		if d.Subsystem == "" {
			continue // a commit with no files (empty) attributes nowhere
		}
		get(d.Subsystem).Defects++
		r.Defects = append(r.Defects, d)
	}
	sort.Slice(r.Defects, func(i, j int) bool { return r.Defects[i].SHA < r.Defects[j].SHA })

	// Density, score, terciles over the active subsystems.
	var active []*Subsystem
	for _, s := range feats {
		if s.Commits > 0 {
			dens := float64(s.Defects) / float64(s.Commits)
			s.Density = &dens
		}
		if s.Commits >= MinCommits {
			active = append(active, s)
		} else {
			s.LowActivity = true
		}
	}
	densities := make([]float64, len(active))
	churns := make([]float64, len(active))
	for i, s := range active {
		densities[i] = *s.Density
		churns[i] = float64(s.ChurnLines)
	}
	for i, s := range active {
		sc := (percentile(densities, densities[i]) + percentile(churns, churns[i])) / 2
		s.Score = &sc
	}
	scores := make([]float64, len(active))
	for i, s := range active {
		scores[i] = *s.Score
	}
	for _, s := range active {
		s.Tercile = tercile(scores, *s.Score)
		if s.Tercile == 3 {
			r.Tercile3Count++
		}
	}
	r.ScoredSubsystems = len(active)
	for _, s := range feats {
		r.Detail = append(r.Detail, *s)
	}
	sort.Slice(r.Detail, func(i, j int) bool { return r.Detail[i].Name < r.Detail[j].Name })

	// The case's primary subsystem: most changed lines, ties to the higher score.
	for name, lines := range touchedLines {
		t := Touched{Name: name, ChangedLines: lines}
		if s, ok := feats[name]; ok {
			t.Score, t.Tercile = s.Score, s.Tercile
		}
		r.Touched = append(r.Touched, t)
	}
	sort.Slice(r.Touched, func(i, j int) bool {
		a, b := r.Touched[i], r.Touched[j]
		if a.ChangedLines != b.ChangedLines {
			return a.ChangedLines > b.ChangedLines
		}
		if sa, sb := deref(a.Score), deref(b.Score); sa != sb {
			return sa > sb
		}
		return a.Name < b.Name
	})
	prim := r.Touched[0]
	r.Subsystem = prim.Name
	s, known := feats[prim.Name]
	switch {
	case !known || s.Commits == 0:
		r.Reason = fmt.Sprintf("primary subsystem %q has no commits in the window", prim.Name)
		risky := false
		r.Risky = &risky
	case s.LowActivity:
		r.Reason = fmt.Sprintf("primary subsystem %q has %d commits in the window, below min_commits %d (low_activity)", prim.Name, s.Commits, MinCommits)
		risky := false
		r.Risky = &risky
	default:
		r.Score, r.Tercile = s.Score, s.Tercile
		risky := s.Tercile == 3
		r.Risky = &risky
	}
	return r, nil
}

func deref(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}

// primary returns the key with the most lines; ties break on the higher score when
// scores are given, then on name.
func primary(lines map[string]int, score map[string]float64) string {
	best, bestLines := "", -1
	for name, n := range lines {
		switch {
		case n > bestLines:
			best, bestLines = name, n
		case n == bestLines:
			if score != nil && score[name] != score[best] {
				if score[name] > score[best] {
					best = name
				}
			} else if name < best {
				best = name
			}
		}
	}
	return best
}

// mergePRNumber recognises GitHub's merge-commit subject.
func mergePRNumber(subject string) (int, bool) {
	const prefix = "Merge pull request #"
	if !strings.HasPrefix(subject, prefix) {
		return 0, false
	}
	rest := subject[len(prefix):]
	n := 0
	for _, r := range rest {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n, n > 0
}

// percentile is (count strictly below + 0.5 * count equal) / n.
func percentile(values []float64, v float64) float64 {
	if len(values) == 0 {
		return 0
	}
	below, equal := 0, 0
	for _, x := range values {
		switch {
		case x < v:
			below++
		case x == v:
			equal++
		}
	}
	return (float64(below) + 0.5*float64(equal)) / float64(len(values))
}

// tercile is 1, 2 or 3 from the fraction of scores strictly below v, so a tie on a
// boundary falls to the lower tercile.
func tercile(scores []float64, v float64) int {
	if len(scores) == 0 {
		return 0
	}
	below := 0
	for _, x := range scores {
		if x < v {
			below++
		}
	}
	p := float64(below) / float64(len(scores))
	switch {
	case p < 1.0/3:
		return 1
	case p < 2.0/3:
		return 2
	default:
		return 3
	}
}

// ReadCases decodes a cohort.jsonl and returns the cases with the input's hash.
func ReadCases(path string) ([]Case, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	var cases []Case
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var c Case
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, "", fmt.Errorf("%s line %d: %w", path, i+1, err)
		}
		if !strings.HasPrefix(c.SchemaVersion, "cohort-case/") {
			return nil, "", fmt.Errorf("%s line %d: not a cohort case (schema_version %q)", path, i+1, c.SchemaVersion)
		}
		cases = append(cases, c)
	}
	return cases, hex.EncodeToString(sum[:]), nil
}

// WriteAll writes one file per case into a new directory, atomically.
func WriteAll(out string, results []*Result) error {
	if _, err := os.Lstat(out); err == nil {
		return fmt.Errorf("output directory %q already exists; baseline files are immutable, choose a new path", out)
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(out)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".history-baseline-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, r := range results {
		if seen[r.CaseID] {
			return fmt.Errorf("two cases produce case id %s; refusing to overwrite", r.CaseID)
		}
		seen[r.CaseID] = true
		b, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(tmp, r.CaseID+".json"), append(b, '\n'), 0o644); err != nil {
			return err
		}
	}
	return os.Rename(tmp, out)
}
