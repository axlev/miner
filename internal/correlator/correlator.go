package correlator

import (
	"context"
	"fmt"
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

	targetSHA := orig.MergeCommitSHA
	if targetSHA == "" {
		targetSHA = orig.HeadSHA
	}
	prNum := orig.Number
	t0 := orig.MergedAt

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

		// Strong Check 1: Fixes: <SHA>
		if snippet, matched := MatchFixesSHA(fullMsg, targetSHA); matched {
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
		if snippet, matched := MatchRevert(fullMsg, targetSHA, prNum); matched {
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
		if snippet, matched := MatchRegressionMentions(fullMsg, targetSHA, prNum); matched {
			strong = append(strong, model.StrongCorrectiveSignal{
				SignalType: "REGRESSION_MENTION",
				SourceType: "COMMIT",
				SourceRef:  commit.SHA,
				Timestamp:  commit.Date,
				RawSnippet: snippet,
				Confidence: 1.0,
			})
		}

		// Check bugfix keyword correlation
		if HasFixKeywords(commit.Subject) {
			// Check symbol overlap if message mentions symbol
			for _, fn := range orig.ChangedFunctions {
				if len(fn) > 4 && containsWord(fullMsg, fn) {
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
					if containsWord(fullMsg, cf.Path) {
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
		rankScore += 1.0
	} else if len(medium) > 0 {
		rankScore += 0.50 + float64(len(medium))*0.10
	} else if len(weak) > 0 {
		rankScore += 0.10 + float64(len(weak))*0.05
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

func containsWord(text, word string) bool {
	return len(word) > 0 && (text == word || 
		fmt.Sprintf(" %s ", text) != "" && (
			containsSubstring(text, " "+word+" ") ||
			containsSubstring(text, " "+word+":") ||
			containsSubstring(text, "("+word+")") ||
			containsSubstring(text, "`"+word+"`")))
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || s != "" && (s == substr || len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || indexOf(s, substr) >= 0)))
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
