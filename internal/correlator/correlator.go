package correlator

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"pr-analysis/internal/gitx"
	"pr-analysis/internal/model"
)

type singleflightDiffEntry struct {
	once    sync.Once
	summary *gitx.CommitDiffSummary
	err     error
}

type singleflightParentInfoEntry struct {
	once sync.Once
	info *gitx.CommitParentInfo
	err  error
}

type singleflightBlameEntry struct {
	once    sync.Once
	lineMap map[int]string
	err     error
}

type singleflightOrigDiffEntry struct {
	once       sync.Once
	rawDiff    string
	patchID    string
	patchIDErr error
	diffErr    error
}

type singleflightReverseDiffEntry struct {
	once    sync.Once
	rawDiff string
	patchID string
	err     error
}

// Correlator handles retrospective cross-referencing between pre-merge PRs and post-merge history.
type Correlator struct {
	gitRepo          *gitx.Repository
	diffCache        sync.Map // sha string -> *singleflightDiffEntry
	parentInfoCache  sync.Map // sha string -> *singleflightParentInfoEntry
	blameRangeCache  sync.Map // key string -> *singleflightBlameEntry
	origDiffCache    sync.Map // base...head string -> *singleflightOrigDiffEntry
	reverseDiffCache sync.Map // sha->parent string -> *singleflightReverseDiffEntry
}

// NewCorrelator creates a Correlator instance with an initialized commit diff cache.
func NewCorrelator(gitRepo *gitx.Repository) *Correlator {
	return &Correlator{
		gitRepo: gitRepo,
	}
}

// getCommitDiff safely retrieves or computes the parsed diff summary for a commit SHA once under concurrency.
func (c *Correlator) getCommitDiff(ctx context.Context, sha string) (*gitx.CommitDiffSummary, error) {
	if c.gitRepo == nil || sha == "" {
		return nil, fmt.Errorf("no repository or empty sha")
	}

	val, _ := c.diffCache.LoadOrStore(sha, &singleflightDiffEntry{})
	entry := val.(*singleflightDiffEntry)
	entry.once.Do(func() {
		entry.summary, entry.err = c.gitRepo.CommitDiff(ctx, sha)
	})
	if entry.err != nil {
		c.diffCache.Delete(sha)
		return nil, entry.err
	}
	return entry.summary, nil
}

func (c *Correlator) getParentInfo(ctx context.Context, sha string) (*gitx.CommitParentInfo, error) {
	if c.gitRepo == nil || sha == "" {
		return nil, fmt.Errorf("no repository or empty sha")
	}

	val, _ := c.parentInfoCache.LoadOrStore(sha, &singleflightParentInfoEntry{})
	entry := val.(*singleflightParentInfoEntry)
	entry.once.Do(func() {
		entry.info, entry.err = c.gitRepo.InspectCommitParentAndRemovedLines(ctx, sha)
	})
	if entry.err != nil {
		c.parentInfoCache.Delete(sha)
		return nil, entry.err
	}
	return entry.info, nil
}

func (c *Correlator) getBlameRange(ctx context.Context, parentSHA, path string, startLine, endLine int) (map[int]string, error) {
	if c.gitRepo == nil || parentSHA == "" || path == "" {
		return nil, fmt.Errorf("invalid blame parameters")
	}

	key := fmt.Sprintf("%s:%s:%d-%d", parentSHA, path, startLine, endLine)
	val, _ := c.blameRangeCache.LoadOrStore(key, &singleflightBlameEntry{})
	entry := val.(*singleflightBlameEntry)
	entry.once.Do(func() {
		entry.lineMap, entry.err = c.gitRepo.BlameLineRange(ctx, parentSHA, path, startLine, endLine)
	})
	if entry.err != nil {
		c.blameRangeCache.Delete(key)
		return nil, entry.err
	}
	return entry.lineMap, nil
}

