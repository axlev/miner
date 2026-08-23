package gitx

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GitCommandTimeout is the per-command timeout for git subprocess execution.
const GitCommandTimeout = 15 * time.Second

var (
	diffGitOldPathRegex = regexp.MustCompile(`^diff --git a/(\S+) b/(\S+)`)
	hunkU0Regex         = regexp.MustCompile(`^@@\s+-(\d+)(?:,(\d+))?\s+\+(\d+)(?:,(\d+))?\s+@@`)
)

// RemovedLineLocation specifies an old-side line removed or replaced in the parent commit.
type RemovedLineLocation struct {
	Path       string
	LineNumber int
	Content    string
}

// BlameRange represents an inclusive 1-indexed line range [StartLine, EndLine].
type BlameRange struct {
	StartLine int
	EndLine   int
}

// GroupContiguousRanges groups a slice of line numbers into sorted, non-overlapping contiguous ranges.
func GroupContiguousRanges(lines []int) []BlameRange {
	if len(lines) == 0 {
		return nil
	}

	uniqueMap := make(map[int]bool)
	for _, l := range lines {
		if l > 0 {
			uniqueMap[l] = true
		}
	}

	var sortedLines []int
	for l := range uniqueMap {
		sortedLines = append(sortedLines, l)
	}
	sort.Ints(sortedLines)

	if len(sortedLines) == 0 {
		return nil
	}

	var ranges []BlameRange
	start := sortedLines[0]
	end := sortedLines[0]

	for i := 1; i < len(sortedLines); i++ {
		cur := sortedLines[i]
		if cur == end+1 {
			end = cur
		} else {
			ranges = append(ranges, BlameRange{StartLine: start, EndLine: end})
			start = cur
			end = cur
		}
	}
	ranges = append(ranges, BlameRange{StartLine: start, EndLine: end})

	return ranges
}

// CommitParentInfo captures parent resolution and removed line locations for a commit.
type CommitParentInfo struct {
	SHA          string
	ParentSHA    string
	IsMerge      bool
	IsRoot       bool
	RemovedLines []RemovedLineLocation
}

// InspectCommitParentAndRemovedLines inspects the commit's parent count and extracts old-side removed lines via -U0.
func (r *Repository) InspectCommitParentAndRemovedLines(ctx context.Context, sha string) (*CommitParentInfo, error) {
	if sha == "" {
		return nil, fmt.Errorf("empty sha")
	}

	cmdCtx1, cancel1 := context.WithTimeout(ctx, GitCommandTimeout)
	parentsCmd := exec.CommandContext(cmdCtx1, "git", "--git-dir", r.RepoDir, "rev-list", "--parents", "-n", "1", sha)
	parentsOut, err := parentsCmd.Output()
	cancel1()
	if err != nil {
		return nil, fmt.Errorf("git rev-list --parents failed for %s: %w", sha, err)
	}

	parents := strings.Fields(string(parentsOut))
	if len(parents) > 2 {
		return &CommitParentInfo{
			SHA:     sha,
			IsMerge: true,
		}, nil
	}
	if len(parents) < 2 {
		return &CommitParentInfo{
			SHA:    sha,
			IsRoot: true,
		}, nil
	}

	parentSHA := parents[1]

	// Extract -U0 diff (zero context lines) to get exact old-side removed lines
	cmdCtx2, cancel2 := context.WithTimeout(ctx, GitCommandTimeout)
	cmd := exec.CommandContext(cmdCtx2, "git", "--git-dir", r.RepoDir, "show", "--format=", "-p", "-U0", "-M", sha)
	out, err := cmd.Output()
	cancel2()
	if err != nil {
		return nil, fmt.Errorf("git show -U0 failed for %s: %w", sha, err)
	}

	diffText := string(out)
	diffText = strings.ReplaceAll(diffText, "\r\n", "\n")
	diffText = strings.ReplaceAll(diffText, "\r", "\n")
	lines := strings.Split(diffText, "\n")

	var removedLines []RemovedLineLocation
	curPath := ""
	inHunk := false
	curOldLine := 0

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			inHunk = false
			if m := diffGitOldPathRegex.FindStringSubmatch(line); len(m) > 1 {
				curPath = m[1]
			}
			continue
		}

		if strings.HasPrefix(line, "@@") {
			inHunk = false
			if m := hunkU0Regex.FindStringSubmatch(line); len(m) > 1 {
				start, err := strconv.Atoi(m[1])
				if err == nil {
					count := 1
					if m[2] != "" {
						count, _ = strconv.Atoi(m[2])
					}
					if count > 0 {
						inHunk = true
						curOldLine = start
					}
				}
			}
			continue
		}

		if inHunk && strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			if curPath != "" && curOldLine > 0 {
				removedLines = append(removedLines, RemovedLineLocation{
					Path:       curPath,
					LineNumber: curOldLine,
					Content:    line[1:],
				})
			}
			curOldLine++
		}
	}

	return &CommitParentInfo{
		SHA:          sha,
		ParentSHA:    parentSHA,
		IsMerge:      false,
		IsRoot:       false,
		RemovedLines: removedLines,
	}, nil
}

