package collector

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v62/github"
	"miner/internal/model"
	"miner/internal/storage"
)

func setupTestGitRepo(t *testing.T) (string, func(args ...string) string) {
	tempDir := t.TempDir()

	runGit := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = tempDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %s (%v)", args, string(out), err)
		}
		return strings.TrimSpace(string(out))
	}

	runGit("init")
	runGit("config", "user.name", "Test")
	runGit("config", "user.email", "test@example.com")

	return tempDir, runGit
}

func TestRebuildRawOffline_ExactMembershipAndOrdering(t *testing.T) {
	tempDir, runGit := setupTestGitRepo(t)

	// Base commit
	os.WriteFile(filepath.Join(tempDir, "main.c"), []byte("int main() { return 0; }\n"), 0644)
	runGit("add", ".")
	runGit("commit", "-m", "base")
	baseSHA := runGit("rev-parse", "HEAD")

	// Branch 1 (PR 100)
	runGit("checkout", "-b", "b100")
	os.WriteFile(filepath.Join(tempDir, "f100.c"), []byte("int f100() { return 100; }\n"), 0644)
	runGit("add", ".")
	runGit("commit", "-m", "pr 100")
	head100 := runGit("rev-parse", "HEAD")

	// Branch 2 (PR 200)
	runGit("checkout", "master")
	runGit("checkout", "-b", "b200")
	os.WriteFile(filepath.Join(tempDir, "f200.c"), []byte("int f200() { return 200; }\n"), 0644)
	runGit("add", ".")
	runGit("commit", "-m", "pr 200")
	head200 := runGit("rev-parse", "HEAD")

	// Branch 3 (PR 300)
	runGit("checkout", "master")
	runGit("checkout", "-b", "b300")
	os.WriteFile(filepath.Join(tempDir, "f300.c"), []byte("int f300() { return 300; }\n"), 0644)
	runGit("add", ".")
	runGit("commit", "-m", "pr 300")
	head300 := runGit("rev-parse", "HEAD")

	cacheDir := t.TempDir()
	diskCache := storage.NewDiskCache(cacheDir)

	makeBundle := func(num int, head string) {
		title := "PR Title"
		body := "PR Body fixes #10"
		login := "author"
		now := time.Now().UTC()
		bundle := RawPRBundle{
			PR: &github.PullRequest{
				Number:    github.Int(num),
				Title:     github.String(title),
				Body:      github.String(body),
				User:      &github.User{Login: github.String(login)},
				CreatedAt: &github.Timestamp{Time: now},
				MergedAt:  &github.Timestamp{Time: now},
				Base: &github.PullRequestBranch{
					Ref: github.String("master"),
					SHA: github.String(baseSHA),
				},
				Head: &github.PullRequestBranch{
					Ref: github.String("feature"),
					SHA: github.String(head),
				},
			},
			FetchedAt: now,
		}
		raw, err := json.Marshal(bundle)
		if err != nil {
			t.Fatalf("failed to marshal test bundle: %v", err)
		}
		if err := diskCache.WritePR("FRRouting/frr", num, raw); err != nil {
			t.Fatalf("failed to write test cache: %v", err)
		}
	}

	makeBundle(100, head100)
	makeBundle(200, head200)
	makeBundle(300, head300)

	// Create seed file with unordered and duplicate entries: 300, 100, 200, 100
	seedFile := filepath.Join(t.TempDir(), "seed.jsonl")
	seedRecords := []model.PRCandidateRecord{
		{Original: model.OriginalPR{Number: 300, Repository: "FRRouting/frr"}},
		{Original: model.OriginalPR{Number: 100, Repository: "FRRouting/frr"}},
		{Original: model.OriginalPR{Number: 200, Repository: "FRRouting/frr"}},
		{Original: model.OriginalPR{Number: 100, Repository: "FRRouting/frr"}},
	}
	if err := storage.WriteJSONL(seedFile, seedRecords); err != nil {
		t.Fatalf("failed to write test seed: %v", err)
	}

	opts := RebuildRawOptions{
		SeedFile:       seedFile,
		CacheDir:       cacheDir,
		GitDir:         tempDir,
		Repo:           "FRRouting/frr",
		ObservationEnd: time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC),
	}

	records, summary, err := RebuildRawOffline(context.Background(), opts, nil)
	if err != nil {
		t.Fatalf("RebuildRawOffline failed unexpectedly: %v", err)
	}

	if summary.SeedCount != 3 {
		t.Errorf("expected seed count 3 (deduplicated), got %d", summary.SeedCount)
	}
	if summary.RebuiltCount != 3 {
		t.Errorf("expected rebuilt count 3, got %d", summary.RebuiltCount)
	}
	if summary.MissingCacheCount != 0 {
		t.Errorf("expected missing cache count 0, got %d", summary.MissingCacheCount)
	}
	if summary.DiffErrorCount != 0 {
		t.Errorf("expected diff error count 0, got %d", summary.DiffErrorCount)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}

	// Verify deterministic sorting ascending
	if records[0].Original.Number != 100 || records[1].Original.Number != 200 || records[2].Original.Number != 300 {
		t.Errorf("records not sorted deterministically: %d, %d, %d",
			records[0].Original.Number, records[1].Original.Number, records[2].Original.Number)
	}
}