func (c *Correlator) getOriginalPRDiff(ctx context.Context, baseSHA, headSHA string) (string, string, error, error) {
	if c.gitRepo == nil || baseSHA == "" || headSHA == "" {
		return "", "", nil, fmt.Errorf("invalid base or head SHA")
	}

	key := baseSHA + "..." + headSHA
	val, _ := c.origDiffCache.LoadOrStore(key, &singleflightOrigDiffEntry{})
	entry := val.(*singleflightOrigDiffEntry)
	entry.once.Do(func() {
		diffRes, err := c.gitRepo.DiffPR(ctx, baseSHA, headSHA)
		if err != nil {
			entry.diffErr = err
			return
		}
		entry.rawDiff = diffRes.RawDiff
		entry.patchID, entry.patchIDErr = computeStablePatchID(ctx, c.gitRepo.RepoDir, diffRes.RawDiff)
	})
	if entry.diffErr != nil {
		c.origDiffCache.Delete(key)
		return "", "", nil, entry.diffErr
	}
	return entry.rawDiff, entry.patchID, entry.patchIDErr, nil
}

func (c *Correlator) getReverseDiff(ctx context.Context, sha, parentSHA string) (string, string, error) {
	if c.gitRepo == nil || sha == "" || parentSHA == "" {
		return "", "", fmt.Errorf("invalid sha or parentSHA")
	}

	key := sha + "->" + parentSHA
	val, _ := c.reverseDiffCache.LoadOrStore(key, &singleflightReverseDiffEntry{})
	entry := val.(*singleflightReverseDiffEntry)
	entry.once.Do(func() {
		entry.rawDiff, entry.patchID, entry.err = c.gitRepo.ReverseCommitDiff(ctx, sha, parentSHA)
	})
	if entry.err != nil {
		c.reverseDiffCache.Delete(key)
		return "", "", entry.err
	}
	return entry.rawDiff, entry.patchID, nil
}

// ClassifyEvidenceScope determines whether changed file paths represent code, test, docs, packaging, mixed, or unknown.
func ClassifyEvidenceScope(paths []string) model.EvidenceScope {
	if len(paths) == 0 {
		return model.ScopeUnknown
	}

	hasCode, hasTest, hasDocs, hasPackaging := false, false, false, false
	for _, p := range paths {
		norm := strings.ToLower(p)
		switch {
		case strings.HasPrefix(norm, "tests/") || strings.HasPrefix(norm, "topotests/") || strings.Contains(norm, "/topotests/"):
			hasTest = true
		case strings.HasPrefix(norm, "doc/") || strings.HasPrefix(norm, "docs/") || strings.HasPrefix(norm, "man/") || strings.HasSuffix(norm, ".rst") || strings.HasSuffix(norm, ".md"):
			hasDocs = true
		case strings.HasPrefix(norm, "debian/") || strings.HasPrefix(norm, "redhat/") || strings.HasPrefix(norm, "docker/") || strings.HasPrefix(norm, "pkg/") || strings.HasSuffix(norm, ".spec"):
			hasPackaging = true
		default:
			hasCode = true
		}
	}

	categories := 0
	if hasCode {
		categories++
	}
	if hasTest {
		categories++
	}
	if hasDocs {
		categories++
	}
	if hasPackaging {
		categories++
	}

	if categories > 1 {
		return model.ScopeMixed
	}
	if hasCode {
		return model.ScopeCode
	}
	if hasTest {
		return model.ScopeTest
	}
	if hasDocs {
		return model.ScopeDocs
	}
	if hasPackaging {
		return model.ScopePackaging
	}
	return model.ScopeUnknown
}

