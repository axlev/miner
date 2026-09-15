// Package contamkeys builds the per-case contamination-keys file the engine's
// post-hoc contamination scan reads: every fixing commit and PR, any CVE identifier
// the fixes mention, and the verbatim post-merge discussion that describes the
// outcome.
//
// Everything here is oracle-grade, evaluator-only material. A keys file names the
// fix and quotes the discussion of the defect; it must never reach a reviewer, an
// engine-visible path, or either coder role (system-design.md §13), and it is never
// written under a prospective bundle root.
package contamkeys

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"miner/internal/buildinfo"
	"miner/internal/collector"
	"miner/internal/model"
	"miner/internal/prospectiveexport"
)

// SchemaVersion identifies the keys file.
const SchemaVersion = "contamination-keys/v1"

// Keys is one case's file: <keys-dir>/<case_id>.json.
type Keys struct {
	SchemaVersion string `json:"schema_version"`
	CaseID        string `json:"case_id"`
	// FixingSHAs is the plain list the scanner reads: every fixing commit, all tiers,
	// full 40-hex, sorted. Its shape is frozen for the scanner; tier and source live in
	// FixingCommits, which the scanner ignores.
	FixingSHAs []string `json:"fixing_shas"`
	// FixingPRNumbers are PRs that carried a fix — never the case's own PR.
	FixingPRNumbers []int          `json:"fixing_pr_numbers"`
	CVEIDs          []string       `json:"cve_ids"`
	Discussion      []Discussion   `json:"discussion"`
	FixingCommits   []FixingCommit `json:"fixing_commits"`
	Provenance      Provenance     `json:"provenance"`
}

// FixingCommit is the evaluator's view of one entry in FixingSHAs.
type FixingCommit struct {
	SHA        string `json:"sha"`
	Tier       string `json:"tier"`        // "strong" | "medium"
	SourceType string `json:"source_type"` // as recorded on the signal
	MatchedBy  string `json:"matched_by"`  // signal_type
}

