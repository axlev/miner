package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestExtractHunkSymbols(t *testing.T) {
	diffText := `
diff --git a/bgpd/bgp_fsm.c b/bgpd/bgp_fsm.c
index 1234567..89abcdef 100644
--- a/bgpd/bgp_fsm.c
+++ b/bgpd/bgp_fsm.c
@@ -1020,7 +1020,10 @@ int bgp_fsm_change_status(struct peer *peer, int status)
 {
 	int old_status = peer->status;
 
+	if (status == Established) {
+		bgp_timer_set(peer);
+	}
 	return 0;
 }
@@ -1500,6 +1503,14 @@ static void bgp_stop(struct peer *peer)
 {
+	peer_reset_state(peer);
+}
+
+void bgp_peer_graceful_restart(struct peer *peer)
+{
+	peer->gr_timer = 1;
+}
`

	symbols := ExtractHunkSymbols(diffText)

	expected := map[string]bool{
		"bgp_fsm_change_status":     true,
		"bgp_stop":                  true,
		"bgp_peer_graceful_restart": true,
	}

	for _, s := range symbols {
		if !expected[s] {
			t.Errorf("unexpected symbol extracted: %s", s)
		}
		delete(expected, s)
	}

	if len(expected) > 0 {
		t.Errorf("missing expected symbols: %+v", expected)
	}
}

func TestExtractHunkSymbols_LanguageAware(t *testing.T) {
	diffText := `
diff --git a/bgpd/bgpd.c b/bgpd/bgpd.c
--- a/bgpd/bgpd.c
+++ b/bgpd/bgpd.c
@@ -100,6 +100,10 @@ struct bgp *bgp_lookup_by_name(const char *name)
+	return NULL;
 }
@@ -200,6 +204,10 @@ DEFUN (bgp_router_id, bgp_router_id_cmd)
+	return CMD_SUCCESS;
+}
+int main(int argc, char **argv)
+{
+	return 0;
+}
diff --git a/lib/bfd.c b/lib/bfd.c
--- a/lib/bfd.c
+++ b/lib/bfd.c
@@ -50,6 +50,10 @@ bool bfd_session_is_down(struct bfd_session *bs)
+	return bs->status == BFD_STATUS_DOWN;
 }
diff --git a/tests/topotests/bgp_test.py b/tests/topotests/bgp_test.py
--- a/tests/topotests/bgp_test.py
+++ b/tests/topotests/bgp_test.py
@@ -10,6 +10,12 @@ def test_down_event():
+    pass
+def build_topo():
+    pass
`

	parsed := ExtractFileHunkSymbols(diffText)

	// Valid C symbols from .c files
	expectedSymbols := map[string]bool{
		"bgp_lookup_by_name":  true,
		"bfd_session_is_down": true,
	}

	if len(parsed.Symbols) != len(expectedSymbols) {
		t.Fatalf("expected %d symbols, got %d: %v", len(expectedSymbols), len(parsed.Symbols), parsed.Symbols)
	}

	for _, s := range parsed.Symbols {
		if !expectedSymbols[s] {
			t.Errorf("unexpected symbol in parsed list: %s", s)
		}
	}

	// Verify path-associated locations
	if len(parsed.FunctionLocations) != 2 {
		t.Fatalf("expected 2 function locations, got %d: %+v", len(parsed.FunctionLocations), parsed.FunctionLocations)
	}

	locMap := make(map[string]string)
	for _, l := range parsed.FunctionLocations {
		locMap[l.Function] = l.Path
	}

	if locMap["bgp_lookup_by_name"] != "bgpd/bgpd.c" {
		t.Errorf("expected bgpd/bgpd.c for bgp_lookup_by_name, got %s", locMap["bgp_lookup_by_name"])
	}
	if locMap["bfd_session_is_down"] != "lib/bfd.c" {
		t.Errorf("expected lib/bfd.c for bfd_session_is_down, got %s", locMap["bfd_session_is_down"])
	}
}

func TestParseNumstat(t *testing.T) {
	numstatOutput := `
15	4	bgpd/bgp_fsm.c
0	20	zebra/zebra_rib.c
45	0	lib/event.c
`
	files := ParseNumstat(numstatOutput)
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}

	if files[0].Path != "bgpd/bgp_fsm.c" || files[0].Additions != 15 || files[0].Deletions != 4 || files[0].Status != "modified" {
		t.Errorf("unexpected file 0: %+v", files[0])
	}

	if files[1].Path != "lib/event.c" || files[1].Status != "added" || files[1].Additions != 45 {
		t.Errorf("unexpected file 1: %+v", files[1])
	}

	if files[2].Path != "zebra/zebra_rib.c" || files[2].Status != "deleted" || files[2].Deletions != 20 {
		t.Errorf("unexpected file 2: %+v", files[2])
	}
}

