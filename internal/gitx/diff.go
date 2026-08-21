package gitx

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"pr-analysis/internal/model"
)

// DiffResult holds the extracted file modifications, diff text, and symbols.
type DiffResult struct {
	Files   []model.ChangedFile
	Symbols []string
	RawDiff string
}

// DiffBetween returns the diff and extracted symbols between baseSHA and headSHA.
func (r *Repository) DiffBetween(ctx context.Context, baseSHA, headSHA string) (*DiffResult, error) {
	// git diff -p -M generates unified diff with C function names and rename detection
	cmd := exec.CommandContext(ctx, "git", "--git-dir", r.RepoDir, "diff", "-p", "-M", baseSHA, headSHA)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff failed for %s..%s: %w", baseSHA, headSHA, err)
	}

	diffText := string(out)
	symbols := ExtractHunkSymbols(diffText)

	// Fetch numstat for accurate file stats
	statCmd := exec.CommandContext(ctx, "git", "--git-dir", r.RepoDir, "diff", "--numstat", "-M", baseSHA, headSHA)
	statOut, err := statCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --numstat failed: %w", err)
	}

	files := ParseNumstat(string(statOut))

	return &DiffResult{
		Files:   files,
		Symbols: symbols,
		RawDiff: diffText,
	}, nil
}

// ParseNumstat parses output of `git diff --numstat`.
func ParseNumstat(output string) []model.ChangedFile {
	var files []model.ChangedFile
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}

		addStr := parts[0]
		delStr := parts[1]
		path := parts[2]

		additions, _ := strconv.Atoi(addStr)
		deletions, _ := strconv.Atoi(delStr)

		status := "modified"
		if additions > 0 && deletions == 0 {
			status = "added"
		} else if additions == 0 && deletions > 0 {
			status = "deleted"
		}

		files = append(files, model.ChangedFile{
			Path:      path,
			Status:    status,
			Additions: additions,
			Deletions: deletions,
		})
	}

	return files
}
