package gitx

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"miner/internal/model"
)

// DiffResult holds the extracted file modifications, diff text, and symbols.
type DiffResult struct {
	Files             []model.ChangedFile
	Symbols           []string
	FunctionLocations []model.ChangedFunctionLocation
	PathSymbols       map[string][]string
	RawDiff           string
}

// DiffBetween returns the two-endpoint diff and extracted symbols between baseSHA and headSHA.
func (r *Repository) DiffBetween(ctx context.Context, baseSHA, headSHA string) (*DiffResult, error) {
	cmdCtx1, cancel1 := context.WithTimeout(ctx, GitCommandTimeout)
	// git diff -p -M generates unified diff with C function names and rename detection
	cmd := exec.CommandContext(cmdCtx1, "git", "--git-dir", r.RepoDir, "diff", "-p", "-M", baseSHA, headSHA)
	out, err := cmd.Output()
	cancel1()
	if err != nil {
		return nil, fmt.Errorf("git diff failed for %s..%s: %w", baseSHA, headSHA, err)
	}

	diffText := string(out)
	parsedSymbols := ExtractFileHunkSymbols(diffText)

	// Fetch numstat for accurate file stats
	cmdCtx2, cancel2 := context.WithTimeout(ctx, GitCommandTimeout)
	statCmd := exec.CommandContext(cmdCtx2, "git", "--git-dir", r.RepoDir, "diff", "--numstat", "-M", baseSHA, headSHA)
	statOut, err := statCmd.Output()
	cancel2()
	if err != nil {
		return nil, fmt.Errorf("git diff --numstat failed: %w", err)
	}

	files := ParseNumstat(string(statOut))

	return &DiffResult{
		Files:             files,
		Symbols:           parsedSymbols.Symbols,
		FunctionLocations: parsedSymbols.FunctionLocations,
		PathSymbols:       parsedSymbols.PathSymbols,
		RawDiff:           diffText,
	}, nil
}

// DiffPR returns the merge-base PR diff (base...head) and extracted symbols.
func (r *Repository) DiffPR(ctx context.Context, baseSHA, headSHA string) (*DiffResult, error) {
	cmdCtx1, cancel1 := context.WithTimeout(ctx, GitCommandTimeout)
	tripleDot := fmt.Sprintf("%s...%s", baseSHA, headSHA)
	cmd := exec.CommandContext(cmdCtx1, "git", "--git-dir", r.RepoDir, "diff", "-p", "-M", tripleDot)
	out, err := cmd.Output()
	cancel1()
	if err != nil {
		return nil, fmt.Errorf("git diff failed for %s: %w", tripleDot, err)
	}

	diffText := string(out)
	parsedSymbols := ExtractFileHunkSymbols(diffText)

	cmdCtx2, cancel2 := context.WithTimeout(ctx, GitCommandTimeout)
	statCmd := exec.CommandContext(cmdCtx2, "git", "--git-dir", r.RepoDir, "diff", "--numstat", "-M", tripleDot)
	statOut, err := statCmd.Output()
	cancel2()
	if err != nil {
		return nil, fmt.Errorf("git diff --numstat failed for %s: %w", tripleDot, err)
	}

	files := ParseNumstat(string(statOut))

	return &DiffResult{
		Files:             files,
		Symbols:           parsedSymbols.Symbols,
		FunctionLocations: parsedSymbols.FunctionLocations,
		PathSymbols:       parsedSymbols.PathSymbols,
		RawDiff:           diffText,
	}, nil
}

// MergeBase resolves the merge-base commit between two refs.
func (r *Repository) MergeBase(ctx context.Context, a, b string) (string, error) {
	out, err := r.gitOutput(ctx, "merge-base", a, b)
	if err != nil {
		return "", fmt.Errorf("git merge-base failed for %s %s: %w", a, b, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// CanonicalChangePatch resolves the merge-base of baseSHA and headSHA and returns the
// unambiguous unified diff from that merge-base to headSHA, matching engine-runner's
// expected comparison semantics (equivalent to `git diff base...head` but with the
// resolved merge-base SHA available to the caller for the public manifest).
func (r *Repository) CanonicalChangePatch(ctx context.Context, baseSHA, headSHA string) (patch []byte, mergeBase string, err error) {
	mergeBase, err = r.MergeBase(ctx, baseSHA, headSHA)
	if err != nil {
		return nil, "", err
	}
	patch, err = r.gitOutput(ctx, "diff", "--no-ext-diff", "-M", "--binary", "--full-index", mergeBase, headSHA, "--")
	if err != nil {
		return nil, mergeBase, fmt.Errorf("git diff failed for %s..%s: %w", mergeBase, headSHA, err)
	}
	return patch, mergeBase, nil
}

// ParseNumstat parses output of `git diff --numstat` or `git show --numstat`.
func ParseNumstat(output string) []model.ChangedFile {
	var files []model.ChangedFile
	output = strings.ReplaceAll(output, "\r\n", "\n")
	output = strings.ReplaceAll(output, "\r", "\n")
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var addStr, delStr, path string

		// Standard git numstat is tab-delimited: "<added>\t<deleted>\t<path>"
		parts := strings.Split(line, "\t")
		if len(parts) >= 3 {
			addStr = parts[0]
			delStr = parts[1]
			path = strings.Join(parts[2:], "\t")
		} else {
			// Fallback to whitespace fields if tabs are not present
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				addStr = fields[0]
				delStr = fields[1]
				path = strings.Join(fields[2:], " ")
			} else {
				continue
			}
		}

		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		// Handle binary files: "-	-	path"
		isBinary := addStr == "-" && delStr == "-"
		var additions, deletions int

		if !isBinary {
			var err error
			additions, err = strconv.Atoi(addStr)
			if err != nil || additions < 0 {
				continue
			}
			deletions, err = strconv.Atoi(delStr)
			if err != nil || deletions < 0 {
				continue
			}
		}

		status := "modified"
		if !isBinary {
			if additions > 0 && deletions == 0 {
				status = "added"
			} else if additions == 0 && deletions > 0 {
				status = "deleted"
			}
		}

		files = append(files, model.ChangedFile{
			Path:      path,
			Status:    status,
			Additions: additions,
			Deletions: deletions,
		})
	}

	// Preserve deterministic path ordering
	sortFilesByPath(files)
	return files
}

func sortFilesByPath(files []model.ChangedFile) {
	for i := 0; i < len(files)-1; i++ {
		for j := i + 1; j < len(files); j++ {
			if files[i].Path > files[j].Path {
				files[i], files[j] = files[j], files[i]
			}
		}
	}
}