// BlameLineRange executes git blame --line-porcelain for a specific line range in the parent commit.
func (r *Repository) BlameLineRange(ctx context.Context, parentSHA, path string, startLine, endLine int) (map[int]string, error) {
	if parentSHA == "" || path == "" || startLine <= 0 || endLine < startLine {
		return nil, fmt.Errorf("invalid blame range arguments: sha=%q path=%q range=%d-%d", parentSHA, path, startLine, endLine)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, GitCommandTimeout)
	defer cancel()

	rangeArg := fmt.Sprintf("-L%d,%d", startLine, endLine)
	cmd := exec.CommandContext(cmdCtx, "git", "--git-dir", r.RepoDir, "blame", "--line-porcelain", "-M", "-C", rangeArg, parentSHA, "--", path)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git blame failed for %s %s [%d-%d]: %w", parentSHA, path, startLine, endLine, err)
	}

	output := string(out)
	output = strings.ReplaceAll(output, "\r\n", "\n")
	output = strings.ReplaceAll(output, "\r", "\n")
	lines := strings.Split(output, "\n")

	lineMap := make(map[int]string)
	currentSHA := ""

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Check if line starts with 40-character hex commit SHA
		fields := strings.Fields(line)
		if len(fields) >= 3 && len(fields[0]) == 40 && isHex(fields[0]) {
			currentSHA = strings.ToLower(fields[0])
			finalLine, err := strconv.Atoi(fields[2])
			if err == nil && finalLine > 0 {
				lineMap[finalLine] = currentSHA
			}
		}
	}

	return lineMap, nil
}

// ReverseCommitDiff generates the reverse patch from sha to parentSHA and computes its stable patch ID.
func (r *Repository) ReverseCommitDiff(ctx context.Context, sha, parentSHA string) (rawDiff string, patchID string, err error) {
	if sha == "" || parentSHA == "" {
		return "", "", fmt.Errorf("empty sha or parentSHA")
	}

	cmdCtx1, cancel1 := context.WithTimeout(ctx, GitCommandTimeout)
	cmd := exec.CommandContext(cmdCtx1, "git", "--git-dir", r.RepoDir, "diff-tree", "-p", "-M", sha, parentSHA)
	out, err := cmd.Output()
	cancel1()
	if err != nil {
		return "", "", fmt.Errorf("git diff-tree failed for reverse diff %s -> %s: %w", sha, parentSHA, err)
	}

	diffText := string(out)

	cmdCtx2, cancel2 := context.WithTimeout(ctx, GitCommandTimeout)
	patchIDCmd := exec.CommandContext(cmdCtx2, "git", "--git-dir", r.RepoDir, "patch-id", "--stable")
	patchIDCmd.Stdin = strings.NewReader(diffText)
	patchIDOut, patchErr := patchIDCmd.Output()
	cancel2()
	if patchErr != nil {
		return diffText, "", fmt.Errorf("git patch-id failed for reverse diff %s -> %s: %w", sha, parentSHA, patchErr)
	}

	parts := strings.Fields(string(patchIDOut))
	if len(parts) == 0 || parts[0] == "" {
		return diffText, "", fmt.Errorf("git patch-id returned empty output for reverse diff %s -> %s", sha, parentSHA)
	}
	patchID = parts[0]

	return diffText, patchID, nil
}

func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
