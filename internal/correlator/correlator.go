package correlator

import (
	"context"
	"strings"
	"time"

	"pr-analysis/internal/gitx"
	"pr-analysis/internal/model"
)

// Correlator handles retrospective cross-referencing between pre-merge PRs and post-merge history.
type Correlator struct {
	gitRepo *gitx.Repository
}

// NewCorrelator creates a Correlator instance.
func NewCorrelator(gitRepo *gitx.Repository) *Correlator {
	return &Correlator{gitRepo: gitRepo}
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

	// Filter changed functions with length >= 4
	var validFunctions []string
	for _, fn := range orig.ChangedFunctions {
		if len(fn) >= 4 {
			validFunctions = append(validFunctions, fn)
		}
	}

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

		if hasFixCitation {
			// Strong Check 1: Fixes: <SHA>
			if snippet, matched := MatchFixesSHA(fullMsg, targetSHAs); matched {
				strong = append(strong, model.StrongCorrectiveSignal{
					SignalType: "FIXES_SHA",
					SourceType: "COMMIT",
					SourceRef:  commit.SHA,
					Timestamp:  commit.Date,
					RawSnippet: snippet,
					Confidence: 1.0,
				})
			}

			// Strong Check 2: Fixes #<PR_NUM>
			if snippet, matched := MatchFixesPR(fullMsg, prNum); matched {
				strong = append(strong, model.StrongCorrectiveSignal{
					SignalType: "FIXES_PR",
					SourceType: "COMMIT",
					SourceRef:  commit.SHA,
					Timestamp:  commit.Date,
					RawSnippet: snippet,
					Confidence: 1.0,
				})
			}

			// Strong Check 3: Revert commit
			if snippet, matched := MatchRevert(fullMsg, targetSHAs, prNum); matched {
				strong = append(strong, model.StrongCorrectiveSignal{
					SignalType: "EXPLICIT_REVERT",
					SourceType: "COMMIT",
					SourceRef:  commit.SHA,
					Timestamp:  commit.Date,
					RawSnippet: snippet,
					Confidence: 1.0,
				})
			}

			// Strong Check 4: Regression mention
			if snippet, matched := MatchRegressionMentions(fullMsg, targetSHAs, prNum); matched {
				strong = append(strong, model.StrongCorrectiveSignal{
					SignalType: "REGRESSION_MENTION",
					SourceType: "COMMIT",
					SourceRef:  commit.SHA,
					Timestamp:  commit.Date,
					RawSnippet: snippet,
					Confidence: 1.0,
				})
			}
		}

		// Check bugfix keyword correlation for medium & weak signals
		if HasFixKeywords(commit.Subject) {
			// Check symbol overlap
			for _, fn := range validFunctions {
				if strings.Contains(fullMsg, fn) {
					medium = append(medium, model.MediumCorrectiveSignal{
						SignalType:     "SAME_FUNCTION_FIX",
						SourceType:     "COMMIT",
						SourceRef:      commit.SHA,
						Timestamp:      commit.Date,
						FunctionOrPath: fn,
						Context:        commit.Subject,
					})
				}
			}

			// Check file overlap if within 90 days
			if commit.Date.Sub(t0) <= 90*24*time.Hour {
				for _, cf := range orig.ChangedFiles {
					if len(cf.Path) > 5 && strings.Contains(fullMsg, cf.Path) {
						weak = append(weak, model.WeakCorrectiveSignal{
							SignalType: "SAME_FILE_MODIFICATION",
							SourceRef:  commit.SHA,
							Timestamp:  commit.Date,
							FilePath:   cf.Path,
							Context:    commit.Subject,
						})
					}
				}
			}
		}
	}

	// Calculate summary rank score: strong signals weighted heavily
	rankScore := 0.0
	if len(strong) > 0 {
		rankScore = 1.0
	} else if len(medium) > 0 {
		rankScore = 0.50 + float64(len(medium))*0.05
	} else if len(weak) > 0 {
		rankScore = 0.10 + float64(len(weak))*0.02
	}
	if rankScore > 1.0 {
		rankScore = 1.0
	}

	return model.RetrospectiveEvidence{
		SummaryRankScore: rankScore,
		StrongSignals:    strong,
		MediumSignals:    medium,
		WeakSignals:      weak,
	}
}
