package correlator

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	// Strong Fixes: <SHA> regex
	// Matches: "Fixes: 1fb48f5", "Fixes: commit 1fb48f5", "Fixes 1fb48f5", "fixed-by: 1fb48f5"
	fixesSHARegex = regexp.MustCompile(`(?i)(?:fixes|fixed-by|fixes\s+commit|fixed\s+by|fixes\s+in):\s*["']?([0-9a-f]{6,40})["']?`)

	// Strong Fixes: #<PR_NUM> regex
	// Matches: "Fixes: #16194", "Fixes #16194", "Closes #16194", "Resolves #16194"
	fixesPRRegex = regexp.MustCompile(`(?i)(?:fixes|fixed-by|closes|resolves|reverts)\s*[:]?\s*#([0-9]+)\b`)

	// Strong Revert regex
	// Matches: "This reverts commit 1fb48f5...", "Revert commit 1fb48f5...", "Revert \"...\""
	revertCommitRegex  = regexp.MustCompile(`(?i)(?:this\s+reverts\s+commit|revert\s+commit|reverts\s+commit|reverted\s+commit)\s+["']?([0-9a-f]{6,40})["']?`)
	revertSubjectRegex = regexp.MustCompile(`(?i)Revert\s+"(.*)"`)

	// Strong Regression mention regex
	// Matches: "regression introduced in 1fb48f5", "regression in #16194", "broken by commit 1fb48f5", "caused by #16194"
	regressionRegex = regexp.MustCompile(`(?i)(?:regression(?:\s+(?:introduced\s+in|caused\s+by|in))?|broken\s+by|introduced\s+in|introduced\s+by|caused\s+by|fault\s+in|broke\s+in)\s+(?:commit\s+([0-9a-f]{6,40})|#([0-9]+)|([0-9a-f]{6,40}))`)

	// Bugfix keywords for commit messages
	fixKeywordRegex = regexp.MustCompile(`(?i)\b(fix|fixes|fixed|crash|leak|race|panic|null|segfault|deadlock|regression|memory corruption|use-after-free)\b`)
)

// matchSHAList returns true if shaInMsg matches any candidate target SHA (prefix matching >= 6 chars).
func matchSHAList(shaInMsg string, targetSHAs []string) bool {
	if len(shaInMsg) < 6 {
		return false
	}
	shaInMsg = strings.ToLower(shaInMsg)

	for _, target := range targetSHAs {
		if len(target) < 6 {
			continue
		}
		targetLower := strings.ToLower(target)
		if strings.HasPrefix(targetLower, shaInMsg) || strings.HasPrefix(shaInMsg, targetLower) {
			return true
		}
	}
	return false
}

// MatchFixesSHA checks if a message contains a Fixes: tag matching any target SHA or prefix.
func MatchFixesSHA(message string, targetSHAs []string) (string, bool) {
	if len(targetSHAs) == 0 {
		return "", false
	}
	matches := fixesSHARegex.FindAllStringSubmatch(message, -1)
	for _, m := range matches {
		if len(m) > 1 && matchSHAList(m[1], targetSHAs) {
			return m[0], true
		}
	}
	return "", false
}

// MatchFixesPR checks if a message fixes the target PR number.
func MatchFixesPR(message string, targetPRNumber int) (string, bool) {
	if targetPRNumber <= 0 {
		return "", false
	}
	matches := fixesPRRegex.FindAllStringSubmatch(message, -1)
	for _, m := range matches {
		if len(m) > 1 {
			if num, err := strconv.Atoi(m[1]); err == nil && num == targetPRNumber {
				return m[0], true
			}
		}
	}
	return "", false
}

// MatchRevert checks if a message reverts any target SHA or PR number.
func MatchRevert(message string, targetSHAs []string, targetPRNumber int) (string, bool) {
	if len(targetSHAs) > 0 {
		matches := revertCommitRegex.FindAllStringSubmatch(message, -1)
		for _, m := range matches {
			if len(m) > 1 && matchSHAList(m[1], targetSHAs) {
				return m[0], true
			}
		}
	}

	if targetPRNumber > 0 {
		matches := revertSubjectRegex.FindAllStringSubmatch(message, -1)
		for _, m := range matches {
			if len(m) > 1 && strings.Contains(m[1], fmt.Sprintf("#%d", targetPRNumber)) {
				return m[0], true
			}
		}
	}

	return "", false
}

// MatchRegressionMentions checks if a message explicitly cites the PR or any SHA as the cause/regression.
func MatchRegressionMentions(message string, targetSHAs []string, targetPRNumber int) (string, bool) {
	matches := regressionRegex.FindAllStringSubmatch(message, -1)
	for _, m := range matches {
		if len(m) > 1 && m[1] != "" && matchSHAList(m[1], targetSHAs) {
			return m[0], true
		}
		if len(m) > 2 && m[2] != "" && targetPRNumber > 0 {
			if num, err := strconv.Atoi(m[2]); err == nil && num == targetPRNumber {
				return m[0], true
			}
		}
		if len(m) > 3 && m[3] != "" && matchSHAList(m[3], targetSHAs) {
			return m[0], true
		}
	}
	return "", false
}

// StrongCorrectiveRefs extracts every strong-tier corrective reference a message
// carries, without a target: the SHAs and PR numbers named by the same Fixes:,
// revert, and regression-attribution regexes CorrelatePR matches against a specific
// PR. matched is true when any strong pattern fired, even one whose reference could
// not be captured. This is the history baseline's corrective-commit test; it reuses
// the matchers rather than restating them so the two can never drift.
func StrongCorrectiveRefs(message string) (shas []string, prs []int, matched bool) {
	seenSHA := map[string]bool{}
	seenPR := map[int]bool{}
	addSHA := func(s string) {
		s = strings.ToLower(s)
		if s != "" && !seenSHA[s] {
			seenSHA[s] = true
			shas = append(shas, s)
		}
	}
	addPR := func(s string) {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && !seenPR[n] {
			seenPR[n] = true
			prs = append(prs, n)
		}
	}
	for _, m := range fixesSHARegex.FindAllStringSubmatch(message, -1) {
		matched = true
		if len(m) > 1 {
			addSHA(m[1])
		}
	}
	for _, m := range fixesPRRegex.FindAllStringSubmatch(message, -1) {
		matched = true
		if len(m) > 1 {
			addPR(m[1])
		}
	}
	for _, m := range revertCommitRegex.FindAllStringSubmatch(message, -1) {
		matched = true
		if len(m) > 1 {
			addSHA(m[1])
		}
	}
	if revertSubjectRegex.MatchString(message) {
		matched = true
	}
	for _, m := range regressionRegex.FindAllStringSubmatch(message, -1) {
		matched = true
		if len(m) > 1 && m[1] != "" {
			addSHA(m[1])
		}
		if len(m) > 2 && m[2] != "" {
			addPR(m[2])
		}
		if len(m) > 3 && m[3] != "" {
			addSHA(m[3])
		}
	}
	return shas, prs, matched
}

// HasFixKeywords returns true if message contains bugfix/corrective terminology.
func HasFixKeywords(message string) bool {
	return fixKeywordRegex.MatchString(message)
}
