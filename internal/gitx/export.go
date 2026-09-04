package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ExportCommitMetadata is stable, machine-readable metadata for an existing commit.
type ExportCommitMetadata struct {
	Commit         string   `json:"commit"`
	Parents        []string `json:"parents"`
	AuthorName     string   `json:"author_name"`
	AuthorEmail    string   `json:"author_email"`
	AuthorDate     string   `json:"author_date"`
	CommitterName  string   `json:"committer_name"`
	CommitterEmail string   `json:"committer_email"`
	CommitDate     string   `json:"commit_date"`
	Subject        string   `json:"subject"`
	Body           string   `json:"body"`
	Message        string   `json:"message"`
	ChangedPaths   []string `json:"changed_paths"`
}

func (r *Repository) gitOutput(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"--git-dir", r.RepoDir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s failed: %s (%w)", strings.Join(args, " "), strings.TrimSpace(stderr.String()), err)
	}
	return out, nil
}

// ExportCommit resolves complete commit metadata without interpreting message trailers.
func (r *Repository) ExportCommit(ctx context.Context, ref string) (*ExportCommitMetadata, error) {
	format := "%H%x00%P%x00%an%x00%ae%x00%aI%x00%cn%x00%ce%x00%cI%x00%s%x00%b%x00%B"
	out, err := r.gitOutput(ctx, "show", "-s", "--no-show-signature", "--format="+format, ref+"^{commit}")
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(string(out), "\x00", 11)
	if len(parts) != 11 {
		return nil, fmt.Errorf("unexpected metadata format for %q", ref)
	}
	sha := parts[0]
	pathsOut, err := r.gitOutput(ctx, "diff-tree", "--root", "--no-commit-id", "--name-only", "-r", "-m", "--first-parent", sha)
	if err != nil {
		return nil, err
	}
	var paths []string
	seen := map[string]bool{}
	for _, p := range strings.Split(strings.TrimSpace(string(pathsOut)), "\n") {
		if p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	parents := strings.Fields(parts[1])
	return &ExportCommitMetadata{Commit: sha, Parents: parents, AuthorName: parts[2], AuthorEmail: parts[3], AuthorDate: parts[4], CommitterName: parts[5], CommitterEmail: parts[6], CommitDate: parts[7], Subject: parts[8], Body: parts[9], Message: strings.TrimSuffix(parts[10], "\n"), ChangedPaths: paths}, nil
}

// ExportPatch returns an unambiguous patch and a name describing its construction.
func (r *Repository) ExportPatch(ctx context.Context, commit string, parents []string) ([]byte, string, error) {
	if len(parents) > 1 {
		out, err := r.gitOutput(ctx, "diff", "--no-ext-diff", "--binary", "--full-index", parents[0], commit, "--")
		return out, "git-diff-first-parent", err
	}
	out, err := r.gitOutput(ctx, "show", "--format=", "--no-ext-diff", "--binary", "--full-index", "--patch", commit, "--")
	method := "git-show-parent-diff"
	if len(parents) == 0 {
		method = "git-show-root-diff"
	}
	return out, method, err
}