func TestDiffPR_MergeBaseFixture(t *testing.T) {
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
			t.Fatalf("git command failed: git %v: %s (%v)", args, string(out), err)
		}
		return string(out)
	}

	runGit("init")
	runGit("config", "user.name", "Test")
	runGit("config", "user.email", "test@example.com")

	// 1. Initial base commit
	os.WriteFile(filepath.Join(tempDir, "base.c"), []byte("int base() { return 0; }\n"), 0644)
	runGit("add", "base.c")
	runGit("commit", "-m", "initial base commit")
	baseCommit := strings.TrimSpace(runGit("rev-parse", "HEAD"))

	// 2. Feature branch with 1 commit modifying feature.c
	runGit("checkout", "-b", "feature")
	os.WriteFile(filepath.Join(tempDir, "feature.c"), []byte("int feature() { return 1; }\n"), 0644)
	runGit("add", "feature.c")
	runGit("commit", "-m", "feature commit")
	featureHead := strings.TrimSpace(runGit("rev-parse", "HEAD"))

	// 3. Upstream master advances with 2 commits modifying upstream1.c and upstream2.c
	runGit("checkout", "master")
	os.WriteFile(filepath.Join(tempDir, "upstream1.c"), []byte("int u1() { return 0; }\n"), 0644)
	runGit("add", "upstream1.c")
	runGit("commit", "-m", "upstream 1")

	os.WriteFile(filepath.Join(tempDir, "upstream2.c"), []byte("int u2() { return 0; }\n"), 0644)
	runGit("add", "upstream2.c")
	runGit("commit", "-m", "upstream 2")
	masterHead := strings.TrimSpace(runGit("rev-parse", "HEAD"))

	repo := OpenRepository(filepath.Join(tempDir, ".git"))
	ctx := context.Background()

	// Two-endpoint DiffBetween: should include feature.c, upstream1.c, upstream2.c (3 files)
	diffBetweenRes, err := repo.DiffBetween(ctx, masterHead, featureHead)
	if err != nil {
		t.Fatalf("DiffBetween failed: %v", err)
	}
	if len(diffBetweenRes.Files) != 3 {
		t.Errorf("DiffBetween: expected 3 files due to divergence, got %d", len(diffBetweenRes.Files))
	}

	// Merge-base DiffPR: should contain ONLY the 1 feature.c file from the PR branch!
	diffPRRes, err := repo.DiffPR(ctx, masterHead, featureHead)
	if err != nil {
		t.Fatalf("DiffPR failed: %v", err)
	}
	if len(diffPRRes.Files) != 1 {
		t.Fatalf("DiffPR: expected exactly 1 file, got %d", len(diffPRRes.Files))
	}
	if diffPRRes.Files[0].Path != "feature.c" {
		t.Errorf("DiffPR: expected feature.c, got %s", diffPRRes.Files[0].Path)
	}
	_ = baseCommit
}

func TestDiffPR_PR16194_ExactPaths(t *testing.T) {
	// Look for FRR git repository clone in local data directories
	repoDirs := []string{
		"../../data/repos/FRRouting/frr.git",
		"../../data/cache/repos/FRRouting/frr.git",
		"./data/repos/FRRouting/frr.git",
		"./data/cache/repos/FRRouting/frr.git",
	}

	var repoDir string
	for _, dir := range repoDirs {
		if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
			repoDir = dir
			break
		}
	}

	if repoDir == "" {
		t.Skip("FRR local git clone not found; skipping PR #16194 integration test")
	}

	baseSHA := "82dcb1d63e7366ee2b3bd1d6896f64369cbe4d4a"
	headSHA := "1fb48f5d13faf4ec1e6d4c2cdded9ca2dcd6d609"

	repo := OpenRepository(repoDir)
	ctx := context.Background()

	res, err := repo.DiffPR(ctx, baseSHA, headSHA)
	if err != nil {
		t.Fatalf("DiffPR failed on PR #16194: %v", err)
	}

	expectedPaths := []string{
		"bgpd/bgp_packet.c",
		"bgpd/bgpd.c",
		"lib/bfd.c",
		"lib/bfd.h",
		"tests/topotests/bgp_bfd_down_cease_notification/r1/bfdd.conf",
		"tests/topotests/bgp_bfd_down_cease_notification/r1/bgpd.conf",
		"tests/topotests/bgp_bfd_down_cease_notification/test_bgp_bfd_down_cease_notification.py",
		"tests/topotests/bgp_bfd_down_cease_notification/test_bgp_bfd_down_cease_notification_shutdown.py",
	}
	sort.Strings(expectedPaths)

	var actualPaths []string
	for _, f := range res.Files {
		actualPaths = append(actualPaths, f.Path)
	}
	sort.Strings(actualPaths)

	if len(actualPaths) != len(expectedPaths) {
		t.Fatalf("PR #16194: expected %d paths, got %d:\nActual: %v\nExpected: %v",
			len(expectedPaths), len(actualPaths), actualPaths, expectedPaths)
	}

	for i := range expectedPaths {
		if actualPaths[i] != expectedPaths[i] {
			t.Errorf("PR #16194 path mismatch at index %d: got %q, expected %q",
				i, actualPaths[i], expectedPaths[i])
		}
	}
}