// CorrelatePR analyzes post-T0 commits and events up to observationEnd to detect retrospective signals.
func (c *Correlator) CorrelatePR(ctx context.Context, orig *model.OriginalPR, postCommits []gitx.CommitLogEntry, observationEnd time.Time) model.RetrospectiveEvidence {
	var strong []model.StrongCorrectiveSignal
	var medium []model.MediumCorrectiveSignal
	var weak []model.WeakCorrectiveSignal

	// Collect all candidate SHAs for this PR (Merge commit SHA, Head commit SHA, and PR branch commits)
	shaMap := make(map[string]bool)
	if orig.MergeCommitSHA != "" {
		shaMap[orig.MergeCommitSHA] = true
	}
	if orig.HeadSHA != "" {
		shaMap[orig.HeadSHA] = true
	}
	for _, sha := range orig.CommitSHAs {
		if sha != "" {
			shaMap[sha] = true
		}
	}

	var targetSHAs []string
	for sha := range shaMap {
		targetSHAs = append(targetSHAs, sha)
	}

	prNum := orig.Number
	t0 := orig.MergedAt

	// Build lookup map for original (path, function) locations
	origLocMap := make(map[string]map[string]bool) // path -> func -> bool
	for _, loc := range orig.ChangedFunctionLocations {
		if loc.Path != "" && loc.Function != "" {
			if origLocMap[loc.Path] == nil {
				origLocMap[loc.Path] = make(map[string]bool)
			}
			origLocMap[loc.Path][loc.Function] = true
		}
	}

	candidateLinkSHAs := make(map[string]bool)

	// 1. Scan Post-T0 Commits
	for _, commit := range postCommits {
		// Strict temporal boundary: ignore commits before or at T0, or after observationEnd
		if commit.Date.Before(t0) || commit.Date.Equal(t0) {
			continue
		}
		if !observationEnd.IsZero() && commit.Date.After(observationEnd) {
			continue
		}

		fullMsg := commit.FullMessage
		lowerMsg := strings.ToLower(fullMsg)

		// Fast pre-filter: only run strong regexes if message has citation keywords
		hasFixCitation := strings.Contains(lowerMsg, "fix") ||
			strings.Contains(lowerMsg, "revert") ||
			strings.Contains(lowerMsg, "regression") ||
			strings.Contains(lowerMsg, "broken") ||
			strings.Contains(lowerMsg, "caused") ||
			strings.Contains(lowerMsg, "close") ||
			strings.Contains(lowerMsg, "resolve")

		var commitSummary *gitx.CommitDiffSummary
		getSummary := func() *gitx.CommitDiffSummary {
			if commitSummary == nil {
				summary, err := c.getCommitDiff(ctx, commit.SHA)
				if err == nil {
					commitSummary = summary
				}
			}
			return commitSummary
		}

		if hasFixCitation {
			// Helper to populate signal metadata from commit diff
			makeSignal := func(signalType, rawSnippet string) model.StrongCorrectiveSignal {
				scope := model.ScopeUnknown
				patchID := "sha:" + commit.SHA
				var paths []string

				if summary := getSummary(); summary != nil {
					if summary.IsMerge {
						// For merge commits, scope is ScopeUnknown and patch ID is sha:<commit-sha>
						scope = model.ScopeUnknown
						patchID = "sha:" + commit.SHA
						paths = nil
					} else {
						scope = ClassifyEvidenceScope(summary.ChangedFiles)
						patchID = summary.PatchID
						paths = summary.ChangedFiles
					}
				}

				candidateLinkSHAs[commit.SHA] = true

				return model.StrongCorrectiveSignal{
					SignalType:     signalType,
					SourceType:     "COMMIT",
					SourceRef:      commit.SHA,
					Timestamp:      commit.Date,
					RawSnippet:     rawSnippet,
					Confidence:     1.0,
					EvidenceScope:  scope,
					LogicalPatchID: patchID,
					ChangedPaths:   paths,
				}
			}

			// Strong Check 1: Fixes: <SHA>
			if snippet, matched := MatchFixesSHA(fullMsg, targetSHAs); matched {
				strong = append(strong, makeSignal("FIXES_SHA", snippet))
			}

			// Strong Check 2: Fixes #<PR_NUM>
			if snippet, matched := MatchFixesPR(fullMsg, prNum); matched {
				strong = append(strong, makeSignal("FIXES_PR", snippet))
			}

			// Strong Check 3: Revert commit
			if snippet, matched := MatchRevert(fullMsg, targetSHAs, prNum); matched {
				strong = append(strong, makeSignal("EXPLICIT_REVERT", snippet))
			}

			// Strong Check 4: Regression mention
			if snippet, matched := MatchRegressionMentions(fullMsg, targetSHAs, prNum); matched {
				strong = append(strong, makeSignal("REGRESSION_MENTION", snippet))
			}
		}

		// Check bugfix keyword correlation for medium & weak signals
		// Merge commits must NOT emit diff-derived SAME_FUNCTION_FIX or SAME_FILE_MODIFICATION
		if HasFixKeywords(commit.Subject) || hasFixCitation {
			summary := getSummary()
			if summary != nil && !summary.IsMerge {
				scope := ClassifyEvidenceScope(summary.ChangedFiles)
				patchID := summary.PatchID

				// Medium Check: Strict SAME_FUNCTION_FIX (same function AND same file path)
				if len(orig.ChangedFunctionLocations) > 0 {
					matchedFuncs := make(map[string]bool) // "path:func" -> bool
					for path, funcs := range summary.FileSymbols {
						if origFuncs, ok := origLocMap[path]; ok {
							for _, f := range funcs {
								if origFuncs[f] {
									key := fmt.Sprintf("%s:%s", path, f)
									if !matchedFuncs[key] {
										matchedFuncs[key] = true
										candidateLinkSHAs[commit.SHA] = true
										medium = append(medium, model.MediumCorrectiveSignal{
											SignalType:     "SAME_FUNCTION_FIX",
											SourceType:     "COMMIT",
											SourceRef:      commit.SHA,
											Timestamp:      commit.Date,
											FunctionOrPath: key,
											Context:        commit.Subject,
											EvidenceScope:  scope,
											LogicalPatchID: patchID,
											ChangedPaths:   summary.ChangedFiles,
										})
									}
								}
							}
						}
					}
				} else if len(orig.ChangedFunctions) > 0 {
					// Compatibility fallback for legacy records without path-associated locations:
					// Do NOT claim SAME_FUNCTION_FIX; emit weaker compatibility signal
					for _, fn := range orig.ChangedFunctions {
						if len(fn) >= 6 && strings.Contains(fullMsg, fn) {
							weak = append(weak, model.WeakCorrectiveSignal{
								SignalType:    "LEGACY_FLAT_FUNCTION_MATCH",
								SourceRef:     commit.SHA,
								Timestamp:     commit.Date,
								FilePath:      fn,
								Context:       commit.Subject,
								EvidenceScope: scope,
							})
						}
					}
				}

				// Weak Check: File overlap within 90 days
				if commit.Date.Sub(t0) <= 90*24*time.Hour {
					origFileMap := make(map[string]bool)
					for _, cf := range orig.ChangedFiles {
						origFileMap[cf.Path] = true
					}
					for _, lp := range summary.ChangedFiles {
						if origFileMap[lp] {
							weak = append(weak, model.WeakCorrectiveSignal{
								SignalType:    "SAME_FILE_MODIFICATION",
								SourceRef:     commit.SHA,
								Timestamp:     commit.Date,
								FilePath:      lp,
								Context:       commit.Subject,
								EvidenceScope: scope,
							})
							break // at most one file modification signal per commit
						}
					}
				}
			} else if len(orig.ChangedFunctions) > 0 {
				// Fallback when summary / git clone is unavailable: legacy flat match emitted as weak
				for _, fn := range orig.ChangedFunctions {
					if len(fn) >= 6 && strings.Contains(fullMsg, fn) {
						weak = append(weak, model.WeakCorrectiveSignal{
							SignalType:    "LEGACY_FLAT_FUNCTION_MATCH",
							SourceRef:     commit.SHA,
							Timestamp:     commit.Date,
							FilePath:      fn,
							Context:       commit.Subject,
							EvidenceScope: model.ScopeUnknown,
						})
					}
				}
			}
		}
	}

	// 2. Enrich Retrospective Relationships for Qualified Candidate Links
	var relationships []model.CommitRelationshipEvidence
	if len(candidateLinkSHAs) > 0 {
		if c.gitRepo == nil {
			var candidateSHAs []string
			for sha := range candidateLinkSHAs {
				candidateSHAs = append(candidateSHAs, sha)
			}
			sort.Strings(candidateSHAs)
			for _, sha := range candidateSHAs {
				relationships = append(relationships, model.CommitRelationshipEvidence{
					SourceRef:      sha,
					AnalysisStatus: "unavailable",
					AnalysisNote:   "git repository handle is unavailable",
				})
			}
		} else {
			relationships = c.enrichRelationships(ctx, orig, candidateLinkSHAs)
		}
	}

	// Calculate summary rank score: deduplicate by LogicalPatchID
	uniqueStrongLeads := make(map[string]bool)
	for _, s := range strong {
		pid := s.LogicalPatchID
		if pid == "" {
			pid = "sha:" + s.SourceRef
		}
		uniqueStrongLeads[pid] = true
	}

	uniqueMediumLeads := make(map[string]bool)
	for _, m := range medium {
		pid := m.LogicalPatchID
		if pid == "" {
			pid = "sha:" + m.SourceRef
		}
		uniqueMediumLeads[pid] = true
	}

	rankScore := 0.0
	if len(uniqueStrongLeads) > 0 {
		rankScore = 1.0
	} else if len(uniqueMediumLeads) > 0 {
		rankScore = 0.50 + float64(len(uniqueMediumLeads))*0.05
	} else if len(weak) > 0 {
		rankScore = 0.10 + float64(len(weak))*0.02
	}
	if rankScore > 1.0 {
		rankScore = 1.0
	}

	return model.RetrospectiveEvidence{
		SummaryRankScore:    rankScore,
		StrongSignals:       strong,
		MediumSignals:       medium,
		WeakSignals:         weak,
		CommitRelationships: relationships,
	}
}