func TestRebuildRawOffline_MissingCacheFailure(t *testing.T) {
	tempDir, runGit := setupTestGitRepo(t)

	os.WriteFile(filepath.Join(tempDir, "main.c"), []byte("int main() { return 0; }\n"), 0644)
	runGit("add", ".")
	runGit("commit", "-m", "base")
	baseSHA := runGit("rev-parse", "HEAD")

	cacheDir := t.TempDir()
	diskCache := storage.NewDiskCache(cacheDir)

	// Write cache ONLY for PR 100
	bundle := RawPRBundle{
		PR: &github.PullRequest{
			Number:    github.Int(100),
			Title:     github.String("Title"),
			Body:      github.String("Body"),
			CreatedAt: &github.Timestamp{Time: time.Now()},
			MergedAt:  &github.Timestamp{Time: time.Now()},
			Base:      &github.PullRequestBranch{SHA: github.String(baseSHA)},
			Head:      &github.PullRequestBranch{SHA: github.String(baseSHA)},
		},
	}
	raw, _ := json.Marshal(bundle)
	diskCache.WritePR("FRRouting/frr", 100, raw)

	// Seed has PR 100 and missing PR 101
	seedFile := filepath.Join(t.TempDir(), "seed.jsonl")
	seedRecords := []model.PRCandidateRecord{
		{Original: model.OriginalPR{Number: 100, Repository: "FRRouting/frr"}},
		{Original: model.OriginalPR{Number: 101, Repository: "FRRouting/frr"}},
	}
	storage.WriteJSONL(seedFile, seedRecords)

	opts := RebuildRawOptions{
		SeedFile: seedFile,
		CacheDir: cacheDir,
		GitDir:   tempDir,
		Repo:     "FRRouting/frr",
	}

	_, summary, err := RebuildRawOffline(context.Background(), opts, nil)
	if err == nil {
		t.Fatal("expected error due to missing cache for PR 101, got nil")
	}
	if summary.MissingCacheCount != 1 {
		t.Errorf("expected MissingCacheCount 1, got %d", summary.MissingCacheCount)
	}
	if !strings.Contains(err.Error(), "missing or unreadable cache") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRebuildRawOffline_PathAssociatedFunctions(t *testing.T) {
	tempDir, runGit := setupTestGitRepo(t)

	// Base commit
	os.MkdirAll(filepath.Join(tempDir, "bgpd"), 0755)
	os.WriteFile(filepath.Join(tempDir, "bgpd", "bgpd.c"), []byte("/* bgpd.c */\nint helper_func()\n{\n\treturn 0;\n}\n\nint peer_active(struct peer *peer)\n{\n\tint line1 = 1;\n\tint line2 = 2;\n\tint line3 = 3;\n\tint line4 = 4;\n\tint line5 = 5;\n\tint line6 = 6;\n\tint status = 0;\n\treturn status;\n}\n"), 0644)
	runGit("add", ".")
	runGit("commit", "-m", "base")
	baseSHA := runGit("rev-parse", "HEAD")

	// Feature commit
	runGit("checkout", "-b", "feat")
	os.WriteFile(filepath.Join(tempDir, "bgpd", "bgpd.c"), []byte("/* bgpd.c */\nint helper_func()\n{\n\treturn 0;\n}\n\nint peer_active(struct peer *peer)\n{\n\tint line1 = 1;\n\tint line2 = 2;\n\tint line3 = 3;\n\tint line4 = 4;\n\tint line5 = 5;\n\tint line6 = 6;\n\tint status = 1;\n\treturn status;\n}\n"), 0644)
	runGit("add", ".")
	runGit("commit", "-m", "modify peer_active")
	headSHA := runGit("rev-parse", "HEAD")

	cacheDir := t.TempDir()
	diskCache := storage.NewDiskCache(cacheDir)

	now := time.Now().UTC()
	bundle := RawPRBundle{
		PR: &github.PullRequest{
			Number:    github.Int(500),
			Title:     github.String("bgpd fix"),
			Body:      github.String("body"),
			CreatedAt: &github.Timestamp{Time: now},
			MergedAt:  &github.Timestamp{Time: now},
			Base:      &github.PullRequestBranch{Ref: github.String("master"), SHA: github.String(baseSHA)},
			Head:      &github.PullRequestBranch{Ref: github.String("feat"), SHA: github.String(headSHA)},
		},
		FetchedAt: now,
	}
	raw, _ := json.Marshal(bundle)
	diskCache.WritePR("FRRouting/frr", 500, raw)

	seedFile := filepath.Join(t.TempDir(), "seed.jsonl")
	seedRecords := []model.PRCandidateRecord{
		{Original: model.OriginalPR{Number: 500, Repository: "FRRouting/frr"}},
	}
	storage.WriteJSONL(seedFile, seedRecords)

	opts := RebuildRawOptions{
		SeedFile: seedFile,
		CacheDir: cacheDir,
		GitDir:   tempDir,
		Repo:     "FRRouting/frr",
	}

	records, summary, err := RebuildRawOffline(context.Background(), opts, nil)
	if err != nil {
		t.Fatalf("RebuildRawOffline failed: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	orig := records[0].Original
	if len(orig.ChangedFunctionLocations) == 0 {
		t.Fatalf("expected non-empty ChangedFunctionLocations, got empty")
	}

	foundPeerActive := false
	for _, loc := range orig.ChangedFunctionLocations {
		if loc.Path == "bgpd/bgpd.c" && loc.Function == "peer_active" {
			foundPeerActive = true
		}
	}
	if !foundPeerActive {
		t.Errorf("expected ChangedFunctionLocations to contain (bgpd/bgpd.c, peer_active), got %+v", orig.ChangedFunctionLocations)
	}
	if summary.RecordsWithPathLocations != 1 {
		t.Errorf("expected RecordsWithPathLocations 1, got %d", summary.RecordsWithPathLocations)
	}
}

func TestRebuildRawOffline_PR16194_Validation(t *testing.T) {
	cacheDir := "../../data/cache"
	gitDir := "../../data/repos/FRRouting/frr.git"

	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		t.Skip("skipping integration test: data/repos/FRRouting/frr.git not present")
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "github", "FRRouting_frr", "prs", "pr_16194.json")); os.IsNotExist(err) {
		t.Skip("skipping integration test: cached PR 16194 not present")
	}

	seedFile := filepath.Join(t.TempDir(), "seed_16194.jsonl")
	seedRecords := []model.PRCandidateRecord{
		{Original: model.OriginalPR{Number: 16194, Repository: "FRRouting/frr"}},
	}
	if err := storage.WriteJSONL(seedFile, seedRecords); err != nil {
		t.Fatalf("failed to write seed file: %v", err)
	}

	opts := RebuildRawOptions{
		SeedFile:       seedFile,
		CacheDir:       cacheDir,
		GitDir:         gitDir,
		Repo:           "FRRouting/frr",
		ObservationEnd: time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC),
	}

	records, summary, err := RebuildRawOffline(context.Background(), opts, nil)
	if err != nil {
		t.Fatalf("RebuildRawOffline failed for PR #16194: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if summary.RebuiltCount != 1 {
		t.Errorf("expected rebuilt count 1, got %d", summary.RebuiltCount)
	}

	orig := records[0].Original
	if orig.Number != 16194 {
		t.Fatalf("expected PR #16194, got #%d", orig.Number)
	}

	// 1. Exactly eight changed paths
	if len(orig.ChangedFiles) != 8 {
		t.Fatalf("expected exactly 8 changed paths for PR #16194, got %d: %+v", len(orig.ChangedFiles), orig.ChangedFiles)
	}

	// 2. bgpd/bgpd.c is associated with peer_active
	foundPeerActive := false
	for _, loc := range orig.ChangedFunctionLocations {
		if loc.Path == "bgpd/bgpd.c" && loc.Function == "peer_active" {
			foundPeerActive = true
		}
	}
	if !foundPeerActive {
		t.Errorf("expected bgpd/bgpd.c to be associated with peer_active, got %+v", orig.ChangedFunctionLocations)
	}

	// 3. No branch-pollution paths appear
	for _, f := range orig.ChangedFiles {
		if strings.HasPrefix(f.Path, "isisd/") || strings.HasPrefix(f.Path, "ospfd/") || strings.HasPrefix(f.Path, "zebra/") {
			t.Errorf("unexpected branch-pollution path found in PR #16194: %s", f.Path)
		}
	}
}
