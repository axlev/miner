package correlator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"miner/internal/gitx"
	"miner/internal/model"
)

// TestCorrelate_CountsUninspectedCommits is the contract behind the H1 negative
// class: a post-merge commit that carries fix vocabulary but whose diff cannot be
// read must be counted, so a record with no signals is not mistaken for a record
// with no evidence. A commit without fix vocabulary is never inspected and so is
// never counted; a commit whose diff reads fine is not counted either.
func TestCorrelate_CountsUninspectedCommits(t *testing.T) {
	tempDir := t.TempDir()
	runGit := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = tempDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %s (%v)", args, string(out), err)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init")
	os.MkdirAll(filepath.Join(tempDir, "bgpd"), 0755)
	os.WriteFile(filepath.Join(tempDir, "bgpd", "a.c"), []byte("int f(void)\n{\n\treturn 1;\n}\n"), 0644)
	runGit("add", ".")
	runGit("commit", "-m", "base")
	os.WriteFile(filepath.Join(tempDir, "bgpd", "a.c"), []byte("int f(void)\n{\n\treturn 2;\n}\n"), 0644)
	runGit("add", ".")
	runGit("commit", "-m", "bgpd: fix return value")
	realFix := runGit("rev-parse", "HEAD")

	repo := gitx.OpenRepository(filepath.Join(tempDir, ".git"))
	corr := NewCorrelator(repo)

	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	orig := &model.OriginalPR{
		Number:                   1,
		MergedAt:                 t0,
		HeadSHA:                  strings.Repeat("a", 40),
		ChangedFiles:             []model.ChangedFile{{Path: "bgpd/a.c"}},
		ChangedFunctionLocations: []model.ChangedFunctionLocation{{Path: "bgpd/a.c", Function: "f"}},
	}
	missing := strings.Repeat("b", 40)
	missing2 := strings.Repeat("c", 40)
	commits := []gitx.CommitLogEntry{
		// Readable diff with fix vocabulary: inspected, not counted.
		{SHA: realFix, Date: t0.Add(24 * time.Hour), Subject: "bgpd: fix return value", FullMessage: "bgpd: fix return value"},
		// Fix vocabulary, but the object does not exist: counted.
		{SHA: missing, Date: t0.Add(48 * time.Hour), Subject: "bgpd: fix a crash", FullMessage: "bgpd: fix a crash"},
		// Fix vocabulary in the body only (pre-filter, not subject): still inspected, counted.
		{SHA: missing2, Date: t0.Add(72 * time.Hour), Subject: "bgpd: tidy", FullMessage: "bgpd: tidy\n\nThis resolves an issue."},
		// No fix vocabulary anywhere: never inspected, never counted.
		{SHA: strings.Repeat("d", 40), Date: t0.Add(96 * time.Hour), Subject: "bgpd: rename", FullMessage: "bgpd: rename"},
		// Before T0: never considered.
		{SHA: strings.Repeat("e", 40), Date: t0.Add(-time.Hour), Subject: "bgpd: fix old", FullMessage: "bgpd: fix old"},
	}
	ev := corr.CorrelatePR(context.Background(), orig, commits, t0.Add(30*24*time.Hour))

	if ev.UninspectedCommitCount != 2 {
		t.Fatalf("uninspected count = %d, want 2 (got shas %v)", ev.UninspectedCommitCount, ev.UninspectedCommitSHAs)
	}
	if len(ev.UninspectedCommitSHAs) != 2 || ev.UninspectedCommitSHAs[0] != missing || ev.UninspectedCommitSHAs[1] != missing2 {
		t.Errorf("uninspected shas = %v", ev.UninspectedCommitSHAs)
	}
	// The readable fix is inspected normally: it is not in the uninspected list and
	// its file overlap inside 90 days yields the weak signal.
	for _, sha := range ev.UninspectedCommitSHAs {
		if sha == realFix {
			t.Errorf("readable commit %s was counted as uninspected", realFix)
		}
	}
	weakFromReal := 0
	for _, w := range ev.WeakSignals {
		if w.SourceRef == realFix && w.SignalType == "SAME_FILE_MODIFICATION" {
			weakFromReal++
		}
	}
	if weakFromReal != 1 {
		t.Errorf("readable fix should yield one SAME_FILE_MODIFICATION weak signal, got %d (weak=%+v)", weakFromReal, ev.WeakSignals)
	}
}

func TestCorrelate_UninspectedSHAListIsBounded(t *testing.T) {
	tempDir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	corr := NewCorrelator(gitx.OpenRepository(filepath.Join(tempDir, ".git")))
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	orig := &model.OriginalPR{Number: 1, MergedAt: t0, ChangedFiles: []model.ChangedFile{{Path: "x.c"}}}
	var commits []gitx.CommitLogEntry
	for i := 0; i < model.MaxUninspectedCommitSHAs+5; i++ {
		sha := strings.Repeat(string(rune('a'+i%6)), 39) + string(rune('0'+i%10))
		commits = append(commits, gitx.CommitLogEntry{SHA: sha, Date: t0.Add(time.Duration(i+1) * time.Hour), Subject: "fix", FullMessage: "fix"})
	}
	ev := corr.CorrelatePR(context.Background(), orig, commits, t0.Add(48*time.Hour))
	if ev.UninspectedCommitCount != model.MaxUninspectedCommitSHAs+5 {
		t.Errorf("count = %d, want %d", ev.UninspectedCommitCount, model.MaxUninspectedCommitSHAs+5)
	}
	if len(ev.UninspectedCommitSHAs) != model.MaxUninspectedCommitSHAs {
		t.Errorf("sha list = %d entries, want the cap %d", len(ev.UninspectedCommitSHAs), model.MaxUninspectedCommitSHAs)
	}
}