func TestParseNumstat_StrictFiltering(t *testing.T) {
	// Raw text that combines commit headers, commit messages, diff hunks, and numstat lines
	rawText := `commit 3d7548f9e5bb436cdcf5fef22c7fafe31d073db6
Author: Alex <alex@example.com>
Date:   Thu Mar 7 12:00:00 2024 +0000

    bgpd: fix AdminDown notification
    
    Fixes: 1fb48f5d13fa
    Reported by user.

diff --git a/bgpd/bgpd.c b/bgpd/bgpd.c
index 1111111..2222222 100644
--- a/bgpd/bgpd.c
+++ b/bgpd/bgpd.c
@@ -100,6 +100,8 @@ int bgp_notify_send()
+    return helper_call(arg);
+    value = compute_val(123);
+    helper_call(arg);

10	2	bgpd/bgpd.c
-	-	binary/blob.bin
invalid line without numbers
not	a	valid	number
`

	files := ParseNumstat(rawText)

	if len(files) != 2 {
		t.Fatalf("expected exactly 2 valid numstat files, got %d: %+v", len(files), files)
	}

	if files[0].Path != "bgpd/bgpd.c" || files[0].Additions != 10 || files[0].Deletions != 2 || files[0].Status != "modified" {
		t.Errorf("unexpected file 0: %+v", files[0])
	}

	if files[1].Path != "binary/blob.bin" || files[1].Additions != 0 || files[1].Deletions != 0 || files[1].Status != "modified" {
		t.Errorf("unexpected file 1: %+v", files[1])
	}
}

func TestExtractHunkSymbols_RejectFunctionCalls(t *testing.T) {
	diffText := `
diff --git a/bgpd/bgp_packet.c b/bgpd/bgp_packet.c
--- a/bgpd/bgp_packet.c
+++ b/bgpd/bgp_packet.c
@@ -500,6 +500,15 @@ int bgp_fsm_change_status(struct peer *peer, int status)
 {
+	return helper_call(arg);
+	value = helper_call(arg);
+	helper_call(arg);
+	if (helper_call(arg)) {
+		do_something();
+	}
+	int foo_prototype(int a);
+	return 0;
+}
+
+void bgp_peer_graceful_restart(struct peer *peer)
+{
+	peer->gr_timer = 1;
+}
`

	parsed := ExtractFileHunkSymbols(diffText)

	expected := map[string]bool{
		"bgp_fsm_change_status":     true,
		"bgp_peer_graceful_restart": true,
	}

	if len(parsed.Symbols) != len(expected) {
		t.Fatalf("expected exactly %d symbols, got %d: %v", len(expected), len(parsed.Symbols), parsed.Symbols)
	}

	for _, s := range parsed.Symbols {
		if !expected[s] {
			t.Errorf("unexpected symbol extracted (should have been rejected): %s", s)
		}
	}
}

func TestCommitDiff_3d7548f_Integration(t *testing.T) {
	repoDirs := []string{
		"../../data/repos/FRRouting/frr.git",
		"../../data/cache/repos/FRRouting/frr.git",
		"./data/repos/FRRouting/frr.git",
		"./data/cache/repos/FRRouting/frr.git",
	}

	var repoDir string
	for _, dir := range repoDirs {
		if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
			repoDir = dir
			break
		}
	}

	if repoDir == "" {
		t.Skip("FRR local git clone not found; skipping CommitDiff integration test")
	}

	sha := "3d7548f9e5bb436cdcf5fef22c7fafe31d073db6"
	repo := OpenRepository(repoDir)
	ctx := context.Background()

	summary, err := repo.CommitDiff(ctx, sha)
	if err != nil {
		t.Fatalf("CommitDiff failed for %s: %v", sha, err)
	}

	if len(summary.ChangedFiles) != 1 || summary.ChangedFiles[0] != "bgpd/bgpd.c" {
		t.Fatalf("expected changed files to be exactly [bgpd/bgpd.c], got: %v", summary.ChangedFiles)
	}
}

