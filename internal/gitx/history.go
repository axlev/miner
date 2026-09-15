package gitx

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// HistoryCommit is one commit of a bounded history walk with its per-file churn.
type HistoryCommit struct {
	SHA           string
	CommitterDate time.Time
	AuthorDate    time.Time
	Subject       string
	Message       string
	Parents       []string
	// Files maps path -> (additions, deletions); binary files count as 0/0.
	Files map[string][2]int
}

// HistoryWalk lists every ancestor of rev (rev itself included) whose committer
// date is in [since, until), with per-file numstat. It is one git process; the
// caller applies any further filtering. Dates are compared on the committer date
// because that is when a commit existed on the branch — the author date is when
// it was written, which can be earlier by any amount.
func (r *Repository) HistoryWalk(ctx context.Context, rev string, since, until time.Time) ([]HistoryCommit, error) {
	args := []string{
		"--git-dir", r.RepoDir, "log", rev,
		"--since=" + since.UTC().Format(time.RFC3339),
		"--until=" + until.UTC().Format(time.RFC3339),
		"--numstat", "--no-renames",
		"--format=%x1e%H%x00%cI%x00%aI%x00%P%x00%s%x00%B%x1f",
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log %s failed: %w", rev, err)
	}
	var commits []HistoryCommit
	for _, block := range strings.Split(string(out), "\x1e") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		head, rest, ok := strings.Cut(block, "\x1f")
		if !ok {
			return nil, fmt.Errorf("git log output missing record terminator")
		}
		parts := strings.Split(head, "\x00")
		if len(parts) < 6 {
			return nil, fmt.Errorf("git log record has %d fields, want 6", len(parts))
		}
		cd, err := time.Parse(time.RFC3339, parts[1])
		if err != nil {
			return nil, fmt.Errorf("commit %s: committer date %q: %w", parts[0], parts[1], err)
		}
		ad, _ := time.Parse(time.RFC3339, parts[2])
		c := HistoryCommit{SHA: parts[0], CommitterDate: cd.UTC(), AuthorDate: ad.UTC(), Subject: parts[4], Message: parts[5], Files: map[string][2]int{}}
		if p := strings.TrimSpace(parts[3]); p != "" {
			c.Parents = strings.Fields(p)
		}
		// git's --since/--until already filter on committer date; the explicit check
		// makes the half-open interval exact regardless of git's rounding.
		if c.CommitterDate.Before(since) || !c.CommitterDate.Before(until) {
			continue
		}
		for _, line := range strings.Split(rest, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			f := strings.SplitN(line, "\t", 3)
			if len(f) != 3 {
				continue
			}
			add, _ := strconv.Atoi(f[0]) // "-" for binary parses to 0
			del, _ := strconv.Atoi(f[1])
			c.Files[f[2]] = [2]int{add, del}
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// ResolveCommit returns the full SHA for rev, or an error when it is not a commit
// in this repository. Prefixes of six or more hex characters resolve when unique.
func (r *Repository) ResolveCommit(ctx context.Context, rev string) (string, error) {
	out, err := r.gitOutput(ctx, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%s does not resolve to a commit", rev)
	}
	return strings.TrimSpace(string(out)), nil
}
