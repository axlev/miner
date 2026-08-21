package correlator

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	// Strong Fixes: <SHA> regex
	fixesSHARegex = regexp.MustCompile(`(?i)(?:fixes|fixed-by):\s*([0-9a-f]{7,40})`)

	// Strong Fixes: #<PR_NUM> regex
	fixesPRRegex = regexp.MustCompile(`(?i)(?:fixes|fixed-by|closes|resolves):\s*#([0-9]+)`)

	// Strong Revert regex
	revertCommitRegex = regexp.MustCompile(`(?i)This reverts commit\s+([0-9a-f]{7,40})`)
	revertSubjectRegex = regexp.MustCompile(`(?i)Revert "(.*)"`)

	// Strong Regression mention regex (matches "regression in #123", "regression introduced in #123", "caused by #123", "broken by commit <sha>")
	regressionRegex = regexp.MustCompile(`(?i)(?:regression(?:\s+(?:introduced\s+in|caused\s+by|in))?|broken\s+by|introduced\s+in|introduced\s+by|caused\s+by|fault\s+in)\s+(?:commit\s+([0-9a-f]{7,40})|#([0-9]+)|([0-9a-f]{7,40}))`)

	// Bugfix keywords for commit messages
	fixKeywordRegex = regexp.MustCompile(`(?i)\b(fix|fixes|fixed|crash|leak|race|panic|null|segfault|deadlock|regression|memory corruption|use-after-free)\b`)
)

// MatchFixesSHA checks if a message contains a Fixes: tag matching target SHA or prefix.
func MatchFixesSHA(message, targetSHA string) (string, bool) {
	if targetSHA == "" || len(targetSHA) < 7 {
		return "", false
	}
	matches := fixesSHARegex.FindAllStringSubmatch(message, -1)
	for _, m := range matches {
		if len(m) > 1 {
			shaInMsg := strings.ToLower(m[1])
			lowerTarget := strings.ToLower(targetSHA)
			if strings.HasPrefix(lowerTarget, shaInMsg) || strings.HasPrefix(shaInMsg, lowerTarget[:7]) {
				return m[0], true
			}
		}
	}
	return "", false
}

// MatchFixesPR checks if a message fixes the target PR number.
func MatchFixesPR(message string, targetPRNumber int) (string, bool) {
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

// MatchRevert checks if a message reverts the target PR or commit SHA.
func MatchRevert(message, targetSHA string, targetPRNumber int) (string, bool) {
	if targetSHA != "" {
		matches := revertCommitRegex.FindAllStringSubmatch(message, -1)
		for _, m := range matches {
			if len(m) > 1 {
				shaInMsg := strings.ToLower(m[1])
				lowerTarget := strings.ToLower(targetSHA)
				if strings.HasPrefix(lowerTarget, shaInMsg) || strings.HasPrefix(shaInMsg, lowerTarget[:7]) {
					return m[0], true
				}
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

// MatchRegressionMentions checks if a message explicitly cites the PR or SHA as the cause/regression.
func MatchRegressionMentions(message, targetSHA string, targetPRNumber int) (string, bool) {
	matches := regressionRegex.FindAllStringSubmatch(message, -1)
	for _, m := range matches {
		if len(m) > 1 && m[1] != "" && targetSHA != "" {
			shaInMsg := strings.ToLower(m[1])
			lowerTarget := strings.ToLower(targetSHA)
			if strings.HasPrefix(lowerTarget, shaInMsg) || strings.HasPrefix(shaInMsg, lowerTarget[:7]) {
				return m[0], true
			}
		}
		if len(m) > 2 && m[2] != "" {
			if num, err := strconv.Atoi(m[2]); err == nil && num == targetPRNumber {
				return m[0], true
			}
		}
	}
	return "", false
}

// HasFixKeywords returns true if message contains bugfix/corrective terminology.
func HasFixKeywords(message string) bool {
	return fixKeywordRegex.MatchString(message)
}
