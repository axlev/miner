package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Repository encapsulates local git operations on a repository clone.
type Repository struct {
	RepoDir string
}

// OpenRepository returns a Repository handle for the given directory.
func OpenRepository(repoDir string) *Repository {
	dotGit := filepath.Join(repoDir, ".git")
	if fi, err := os.Stat(dotGit); err == nil && fi.IsDir() {
		return &Repository{RepoDir: dotGit}
	}
	return &Repository{RepoDir: repoDir}
}

// EnsureClone ensures a local bare clone of remoteURL exists at repoDir.
func EnsureClone(ctx context.Context, repoDir, remoteURL string) (*Repository, error) {
	if _, err := os.Stat(filepath.Join(repoDir, "HEAD")); err == nil {
		// Repo exists, fetch latest updates
		repo := &Repository{RepoDir: repoDir}
		if err := repo.Fetch(ctx); err != nil {
			// Non-fatal if offline, log warning
			fmt.Printf("Warning: failed to fetch remote for %s: %v (continuing with cached history)\n", repoDir, err)
		}
		return repo, nil
	}

	if err := os.MkdirAll(filepath.Dir(repoDir), 0755); err != nil {
		return nil, fmt.Errorf("failed to create parent directory: %w", err)
	}

	fmt.Printf("Cloning %s into %s (bare mirror)...\n", remoteURL, repoDir)
	cmd := exec.CommandContext(ctx, "git", "clone", "--bare", remoteURL, repoDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git clone failed: %s (%w)", stderr.String(), err)
	}

	return &Repository{RepoDir: repoDir}, nil
}

// Fetch pulls latest commits from origin.
func (r *Repository) Fetch(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "git", "--git-dir", r.RepoDir, "fetch", "origin", "+refs/heads/*:refs/heads/*", "+refs/pull/*/head:refs/pull/*")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git fetch failed: %s (%w)", stderr.String(), err)
	}
	return nil
}