// Discussion is one verbatim post-merge text.
type Discussion struct {
	Source    string `json:"source"` // URL when the provider gave one, else a stable ref
	Kind      string `json:"kind"`   // fixing_commit_message | fixing_pr_title | fixing_pr_body | fixing_pr_comment | fixing_pr_review_comment | issue_title | issue_body | issue_comment | own_pr_comment | own_pr_review_comment
	Author    string `json:"author,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	Body      string `json:"body"`
}

// Provenance says how the file was produced and what it could not establish.
type Provenance struct {
	ProducedBy   string `json:"produced_by"`
	ProducedAt   string `json:"produced_at"`
	CohortExport string `json:"cohort_export"`
	Repository   string `json:"repository"`
	PR           int    `json:"pr"`
	Cutoff       string `json:"cutoff"`
	SamplerLabel string `json:"sampler_label,omitempty"`
	Offline      bool   `json:"offline"`
	// Incomplete lists every source that could not be fetched to completion. A
	// non-empty list means the discussion scan may miss outcome text.
	Incomplete []string `json:"incomplete"`
	Notes      string   `json:"notes,omitempty"`
}

// Complete reports whether every source was established.
func (k *Keys) Complete() bool { return len(k.Provenance.Incomplete) == 0 }

// Fetcher supplies the post-cutoff material a keys file needs. The GitHub
// implementation caches every answer; tests supply a fake.
type Fetcher interface {
	// CommitMessage returns the full message of a commit from the local mirror.
	CommitMessage(ctx context.Context, sha string) (string, error)
	// PullRequestsForCommit returns the numbers of PRs associated with a commit.
	PullRequestsForCommit(ctx context.Context, sha string) ([]int, error)
	// PullRequestBundle returns a fixing PR's bundle: body, comments, review comments.
	PullRequestBundle(ctx context.Context, number int) (*collector.RawPRBundle, error)
	// Issue returns an issue's title, body and comments; isPR is true when the number
	// names a pull request, which is then not an issue thread.
	Issue(ctx context.Context, number int) (issue *IssueThread, isPR bool, err error)
	// OwnBundle returns the case's own collect-time bundle from the cache.
	OwnBundle(number int) (*collector.RawPRBundle, error)
}

// IssueThread is the part of an issue the scan reads.
type IssueThread struct {
	Number   int
	Title    string
	Body     string
	HTMLURL  string
	Author   string
	Comments []IssueComment
	Complete bool
}

// IssueComment is one comment on an issue thread.
type IssueComment struct {
	HTMLURL   string
	Author    string
	CreatedAt time.Time
	Body      string
}

// Case is the subset of a cohort case the builder reads. It is decoded loosely so
// any cohort-case version with these fields works.
type Case struct {
	SchemaVersion string                  `json:"schema_version"`
	SamplerLabel  string                  `json:"sampler_label"`
	Repository    string                  `json:"repository"`
	PR            int                     `json:"pr"`
	Record        model.PRCandidateRecord `json:"record"`
}

// Options control one build.
type Options struct {
	// Cutoff overrides the case's merged_at as the case-id cutoff.
	Cutoff  time.Time
	Offline bool
}

var (
	cvePattern  = regexp.MustCompile(`(?i)\bCVE-\d{4}-\d{4,}\b`)
	refPattern  = regexp.MustCompile(`(?:^|[^\w/])#(\d+)\b`)
	fullSHAOnly = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Build assembles one case's keys. It never fails on a missing or unfetchable
// source: the source is listed in Provenance.Incomplete and the file is still
// produced, because a scan with partial keys is better than none and the gap is
// recorded where the evaluator reads it.
func Build(ctx context.Context, c Case, f Fetcher, opt Options) (*Keys, error) {
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
	k := &Keys{
		SchemaVersion:   SchemaVersion,
		CaseID:          prospectiveexport.GenerateCaseID(c.Repository, c.PR, cutoff),
		FixingSHAs:      []string{},
		FixingPRNumbers: []int{},
		CVEIDs:          []string{},
		Discussion:      []Discussion{},
		FixingCommits:   []FixingCommit{},
		Provenance: Provenance{
			ProducedBy:   buildinfo.MinerVersion(),
			ProducedAt:   time.Now().UTC().Format(time.RFC3339),
			CohortExport: c.SchemaVersion,
			Repository:   c.Repository,
			PR:           c.PR,
			Cutoff:       cutoff.Format(time.RFC3339Nano),
			SamplerLabel: c.SamplerLabel,
			Offline:      opt.Offline,
			Incomplete:   []string{},
		},
	}
	incomplete := func(what string, err error) {
		k.Provenance.Incomplete = append(k.Provenance.Incomplete, fmt.Sprintf("%s: %v", what, err))
	}
	var texts []string // everything the CVE regex scans
	addDiscussion := func(d Discussion) {
		if strings.TrimSpace(d.Body) == "" {
			return
		}
		k.Discussion = append(k.Discussion, d)
		texts = append(texts, d.Body)
	}

	// 1. Fixing commits from the signals, strongest tier first, deduplicated.
	commits := map[string]FixingCommit{}
	for _, s := range c.Record.Retrospective.StrongSignals {
		if fullSHAOnly.MatchString(s.SourceRef) {
			commits[s.SourceRef] = FixingCommit{SHA: s.SourceRef, Tier: "strong", SourceType: s.SourceType, MatchedBy: s.SignalType}
		} else {
			incomplete("strong signal "+s.SignalType, fmt.Errorf("source_ref %q is not a full SHA", s.SourceRef))
		}
	}
	for _, m := range c.Record.Retrospective.MediumSignals {
		if _, seen := commits[m.SourceRef]; seen {
			continue
		}
		if fullSHAOnly.MatchString(m.SourceRef) {
			commits[m.SourceRef] = FixingCommit{SHA: m.SourceRef, Tier: "medium", SourceType: m.SourceType, MatchedBy: m.SignalType}
		} else {
			incomplete("medium signal "+m.SignalType, fmt.Errorf("source_ref %q is not a full SHA", m.SourceRef))
		}
	}
	for sha := range commits {
		k.FixingSHAs = append(k.FixingSHAs, sha)
	}
	sort.Strings(k.FixingSHAs)
	for _, sha := range k.FixingSHAs {
		k.FixingCommits = append(k.FixingCommits, commits[sha])
	}

	// 2. Fixing commit messages (mirror) and fixing PR numbers (provider).
	refs := map[int]bool{}
	fixingPRs := map[int]bool{}
	for _, sha := range k.FixingSHAs {
		msg, err := f.CommitMessage(ctx, sha)
		if err != nil {
			incomplete("commit message "+sha, err)
		} else {
			addDiscussion(Discussion{Source: "commit/" + sha, Kind: "fixing_commit_message", Body: msg})
			collectRefs(msg, refs)
		}
		nums, err := f.PullRequestsForCommit(ctx, sha)
		if err != nil {
			incomplete("pull requests for commit "+sha, err)
			continue
		}
		for _, n := range nums {
			if n != c.PR {
				fixingPRs[n] = true
			}
		}
	}
	for n := range fixingPRs {
		k.FixingPRNumbers = append(k.FixingPRNumbers, n)
	}
	sort.Ints(k.FixingPRNumbers)

	// 3. Fixing PR text and comments.
	for _, n := range k.FixingPRNumbers {
		b, err := f.PullRequestBundle(ctx, n)
		if err != nil || b == nil || b.PR == nil {
			if err == nil {
				err = fmt.Errorf("empty bundle")
			}
			incomplete(fmt.Sprintf("fixing PR #%d", n), err)
			continue
		}
		url := b.PR.GetHTMLURL()
		if url == "" {
			url = fmt.Sprintf("%s#%d", c.Repository, n)
		}
		addDiscussion(Discussion{Source: url + "#title", Kind: "fixing_pr_title", Author: b.PR.GetUser().GetLogin(), CreatedAt: stamp(b.PR.GetCreatedAt().Time), Body: b.PR.GetTitle()})
		addDiscussion(Discussion{Source: url + "#body", Kind: "fixing_pr_body", Author: b.PR.GetUser().GetLogin(), CreatedAt: stamp(b.PR.GetCreatedAt().Time), Body: b.PR.GetBody()})
		collectRefs(b.PR.GetTitle()+"\n"+b.PR.GetBody(), refs)
		if !b.IssueCommentsComplete {
			incomplete(fmt.Sprintf("fixing PR #%d comments", n), fmt.Errorf("not fetched to completion"))
		}
		for _, cm := range b.IssueComments {
			addDiscussion(Discussion{Source: orRef(cm.GetHTMLURL(), fmt.Sprintf("%s#%d/comment/%d", c.Repository, n, cm.GetID())), Kind: "fixing_pr_comment", Author: cm.GetUser().GetLogin(), CreatedAt: stamp(cm.GetCreatedAt().Time), Body: cm.GetBody()})
		}
		if !b.ReviewCommentsComplete {
			incomplete(fmt.Sprintf("fixing PR #%d review comments", n), fmt.Errorf("not fetched to completion"))
		}
		for _, rc := range b.ReviewComments {
			addDiscussion(Discussion{Source: orRef(rc.GetHTMLURL(), fmt.Sprintf("%s#%d/review-comment/%d", c.Repository, n, rc.GetID())), Kind: "fixing_pr_review_comment", Author: rc.GetUser().GetLogin(), CreatedAt: stamp(rc.GetCreatedAt().Time), Body: rc.GetBody()})
		}
	}

	// 4. Issues explicitly referenced by the fixes (the v1 rule: nothing else).
	var issueNums []int
	for n := range refs {
		if n != c.PR && !fixingPRs[n] {
			issueNums = append(issueNums, n)
		}
	}
	sort.Ints(issueNums)
	for _, n := range issueNums {
		th, isPR, err := f.Issue(ctx, n)
		if err != nil {
			incomplete(fmt.Sprintf("referenced issue #%d", n), err)
			continue
		}
		if isPR || th == nil {
			continue // a PR reference that no fixing commit belongs to is not an issue thread
		}
		url := orRef(th.HTMLURL, fmt.Sprintf("%s#%d", c.Repository, n))
		addDiscussion(Discussion{Source: url + "#title", Kind: "issue_title", Author: th.Author, Body: th.Title})
		addDiscussion(Discussion{Source: url + "#body", Kind: "issue_body", Author: th.Author, Body: th.Body})
		if !th.Complete {
			incomplete(fmt.Sprintf("referenced issue #%d comments", n), fmt.Errorf("not fetched to completion"))
		}
		for _, cm := range th.Comments {
			addDiscussion(Discussion{Source: orRef(cm.HTMLURL, fmt.Sprintf("%s#%d/comment", c.Repository, n)), Kind: "issue_comment", Author: cm.Author, CreatedAt: stamp(cm.CreatedAt), Body: cm.Body})
		}
	}

	// 5. The case's own post-merge discussion, from its collect-time bundle.
	own, err := f.OwnBundle(c.PR)
	if err != nil || own == nil {
		if err == nil {
			err = fmt.Errorf("no bundle")
		}
		incomplete("own PR bundle", err)
	} else {
		if !own.IssueCommentsComplete {
			incomplete("own PR comments", fmt.Errorf("bundle %q holds an incomplete comment list (below 1.2.0 or fetch failed)", own.BundleVersion))
		}
		if !own.ReviewCommentsComplete {
			incomplete("own PR review comments", fmt.Errorf("bundle %q holds an incomplete review-comment list", own.BundleVersion))
		}
		if !own.FetchedAt.IsZero() {
			k.Provenance.Notes = "own PR discussion is as of the collect-time bundle, fetched " + own.FetchedAt.UTC().Format(time.RFC3339)
		}
		for _, cm := range own.IssueComments {
			if cm.GetCreatedAt().Time.After(cutoff) {
				addDiscussion(Discussion{Source: orRef(cm.GetHTMLURL(), fmt.Sprintf("%s#%d/comment/%d", c.Repository, c.PR, cm.GetID())), Kind: "own_pr_comment", Author: cm.GetUser().GetLogin(), CreatedAt: stamp(cm.GetCreatedAt().Time), Body: cm.GetBody()})
			}
		}
		for _, rc := range own.ReviewComments {
			if rc.GetCreatedAt().Time.After(cutoff) {
				addDiscussion(Discussion{Source: orRef(rc.GetHTMLURL(), fmt.Sprintf("%s#%d/review-comment/%d", c.Repository, c.PR, rc.GetID())), Kind: "own_pr_review_comment", Author: rc.GetUser().GetLogin(), CreatedAt: stamp(rc.GetCreatedAt().Time), Body: rc.GetBody()})
			}
		}
	}

	// 6. CVE identifiers, from everything gathered.
	cves := map[string]bool{}
	for _, t := range texts {
		for _, m := range cvePattern.FindAllString(t, -1) {
			cves[strings.ToUpper(m)] = true
		}
	}
	for id := range cves {
		k.CVEIDs = append(k.CVEIDs, id)
	}
	sort.Strings(k.CVEIDs)
	sort.SliceStable(k.Discussion, func(i, j int) bool {
		if k.Discussion[i].Kind != k.Discussion[j].Kind {
			return k.Discussion[i].Kind < k.Discussion[j].Kind
		}
		return k.Discussion[i].Source < k.Discussion[j].Source
	})
	sort.Strings(k.Provenance.Incomplete)
	return k, nil
}

func collectRefs(text string, into map[int]bool) {
	for _, m := range refPattern.FindAllStringSubmatch(text, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			into[n] = true
		}
	}
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func orRef(url, ref string) string {
	if url != "" {
		return url
	}
	return ref
}

// ReadCases decodes a cohort.jsonl.
func ReadCases(path string) ([]Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []Case
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var c Case
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, i+1, err)
		}
		if !strings.HasPrefix(c.SchemaVersion, "cohort-case/") {
			return nil, fmt.Errorf("%s line %d: not a cohort case (schema_version %q)", path, i+1, c.SchemaVersion)
		}
		cases = append(cases, c)
	}
	return cases, nil
}

// WriteAll writes one file per case into a new directory, atomically: the
// directory is built under a temporary name and renamed into place.
func WriteAll(out string, keys []*Keys) error {
	if _, err := os.Lstat(out); err == nil {
		return fmt.Errorf("output directory %q already exists; keys are immutable, choose a new path", out)
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(out)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".contamination-keys-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k.CaseID] {
			return fmt.Errorf("two cases produce case id %s; refusing to overwrite", k.CaseID)
		}
		seen[k.CaseID] = true
		b, err := json.MarshalIndent(k, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(tmp, k.CaseID+".json"), append(b, '\n'), 0o644); err != nil {
			return err
		}
	}
	return os.Rename(tmp, out)
}
