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
	SHA       string
	Date      time.Time
	Author    string
	Subject   string
	Body      string
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