// HeadSHA returns the current HEAD commit hash.
func (r *Repository) HeadSHA(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "--git-dir", r.RepoDir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// CommitLogEntry represents a parsed git commit.
type CommitLogEntry struct {
	SHA         string
	Date        time.Time
	Author      string
	Subject     string
	Body        string
	FullMessage string
}

// CommitsAfter returns all commits on refs/heads/master or main between t0 and observationEnd.
func (r *Repository) CommitsAfter(ctx context.Context, t0 time.Time, observationEnd time.Time) ([]CommitLogEntry, error) {
	// git log format with zero byte delimiter: %H%x00%aI%x00%an%x00%B%x1f
	// %x1f is Unit Separator between commits, %x00 is null separator between fields
	args := []string{
		"--git-dir", r.RepoDir,
		"log",
		"--all",
		"--since=" + t0.UTC().Format(time.RFC3339),
		"--format=%H%x00%aI%x00%an%x00%B%x1f",
	}

	if !observationEnd.IsZero() {
		args = append(args, "--until="+observationEnd.UTC().Format(time.RFC3339))
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log failed: %w", err)
	}

	rawCommits := strings.Split(string(out), "\x1f")
	var entries []CommitLogEntry

	for _, raw := range rawCommits {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		parts := strings.Split(raw, "\x00")
		if len(parts) < 4 {
			continue
		}

		sha := parts[0]
		dateStr := parts[1]
		author := parts[2]
		msg := parts[3]

		commitDate, err := time.Parse(time.RFC3339, dateStr)
		if err != nil {
			commitDate, _ = time.Parse("2006-01-02T15:04:05-0700", dateStr)
		}

		lines := strings.SplitN(strings.TrimSpace(msg), "\n", 2)
		subject := lines[0]
		body := ""
		if len(lines) > 1 {
			body = strings.TrimSpace(lines[1])
		}

		entries = append(entries, CommitLogEntry{
			SHA:         sha,
			Date:        commitDate,
			Author:      author,
			Subject:     subject,
			Body:        body,
			FullMessage: msg,
		})
	}

	return entries, nil
}

// CommitDiffSummary captures the parsed diff characteristics of a single commit.
type CommitDiffSummary struct {
	SHA          string
	ChangedFiles []string
	FileSymbols  map[string][]string // relativePath -> []functionNames
	RawDiff      string
	PatchID      string
	IsMerge      bool
}

// CommitDiff inspects a single commit and extracts its changed files, C symbols, patch ID, and merge status.
func (r *Repository) CommitDiff(ctx context.Context, sha string) (*CommitDiffSummary, error) {
	if sha == "" {
		return nil, fmt.Errorf("empty sha")
	}

	// 1. Check parent count to determine if it's a merge commit
	cmdCtx1, cancel1 := context.WithTimeout(ctx, GitCommandTimeout)
	parentsCmd := exec.CommandContext(cmdCtx1, "git", "--git-dir", r.RepoDir, "rev-list", "--parents", "-n", "1", sha)
	parentsOut, err := parentsCmd.Output()
	cancel1()
	isMerge := false
	if err == nil {
		parents := strings.Fields(string(parentsOut))
		if len(parents) > 2 {
			isMerge = true
		}
	}

	// Merge commits are not treated as atomic single patches
	if isMerge {
		return &CommitDiffSummary{
			SHA:          sha,
			ChangedFiles: nil,
			FileSymbols:  nil,
			RawDiff:      "",
			PatchID:      "sha:" + sha,
			IsMerge:      true,
		}, nil
	}

	// 2. Fetch patch-only output for C symbol extraction and patch identity
	cmdCtx2, cancel2 := context.WithTimeout(ctx, GitCommandTimeout)
	patchCmd := exec.CommandContext(cmdCtx2, "git", "--git-dir", r.RepoDir, "show", "--format=", "-p", "-M", sha)
	patchOut, err := patchCmd.Output()
	cancel2()
	if err != nil {
		return nil, fmt.Errorf("git show -p failed for %s: %w", sha, err)
	}
	patchText := string(patchOut)
	parsedSymbols := ExtractFileHunkSymbols(patchText)

	// 3. Fetch numstat-only output for changed paths
	cmdCtx3, cancel3 := context.WithTimeout(ctx, GitCommandTimeout)
	numstatCmd := exec.CommandContext(cmdCtx3, "git", "--git-dir", r.RepoDir, "show", "--format=", "--numstat", "-M", sha)
	numstatOut, err := numstatCmd.Output()
	cancel3()
	if err != nil {
		return nil, fmt.Errorf("git show --numstat failed for %s: %w", sha, err)
	}
	numstatText := string(numstatOut)
	files := ParseNumstat(numstatText)

	var changedPaths []string
	for _, f := range files {
		changedPaths = append(changedPaths, f.Path)
	}

	// 4. Compute stable patch ID via git patch-id
	cmdCtx4, cancel4 := context.WithTimeout(ctx, GitCommandTimeout)
	patchIDCmd := exec.CommandContext(cmdCtx4, "git", "--git-dir", r.RepoDir, "patch-id", "--stable")
	patchIDCmd.Stdin = strings.NewReader(patchText)
	patchIDOut, patchIDErr := patchIDCmd.Output()
	cancel4()
	if patchIDErr != nil {
		return nil, fmt.Errorf("git patch-id failed for %s: %w", sha, patchIDErr)
	}
	parts := strings.Fields(string(patchIDOut))
	if len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("git patch-id returned empty output for %s", sha)
	}
	patchID := parts[0]

	return &CommitDiffSummary{
		SHA:          sha,
		ChangedFiles: changedPaths,
		FileSymbols:  parsedSymbols.PathSymbols,
		RawDiff:      patchText,
		PatchID:      patchID,
		IsMerge:      false,
	}, nil
}