func TestGroupContiguousRanges(t *testing.T) {
	input := []int{1, 2, 3, 10, 11, 20, 20, 0, -5}
	ranges := GroupContiguousRanges(input)

	expected := []BlameRange{
		{StartLine: 1, EndLine: 3},
		{StartLine: 10, EndLine: 11},
		{StartLine: 20, EndLine: 20},
	}

	if len(ranges) != len(expected) {
		t.Fatalf("expected %d ranges, got %d: %+v", len(expected), len(ranges), ranges)
	}

	for i := range expected {
		if ranges[i] != expected[i] {
			t.Errorf("range %d mismatch: got %+v, expected %+v", i, ranges[i], expected[i])
		}
	}
}

func TestNormalizeSubstantiveLine(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		ok       bool
	}{
		{"  int a = 10;  ", "int a = 10;", true},
		{"{", "", false},
		{"}", "", false},
		{"};", "", false},
		{"// comment", "", false},
		{"/* start comment", "", false},
		{"* mid comment", "", false},
		{"*/", "", false},
		{"a;", "", false}, // len < 3
		{"bgp_fsm_change_status(peer, status);", "bgp_fsm_change_status(peer, status);", true},
	}

	for _, tt := range tests {
		got, ok := NormalizeSubstantiveLine(tt.input)
		if ok != tt.ok || got != tt.expected {
			t.Errorf("NormalizeSubstantiveLine(%q) = (%q, %v), expected (%q, %v)", tt.input, got, ok, tt.expected, tt.ok)
		}
	}
}

func TestExtractSubstantivePatchLines(t *testing.T) {
	diffText := `
diff --git a/bgpd/bgpd.c b/bgpd/bgpd.c
--- a/bgpd/bgpd.c
+++ b/bgpd/bgpd.c
@@ -10,6 +10,7 @@
-int old_func()
-{
-	return 0;
-}
+int new_func()
+{
+	return 1;
+}
`
	added, removed := ExtractSubstantivePatchLines(diffText)

	if len(added["bgpd/bgpd.c"]) != 2 {
		t.Errorf("expected 2 added substantive lines, got: %v", added["bgpd/bgpd.c"])
	}
	if len(removed["bgpd/bgpd.c"]) != 2 {
		t.Errorf("expected 2 removed substantive lines, got: %v", removed["bgpd/bgpd.c"])
	}
}

func TestComputeReversalEvidence_Unit(t *testing.T) {
	origDiff := `
diff --git a/bgpd/bgpd.c b/bgpd/bgpd.c
--- a/bgpd/bgpd.c
+++ b/bgpd/bgpd.c
@@ -10,4 +10,6 @@
+	peer->gr_timer = 100;
+	peer_reset_state(peer);
`
	laterReverseDiff := `
diff --git a/bgpd/bgpd.c b/bgpd/bgpd.c
--- a/bgpd/bgpd.c
+++ b/bgpd/bgpd.c
@@ -10,6 +10,4 @@
-	peer->gr_timer = 100;
-	peer_reset_state(peer);
`
	blamed := []RemovedLineLocation{
		{Path: "bgpd/bgpd.c", LineNumber: 10, Content: "peer->gr_timer = 100;"},
	}

	ev := ComputeReversalEvidence(origDiff, laterReverseDiff, false, blamed)

	if ev.ReversalType != "partial_candidate" {
		t.Errorf("expected partial_candidate, got %s", ev.ReversalType)
	}
	if ev.LaterRemovedLinesMatchingOriginalAdditions != 2 {
		t.Errorf("expected 2 matching removed lines, got %d", ev.LaterRemovedLinesMatchingOriginalAdditions)
	}
	if ev.LineageSupportedRemovedOverlapCount != 1 {
		t.Errorf("expected 1 lineage supported overlap, got %d", ev.LineageSupportedRemovedOverlapCount)
	}
	if len(ev.PathOverlaps) != 1 || ev.PathOverlaps[0].Path != "bgpd/bgpd.c" {
		t.Errorf("unexpected path overlaps: %+v", ev.PathOverlaps)
	}
}

func TestReverseCommitDiff_PatchIDErrors(t *testing.T) {
	repo := OpenRepository("non_existent_dir")
	_, _, err := repo.ReverseCommitDiff(context.Background(), "", "")
	if err == nil {
		t.Errorf("expected error on empty SHAs, got nil")
	}
}