func (c *Correlator) enrichRelationships(
	ctx context.Context,
	orig *model.OriginalPR,
	candidateLinkSHAs map[string]bool,
) []model.CommitRelationshipEvidence {
	var results []model.CommitRelationshipEvidence

	// Build target canonical 40-character SHA set
	targetFullSHAs := make(map[string]bool)
	for _, sha := range orig.CommitSHAs {
		norm := strings.ToLower(strings.TrimSpace(sha))
		if len(norm) == 40 {
			targetFullSHAs[norm] = true
		}
	}
	if norm := strings.ToLower(strings.TrimSpace(orig.HeadSHA)); len(norm) == 40 {
		targetFullSHAs[norm] = true
	}
	if norm := strings.ToLower(strings.TrimSpace(orig.MergeCommitSHA)); len(norm) == 40 {
		targetFullSHAs[norm] = true
	}

	// Sort candidate SHAs for deterministic processing
	var candidateSHAs []string
	for sha := range candidateLinkSHAs {
		candidateSHAs = append(candidateSHAs, sha)
	}
	sort.Strings(candidateSHAs)

	// Obtain original PR merge-base diff text and patch ID
	var origDiffErr error
	var origPatchIDErr error
	var origDiffText, origPatchID string
	if orig.BaseSHA == "" || orig.HeadSHA == "" {
		origDiffErr = fmt.Errorf("unavailable: original PR base_sha or head_sha is empty")
	} else {
		origDiffText, origPatchID, origPatchIDErr, origDiffErr = c.getOriginalPRDiff(ctx, orig.BaseSHA, orig.HeadSHA)
	}

	for _, sha := range candidateSHAs {
		parentInfo, err := c.getParentInfo(ctx, sha)
		if err != nil {
			results = append(results, model.CommitRelationshipEvidence{
				SourceRef:      sha,
				AnalysisStatus: "error",
				AnalysisNote:   err.Error(),
			})
			continue
		}

		if parentInfo.IsMerge {
			results = append(results, model.CommitRelationshipEvidence{
				SourceRef:      sha,
				AnalysisStatus: "skipped_merge",
			})
			continue
		}

		if parentInfo.IsRoot {
			results = append(results, model.CommitRelationshipEvidence{
				SourceRef:      sha,
				AnalysisStatus: "skipped_root",
			})
			continue
		}

		// A. Direct Lineage Analysis via Range Blame
		totalRemovedLines := len(parentInfo.RemovedLines)
		linesByPath := make(map[string][]int)
		removedLocsByPath := make(map[string][]gitx.RemovedLineLocation)

		for _, r := range parentInfo.RemovedLines {
			linesByPath[r.Path] = append(linesByPath[r.Path], r.LineNumber)
			removedLocsByPath[r.Path] = append(removedLocsByPath[r.Path], r)
		}

		var sortedPaths []string
		for p := range linesByPath {
			sortedPaths = append(sortedPaths, p)
		}
		sort.Strings(sortedPaths)

		analyzedRemovedCount := 0
		linesBlamedCount := 0
		matchedSHAMap := make(map[string]bool)
		var blamedRemovedLines []gitx.RemovedLineLocation
		isTruncated := false
		var truncationReasons []string

		// Check path cap
		if len(sortedPaths) > MaxChangedPathsPerCommit {
			isTruncated = true
			truncationReasons = append(truncationReasons, fmt.Sprintf("changed paths limit exceeded (%d > %d)", len(sortedPaths), MaxChangedPathsPerCommit))
			sortedPaths = sortedPaths[:MaxChangedPathsPerCommit]
		}

		for _, p := range sortedPaths {
			if analyzedRemovedCount >= MaxRemovedLinesPerCommit {
				isTruncated = true
				truncationReasons = append(truncationReasons, fmt.Sprintf("removed lines limit exceeded (%d)", MaxRemovedLinesPerCommit))
				break
			}

			ranges := gitx.GroupContiguousRanges(linesByPath[p])
			if len(ranges) > MaxBlameRangesPerPath {
				isTruncated = true
				truncationReasons = append(truncationReasons, fmt.Sprintf("blame ranges limit exceeded for %s (%d > %d)", p, len(ranges), MaxBlameRangesPerPath))
				ranges = ranges[:MaxBlameRangesPerPath]
			}

			// Map for quick lookup of removed line locations in this path
			locByLine := make(map[int]gitx.RemovedLineLocation)
			for _, loc := range removedLocsByPath[p] {
				locByLine[loc.LineNumber] = loc
			}

			for _, rng := range ranges {
				if analyzedRemovedCount >= MaxRemovedLinesPerCommit {
					isTruncated = true
					break
				}

				blameMap, err := c.getBlameRange(ctx, parentInfo.ParentSHA, p, rng.StartLine, rng.EndLine)
				if err != nil {
					isTruncated = true
					truncationReasons = append(truncationReasons, fmt.Sprintf("blame failed for %s [%d-%d]: %v", p, rng.StartLine, rng.EndLine, err))
					continue
				}

				for lineNum := rng.StartLine; lineNum <= rng.EndLine; lineNum++ {
					if analyzedRemovedCount >= MaxRemovedLinesPerCommit {
						isTruncated = true
						break
					}
					analyzedRemovedCount++

					if originSHA, ok := blameMap[lineNum]; ok {
						if targetFullSHAs[originSHA] {
							linesBlamedCount++
							matchedSHAMap[originSHA] = true
							if loc, found := locByLine[lineNum]; found {
								blamedRemovedLines = append(blamedRemovedLines, loc)
							}
						}
					}
				}
			}
		}

		var matchedSHAs []string
		for s := range matchedSHAMap {
			matchedSHAs = append(matchedSHAs, s)
		}
		sort.Strings(matchedSHAs)

		directFraction := 0.0
		if analyzedRemovedCount > 0 {
			directFraction = float64(linesBlamedCount) / float64(analyzedRemovedCount)
		}

		lineageEv := &model.LineageEvidence{
			LaterRemovedLineCount:     totalRemovedLines,
			AnalyzedRemovedLineCount:  analyzedRemovedCount,
			LinesBlamedToOriginalPR:   linesBlamedCount,
			DirectLineageFraction:     directFraction,
			MatchedOriginalCommitSHAs: matchedSHAs,
			Method:                    "git_blame_range_porcelain",
		}

		// B. Reversal Analysis
		// 1) Forward patch of the later commit (parent -> later commit)
		laterSummary, fwdErr := c.getCommitDiff(ctx, sha)
		// 2) Reverse patch (later commit -> parent) used ONLY for exact full-reversal patch ID
		_, laterReversePatchID, revPatchIDErr := c.getReverseDiff(ctx, sha, parentInfo.ParentSHA)

		var reversalEv *model.ReversalEvidence
		var reversalNotes []string

		if origDiffErr != nil {
			reversalNotes = append(reversalNotes, fmt.Sprintf("orig raw diff: %v", origDiffErr))
		}
		if fwdErr != nil {
			reversalNotes = append(reversalNotes, fmt.Sprintf("forward diff: %v", fwdErr))
		}
		if origDiffErr == nil && fwdErr == nil && origPatchIDErr != nil {
			reversalNotes = append(reversalNotes, fmt.Sprintf("orig patch-id: %v", origPatchIDErr))
		}
		if origDiffErr == nil && fwdErr == nil && revPatchIDErr != nil {
			reversalNotes = append(reversalNotes, fmt.Sprintf("reverse patch-id: %v", revPatchIDErr))
		}

		if origDiffErr == nil && fwdErr == nil {
			// Both raw diffs available: compute line-overlap evidence
			fullReverseMatchAvailable := (origPatchIDErr == nil && revPatchIDErr == nil && origPatchID != "" && laterReversePatchID != "")
			fullReverseMatch := fullReverseMatchAvailable && (origPatchID == laterReversePatchID)

			rev := gitx.ComputeReversalEvidence(
				origDiffText,
				laterSummary.RawDiff, // FORWARD diff used to match additions vs removals!
				fullReverseMatch,
				blamedRemovedLines,
			)
			reversalEv = &rev
		}

		status := "complete"
		var noteParts []string
		if isTruncated {
			noteParts = append(noteParts, "truncated: "+strings.Join(truncationReasons, "; "))
		}
		if len(reversalNotes) > 0 {
			noteParts = append(noteParts, strings.Join(reversalNotes, "; "))
		}

		if isTruncated || len(reversalNotes) > 0 {
			status = "partial"
		}

		note := strings.Join(noteParts, " | ")

		results = append(results, model.CommitRelationshipEvidence{
			SourceRef:      sha,
			AnalysisStatus: status,
			AnalysisNote:   note,
			Lineage:        lineageEv,
			Reversal:       reversalEv,
		})
	}

	// Sort results ascending by SourceRef
	sort.Slice(results, func(i, j int) bool {
		return results[i].SourceRef < results[j].SourceRef
	})

	return results
}

func computeStablePatchID(ctx context.Context, repoDir, diffText string) (string, error) {
	if diffText == "" {
		return "", fmt.Errorf("empty diff text")
	}
	cmdCtx, cancel := context.WithTimeout(ctx, GitCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "git", "--git-dir", repoDir, "patch-id", "--stable")
	cmd.Stdin = strings.NewReader(diffText)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git patch-id failed: %w", err)
	}
	parts := strings.Fields(string(out))
	if len(parts) == 0 || parts[0] == "" {
		return "", fmt.Errorf("git patch-id returned empty output")
	}
	return parts[0], nil
}
