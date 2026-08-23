package correlator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pr-analysis/internal/gitx"
	"pr-analysis/internal/model"
)

func TestClassifyEvidenceScope(t *testing.T) {
	tests := []struct {
		name     string
		paths    []string
		expected model.EvidenceScope
	}{
		{
			name:     "Case #15255 Test Maintenance Only",
			paths:    []string{"tests/topotests/bgp_vrf_netns/test_bgp_vrf_netns.py"},
			expected: model.ScopeTest,
		},
		{
			name:     "Case #15326 Code Cleanup",
			paths:    []string{"bgpd/bgp_flowspec.c", "bgpd/bgp_flowspec.h"},
			expected: model.ScopeCode,
		},
		{
			name:     "Case #17478 Test Config Revert",
			paths:    []string{"tests/topotests/ospf_multi_instance/test_ospf_multi_instance.py"},
			expected: model.ScopeTest,
		},
		{
			name:     "Case #15368 Core Code Fix",
			paths:    []string{"bgpd/bgp_route.c", "bgpd/bgp_packet.c"},
			expected: model.ScopeCode,
		},
		{
			name:     "Docs Only",
			paths:    []string{"doc/user/bgp.rst", "doc/developer/workflow.md"},
			expected: model.ScopeDocs,
		},
		{
			name:     "Packaging Only",
			paths:    []string{"debian/rules", "redhat/frr.spec"},
			expected: model.ScopePackaging,
		},
		{
			name:     "Mixed Code and Docs",
			paths:    []string{"bgpd/bgpd.c", "doc/user/installation.rst"},
			expected: model.ScopeMixed,
		},
		{
			name:     "Empty / Unknown",
			paths:    []string{},
			expected: model.ScopeUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyEvidenceScope(tt.paths)
			if got != tt.expected {
				t.Errorf("ClassifyEvidenceScope(%v) = %v, expected %v", tt.paths, got, tt.expected)
			}
		})
	}
}

func TestCorrelatePRSignals(t *testing.T) {
	t0 := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	obsEnd := time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)

	orig := &model.OriginalPR{
		Number:           14000,
		Title:            "ospfd: optimize lsa flooding",
		MergedAt:         t0,
		MergeCommitSHA:   "abcdef11223344556677889900aabbccddeeff00",
		ChangedFunctions: []string{"ospf_lsa_flood"},
		ChangedFunctionLocations: []model.ChangedFunctionLocation{
			{Path: "ospfd/ospf_lsa.c", Function: "ospf_lsa_flood"},
		},
		ChangedFiles: []model.ChangedFile{
			{Path: "ospfd/ospf_lsa.c", Status: "modified"},
		},
	}

	postCommits := []gitx.CommitLogEntry{
		{
			SHA:         "1111222233334444555566667777888899990000",
			Date:        t0.Add(5 * 24 * time.Hour),
			Subject:     "ospfd: fix memory leak in flooding",
			FullMessage: "ospfd: fix memory leak in flooding\n\nFixes: abcdef11223344 (\"ospfd: optimize lsa flooding\")\nReported by CI.",
		},
		{
			SHA:         "2222333344445555666677778888999900001111",
			Date:        t0.Add(15 * 24 * time.Hour),
			Subject:     "ospfd: fix regression in ospf_lsa_flood",
			FullMessage: "ospfd: fix regression in ospf_lsa_flood\n\nRegression introduced in #14000 when handling empty neighbors.",
		},
		{
			SHA:         "3333444455556666777788889999000011112222",
			Date:        t0.Add(-2 * 24 * time.Hour), // Before T0 (must be ignored)
			Subject:     "Fixes: abcdef11223344",
			FullMessage: "Fixes: abcdef11223344",
		},
	}

	correlator := NewCorrelator(nil)
	evidence := correlator.CorrelatePR(context.Background(), orig, postCommits, obsEnd)

	if len(evidence.StrongSignals) < 2 {
		t.Fatalf("expected at least 2 strong signals, got %d", len(evidence.StrongSignals))
	}

	hasFixesSHA := false
	hasRegression := false
	for _, s := range evidence.StrongSignals {
		if s.SignalType == "FIXES_SHA" {
			hasFixesSHA = true
		}
		if s.SignalType == "REGRESSION_MENTION" {
			hasRegression = true
		}
	}

	if !hasFixesSHA {
		t.Errorf("missing expected FIXES_SHA strong signal")
	}
	if !hasRegression {
		t.Errorf("missing expected REGRESSION_MENTION strong signal")
	}

	if evidence.SummaryRankScore != 1.0 {
		t.Errorf("expected summary rank score 1.0, got %f", evidence.SummaryRankScore)
	}
}

func TestCorrelate_PatchID_Deduplication_GitFixture(t *testing.T) {
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

	// Base commit (PR merge commit T0)
	os.WriteFile(filepath.Join(tempDir, "bgpd.c"), []byte("int bgp_init() { return 0; }\n"), 0644)
	runGit("add", "bgpd.c")
	runGit("commit", "-m", "initial base commit")
	baseSHA := runGit("rev-parse", "HEAD")

	// Commit A (Patch A on master): Fix in bgpd.c
	os.WriteFile(filepath.Join(tempDir, "bgpd.c"), []byte("int bgp_init() { return 1; }\n"), 0644)
	runGit("add", "bgpd.c")
	runGit("commit", "-m", "bgpd: fix memory leak in flooding\n\nFixes: "+baseSHA[:12])
	shaA := runGit("rev-parse", "HEAD")

	// Commit B (Patch B on stable branch branched from base): exact same diff on bgpd.c
	runGit("checkout", "-b", "stable", baseSHA)
	os.WriteFile(filepath.Join(tempDir, "bgpd.c"), []byte("int bgp_init() { return 1; }\n"), 0644)
	runGit("add", "bgpd.c")
	runGit("commit", "-m", "bgpd: fix memory leak in flooding (backport #100)\n\nFixes: "+baseSHA[:12])
	shaB := runGit("rev-parse", "HEAD")

	// Commit C (Patch C on master): Different diff on other.c with the SAME subject as Patch A
	runGit("checkout", "master")
	os.WriteFile(filepath.Join(tempDir, "other.c"), []byte("int other() { return 42; }\n"), 0644)
	runGit("add", "other.c")
	runGit("commit", "-m", "bgpd: fix memory leak in flooding\n\nFixes: "+baseSHA[:12])
	shaC := runGit("rev-parse", "HEAD")

	repo := gitx.OpenRepository(filepath.Join(tempDir, ".git"))
	correlator := NewCorrelator(repo)

	t0 := time.Now().Add(-24 * time.Hour)
	obsEnd := time.Now().Add(24 * time.Hour)

	orig := &model.OriginalPR{
		Number:         16194,
		MergedAt:       t0,
		MergeCommitSHA: baseSHA,
	}

	postCommits := []gitx.CommitLogEntry{
		{
			SHA:         shaA,
			Date:        t0.Add(1 * time.Hour),
			Subject:     "bgpd: fix memory leak in flooding",
			FullMessage: "bgpd: fix memory leak in flooding\n\nFixes: " + baseSHA[:12],
		},
		{
			SHA:         shaB,
			Date:        t0.Add(2 * time.Hour),
			Subject:     "bgpd: fix memory leak in flooding (backport #100)",
			FullMessage: "bgpd: fix memory leak in flooding (backport #100)\n\nFixes: " + baseSHA[:12],
		},
		{
			SHA:         shaC,
			Date:        t0.Add(3 * time.Hour),
			Subject:     "bgpd: fix memory leak in flooding",
			FullMessage: "bgpd: fix memory leak in flooding\n\nFixes: " + baseSHA[:12],
		},
	}

	evidence := correlator.CorrelatePR(context.Background(), orig, postCommits, obsEnd)

	if len(evidence.StrongSignals) != 3 {
		t.Fatalf("expected 3 strong signals, got %d", len(evidence.StrongSignals))
	}

	patchIDA := evidence.StrongSignals[0].LogicalPatchID
	patchIDB := evidence.StrongSignals[1].LogicalPatchID
	patchIDC := evidence.StrongSignals[2].LogicalPatchID

	if patchIDA == "" || strings.HasPrefix(patchIDA, "sha:") {
		t.Errorf("expected genuine stable patch ID for patch A, got %s", patchIDA)
	}
	if patchIDA != patchIDB {
		t.Errorf("expected patch A and patch B to have identical patch ID, got A=%s, B=%s", patchIDA, patchIDB)
	}
	if patchIDA == patchIDC {
		t.Errorf("expected patch C (different diff) to have a different patch ID than patch A, got %s", patchIDC)
	}

	if evidence.SummaryRankScore != 1.0 {
		t.Errorf("expected summary rank score 1.0, got %f", evidence.SummaryRankScore)
	}
}

func TestCorrelate_StrictSameFunctionAndPath_GitFixture(t *testing.T) {
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

	// Base commit with multiple functions across files
	os.MkdirAll(filepath.Join(tempDir, "ospfd"), 0755)
	os.MkdirAll(filepath.Join(tempDir, "zebra"), 0755)
	os.MkdirAll(filepath.Join(tempDir, "doc"), 0755)

	os.WriteFile(filepath.Join(tempDir, "ospfd", "ospf_lsa.c"), []byte(`/* OSPF LSA Engine header comment */
#include "ospfd.h"
#include "ospf_lsa.h"

int ospf_lsa_flood(struct ospf *ospf)
{
	int v1 = 1;
	int v2 = 2;
	int v3 = 3;
	int v4 = 4;
	int v5 = 5;
	int a = 1;
	int b = 2;
	int c = 3;
	int d = 4;
	return v1 + v2 + v3 + v4 + v5 + a + b + c + d;
}

/* ---------------------------------------------------- */
/* Section separator between distinct daemon functions  */
/* ---------------------------------------------------- */

int ospf_lsa_unrelated(struct ospf *ospf)
{
	int u1 = 1;
	int u2 = 2;
	int u3 = 3;
	int u4 = 4;
	int u5 = 5;
	int u6 = 6;
	int target = 20;
	return u1 + u2 + u3 + u4 + u5 + u6 + target;
}
`), 0644)

	os.WriteFile(filepath.Join(tempDir, "zebra", "ospf_lsa.c"), []byte(`/* Zebra LSA Engine header comment */
#include "zebra.h"

int ospf_lsa_flood(struct zebra *z)
{
	int zv1 = 1;
	int zv2 = 2;
	int zv3 = 3;
	int zv4 = 4;
	int za = 1;
	int zb = 2;
	return zv1 + zv2 + zv3 + zv4 + za + zb;
}
`), 0644)

	os.WriteFile(filepath.Join(tempDir, "doc", "user.rst"), []byte("User documentation\n"), 0644)

	runGit("add", ".")
	runGit("commit", "-m", "initial base commit")
	baseSHA := runGit("rev-parse", "HEAD")

	// Commit 1 (Strict match: modifies ospf_lsa_flood in ospfd/ospf_lsa.c)
	os.WriteFile(filepath.Join(tempDir, "ospfd", "ospf_lsa.c"), []byte(`/* OSPF LSA Engine header comment */
#include "ospfd.h"
#include "ospf_lsa.h"

int ospf_lsa_flood(struct ospf *ospf)
{
	int v1 = 1;
	int v2 = 2;
	int v3 = 3;
	int v4 = 4;
	int v5 = 5;
	int a = 100;
	int b = 2;
	int c = 3;
	int d = 4;
	return v1 + v2 + v3 + v4 + v5 + a + b + c + d;
}

/* ---------------------------------------------------- */
/* Section separator between distinct daemon functions  */
/* ---------------------------------------------------- */

int ospf_lsa_unrelated(struct ospf *ospf)
{
	int u1 = 1;
	int u2 = 2;
	int u3 = 3;
	int u4 = 4;
	int u5 = 5;
	int u6 = 6;
	int target = 20;
	return u1 + u2 + u3 + u4 + u5 + u6 + target;
}
`), 0644)
	runGit("add", "ospfd/ospf_lsa.c")
	runGit("commit", "-m", "ospfd: fix flood bug")
	sha1 := runGit("rev-parse", "HEAD")

	// Commit 2 (Mention only: message mentions ospf_lsa_flood, patch modifies doc/user.rst)
	os.WriteFile(filepath.Join(tempDir, "doc", "user.rst"), []byte("User documentation updated for ospf_lsa_flood\n"), 0644)
	runGit("add", "doc/user.rst")
	runGit("commit", "-m", "doc: fix notes for ospf_lsa_flood")
	sha2 := runGit("rev-parse", "HEAD")

	// Commit 3 (Same function name, DIFFERENT file: modifies zebra/ospf_lsa.c)
	os.WriteFile(filepath.Join(tempDir, "zebra", "ospf_lsa.c"), []byte(`/* Zebra LSA Engine header comment */
#include "zebra.h"

int ospf_lsa_flood(struct zebra *z)
{
	int za = 100;
	int zb = 2;
	return za + zb;
}
`), 0644)
	runGit("add", "zebra/ospf_lsa.c")
	runGit("commit", "-m", "zebra: fix ospf_lsa_flood in zebra daemon")
	sha3 := runGit("rev-parse", "HEAD")

	// Commit 4 (Same file, DIFFERENT function: modifies ospf_lsa_unrelated in ospfd/ospf_lsa.c)
	os.WriteFile(filepath.Join(tempDir, "ospfd", "ospf_lsa.c"), []byte(`/* OSPF LSA Engine header comment */
#include "ospfd.h"
#include "ospf_lsa.h"

int ospf_lsa_flood(struct ospf *ospf)
{
	int a = 100;
	int b = 2;
	int c = 3;
	int d = 4;
	return a + b + c + d;
}

/* ---------------------------------------------------- */
/* Section separator between distinct daemon functions  */
/* ---------------------------------------------------- */

int ospf_lsa_unrelated(struct ospf *ospf)
{
	int u1 = 1;
	int u2 = 2;
	int u3 = 3;
	int u4 = 4;
	int u5 = 5;
	int u6 = 6;
	int target = 200;
	return u1 + u2 + u3 + u4 + u5 + u6 + target;
}
`), 0644)
	runGit("add", "ospfd/ospf_lsa.c")
	runGit("commit", "-m", "ospfd: fix unrelated function")
	sha4 := runGit("rev-parse", "HEAD")

	// Commit 5 (Merge commit)
	runGit("checkout", "-b", "feature-branch", baseSHA)
	os.WriteFile(filepath.Join(tempDir, "doc", "feature.rst"), []byte("Feature\n"), 0644)
	runGit("add", "doc/feature.rst")
	runGit("commit", "-m", "add feature")
	runGit("checkout", "master")
	runGit("merge", "--no-ff", "-m", "Merge pull request #500 from feature (Fixes #14000)", "feature-branch")
	sha5 := runGit("rev-parse", "HEAD")

	repo := gitx.OpenRepository(filepath.Join(tempDir, ".git"))
	correlator := NewCorrelator(repo)

	t0 := time.Now().Add(-24 * time.Hour)
	obsEnd := time.Now().Add(24 * time.Hour)

	orig := &model.OriginalPR{
		Number:         14000,
		MergedAt:       t0,
		MergeCommitSHA: baseSHA,
		ChangedFiles: []model.ChangedFile{
			{Path: "ospfd/ospf_lsa.c", Status: "modified"},
		},
		ChangedFunctionLocations: []model.ChangedFunctionLocation{
			{Path: "ospfd/ospf_lsa.c", Function: "ospf_lsa_flood"},
		},
		ChangedFunctions: []string{"ospf_lsa_flood"},
	}

	postCommits := []gitx.CommitLogEntry{
		{SHA: sha1, Date: t0.Add(1 * time.Hour), Subject: "ospfd: fix flood bug", FullMessage: "ospfd: fix flood bug"},
		{SHA: sha2, Date: t0.Add(2 * time.Hour), Subject: "doc: fix notes for ospf_lsa_flood", FullMessage: "doc: fix notes for ospf_lsa_flood"},
		{SHA: sha3, Date: t0.Add(3 * time.Hour), Subject: "zebra: fix ospf_lsa_flood in zebra daemon", FullMessage: "zebra: fix ospf_lsa_flood in zebra daemon"},
		{SHA: sha4, Date: t0.Add(4 * time.Hour), Subject: "ospfd: fix unrelated function", FullMessage: "ospfd: fix unrelated function"},
		{SHA: sha5, Date: t0.Add(5 * time.Hour), Subject: "Merge pull request #500 from feature (Fixes #14000)", FullMessage: "Merge pull request #500 from feature (Fixes #14000)"},
	}

	evidence := correlator.CorrelatePR(context.Background(), orig, postCommits, obsEnd)

	// 1. Commit 1: must emit SAME_FUNCTION_FIX for ospfd/ospf_lsa.c:ospf_lsa_flood
	var sameFuncSignals []model.MediumCorrectiveSignal
	for _, m := range evidence.MediumSignals {
		if m.SignalType == "SAME_FUNCTION_FIX" {
			sameFuncSignals = append(sameFuncSignals, m)
		}
	}
	if len(sameFuncSignals) != 1 {
		t.Fatalf("expected exactly 1 SAME_FUNCTION_FIX signal, got %d: %+v", len(sameFuncSignals), sameFuncSignals)
	}
	if sameFuncSignals[0].SourceRef != sha1 || sameFuncSignals[0].FunctionOrPath != "ospfd/ospf_lsa.c:ospf_lsa_flood" {
		t.Errorf("unexpected SAME_FUNCTION_FIX: %+v", sameFuncSignals[0])
	}

	// 2, 3, 4: Commits 2, 3, 4 must NOT emit SAME_FUNCTION_FIX (tested above, len == 1).

	// 5. Merge commit (sha5): must emit FIXES_PR strong signal, but NO diff-derived medium or weak signals
	hasMergeStrong := false
	for _, s := range evidence.StrongSignals {
		if s.SourceRef == sha5 && s.SignalType == "FIXES_PR" {
			hasMergeStrong = true
			if s.EvidenceScope != model.ScopeUnknown {
				t.Errorf("expected merge commit EvidenceScope to be ScopeUnknown, got %v", s.EvidenceScope)
			}
			if s.LogicalPatchID != "sha:"+sha5 {
				t.Errorf("expected merge commit LogicalPatchID to be sha:%s, got %s", sha5, s.LogicalPatchID)
			}
		}
	}
	if !hasMergeStrong {
		t.Errorf("missing FIXES_PR strong signal for merge commit")
	}

	for _, w := range evidence.WeakSignals {
		if w.SourceRef == sha5 {
			t.Errorf("merge commit unexpectedly emitted weak signal: %+v", w)
		}
	}
	for _, m := range evidence.MediumSignals {
		if m.SourceRef == sha5 {
			t.Errorf("merge commit unexpectedly emitted medium signal: %+v", m)
		}
	}

	// 6. Legacy PR record without ChangedFunctionLocations
	legacyPR := &model.OriginalPR{
		Number:           14000,
		MergedAt:         t0,
		MergeCommitSHA:   baseSHA,
		ChangedFunctions: []string{"ospf_lsa_flood"},
	}
	legacyEvidence := correlator.CorrelatePR(context.Background(), legacyPR, postCommits[:1], obsEnd)
	if len(legacyEvidence.MediumSignals) > 0 {
		t.Errorf("legacy PR unexpectedly promoted to SAME_FUNCTION_FIX: %+v", legacyEvidence.MediumSignals)
	}
}

func TestCorrelate_LegacyFallback(t *testing.T) {
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	obsEnd := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	// Legacy record with only flat ChangedFunctions and no ChangedFunctionLocations
	orig := &model.OriginalPR{
		Number:           15000,
		MergedAt:         t0,
		MergeCommitSHA:   "9999999999999999999999999999999999999999",
		ChangedFunctions: []string{"bgp_fsm_change_status"},
	}

	postCommits := []gitx.CommitLogEntry{
		{
			SHA:         "8888888888888888888888888888888888888888",
			Date:        t0.Add(10 * 24 * time.Hour),
			Subject:     "bgpd: fix crash in bgp_fsm_change_status",
			FullMessage: "bgpd: fix crash in bgp_fsm_change_status\n\nSome context.",
		},
	}

	correlator := NewCorrelator(nil)
	evidence := correlator.CorrelatePR(context.Background(), orig, postCommits, obsEnd)

	// Must NOT claim SAME_FUNCTION_FIX because we cannot verify same path
	if len(evidence.MediumSignals) > 0 {
		t.Errorf("expected 0 medium signals for legacy record without path locations, got %d", len(evidence.MediumSignals))
	}

	// Legacy compatibility fallback emitted in weak signals
	if len(evidence.WeakSignals) != 1 || evidence.WeakSignals[0].SignalType != "LEGACY_FLAT_FUNCTION_MATCH" {
		t.Errorf("expected LEGACY_FLAT_FUNCTION_MATCH weak signal, got %+v", evidence.WeakSignals)
	}
}

func TestCorrelate_MediumSignal_Deduplication_GitFixture(t *testing.T) {
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

	// Base commit (PR merge commit T0)
	os.MkdirAll(filepath.Join(tempDir, "bgpd"), 0755)
	os.WriteFile(filepath.Join(tempDir, "bgpd", "bgpd.c"), []byte(`/* bgpd header */
#include "bgpd.h"

int bgp_fsm_step()
{
	int v1 = 1;
	int v2 = 2;
	int v3 = 3;
	int v4 = 4;
	int a = 1;
	int b = 2;
	return v1 + v2 + v3 + v4 + a + b;
}
`), 0644)
	runGit("add", "bgpd/bgpd.c")
	runGit("commit", "-m", "initial base commit")
	baseSHA := runGit("rev-parse", "HEAD")

	// Fix A on master (modifies bgp_fsm_step)
	os.WriteFile(filepath.Join(tempDir, "bgpd", "bgpd.c"), []byte(`/* bgpd header */
#include "bgpd.h"

int bgp_fsm_step()
{
	int v1 = 1;
	int v2 = 2;
	int v3 = 3;
	int v4 = 4;
	int a = 100;
	int b = 2;
	return v1 + v2 + v3 + v4 + a + b;
}
`), 0644)
	runGit("add", "bgpd/bgpd.c")
	runGit("commit", "-m", "bgpd: fix step issue")
	shaA := runGit("rev-parse", "HEAD")

	// Fix B on stable branch (identical diff)
	runGit("checkout", "-b", "stable", baseSHA)
	os.WriteFile(filepath.Join(tempDir, "bgpd", "bgpd.c"), []byte(`/* bgpd header */
#include "bgpd.h"

int bgp_fsm_step()
{
	int v1 = 1;
	int v2 = 2;
	int v3 = 3;
	int v4 = 4;
	int a = 100;
	int b = 2;
	return v1 + v2 + v3 + v4 + a + b;
}
`), 0644)
	runGit("add", "bgpd/bgpd.c")
	runGit("commit", "-m", "bgpd: fix step issue (backport #100)")
	shaB := runGit("rev-parse", "HEAD")

	// Fix C on master (different diff in same function)
	runGit("checkout", "master")
	os.WriteFile(filepath.Join(tempDir, "bgpd", "bgpd.c"), []byte(`/* bgpd header */
#include "bgpd.h"

int bgp_fsm_step()
{
	int v1 = 1;
	int v2 = 2;
	int v3 = 3;
	int v4 = 4;
	int a = 100;
	int b = 200;
	return v1 + v2 + v3 + v4 + a + b;
}
`), 0644)
	runGit("add", "bgpd/bgpd.c")
	runGit("commit", "-m", "bgpd: fix step issue second fix")
	shaC := runGit("rev-parse", "HEAD")

	repo := gitx.OpenRepository(filepath.Join(tempDir, ".git"))
	correlator := NewCorrelator(repo)

	t0 := time.Now().Add(-24 * time.Hour)
	obsEnd := time.Now().Add(24 * time.Hour)

	orig := &model.OriginalPR{
		Number:         16000,
		MergedAt:       t0,
		MergeCommitSHA: baseSHA,
		ChangedFiles: []model.ChangedFile{
			{Path: "bgpd/bgpd.c", Status: "modified"},
		},
		ChangedFunctionLocations: []model.ChangedFunctionLocation{
			{Path: "bgpd/bgpd.c", Function: "bgp_fsm_step"},
		},
	}

	// Case 1: Correlate with Commit A and Commit B (equivalent patches)
	postCommits1 := []gitx.CommitLogEntry{
		{SHA: shaA, Date: t0.Add(1 * time.Hour), Subject: "bgpd: fix step issue", FullMessage: "bgpd: fix step issue"},
		{SHA: shaB, Date: t0.Add(2 * time.Hour), Subject: "bgpd: fix step issue (backport #100)", FullMessage: "bgpd: fix step issue (backport #100)"},
	}
	ev1 := correlator.CorrelatePR(context.Background(), orig, postCommits1, obsEnd)

	if len(ev1.MediumSignals) != 2 {
		t.Fatalf("expected 2 medium signals, got %d", len(ev1.MediumSignals))
	}
	if ev1.MediumSignals[0].LogicalPatchID != ev1.MediumSignals[1].LogicalPatchID {
		t.Errorf("expected identical LogicalPatchID for equivalent fixes")
	}
	// Score: 0.50 + 1 * 0.05 = 0.55
	if ev1.SummaryRankScore != 0.55 {
		t.Errorf("expected score 0.55 for 1 unique medium lead, got %f", ev1.SummaryRankScore)
	}

	// Case 2: Correlate with Commit A, B, and C (distinct patch in same function)
	postCommits2 := append(postCommits1, gitx.CommitLogEntry{
		SHA: shaC, Date: t0.Add(3 * time.Hour), Subject: "bgpd: fix step issue second fix", FullMessage: "bgpd: fix step issue second fix",
	})
	ev2 := correlator.CorrelatePR(context.Background(), orig, postCommits2, obsEnd)

	if len(ev2.MediumSignals) != 3 {
		t.Fatalf("expected 3 medium signals, got %d", len(ev2.MediumSignals))
	}
	// Score: 0.50 + 2 * 0.05 = 0.60
	if ev2.SummaryRankScore != 0.60 {
		t.Errorf("expected score 0.60 for 2 unique medium leads, got %f", ev2.SummaryRankScore)
	}
}

func TestCorrelate_DirectLineageAndReversal_GitFixture(t *testing.T) {
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

	// 1. Initial repo setup with a large file (> 2,000 lines) and auxiliary files
	os.MkdirAll(filepath.Join(tempDir, "bgpd"), 0755)
	os.MkdirAll(filepath.Join(tempDir, "zebra"), 0755)

	var largeFileBuilder strings.Builder
	largeFileBuilder.WriteString("/* bgpd large file */\n#include \"bgpd.h\"\n\n")
	for i := 1; i <= 2100; i++ {
		largeFileBuilder.WriteString(fmt.Sprintf("int filler_func_%d() { return %d; }\n", i, i))
	}
	largeFileBuilder.WriteString("int target_gr_func() {\n\tint x = 1;\n\treturn x;\n}\n")
	for i := 2101; i <= 2500; i++ {
		largeFileBuilder.WriteString(fmt.Sprintf("int filler_func_%d() { return %d; }\n", i, i))
	}

	os.WriteFile(filepath.Join(tempDir, "bgpd", "bgpd_large.c"), []byte(largeFileBuilder.String()), 0644)
	os.WriteFile(filepath.Join(tempDir, "zebra", "zebra.c"), []byte("int zebra_init() { return 0; }\n"), 0644)
	runGit("add", ".")
	runGit("commit", "-m", "initial root commit")
	rootSHA := runGit("rev-parse", "HEAD")

	// 2. Original PR: Modifies target_gr_func in bgpd_large.c (adds custom line) and zebra.c
	runGit("checkout", "-b", "pr-branch")
	prLargeContent := strings.Replace(
		largeFileBuilder.String(),
		"int target_gr_func() {\n\tint x = 1;\n\treturn x;\n}\n",
		"int target_gr_func() {\n\tint x = 1;\n\tpeer->gr_timer = 100;\n\treturn x;\n}\n",
		1,
	)
	os.WriteFile(filepath.Join(tempDir, "bgpd", "bgpd_large.c"), []byte(prLargeContent), 0644)
	os.WriteFile(filepath.Join(tempDir, "zebra", "zebra.c"), []byte("int zebra_init() {\n\tint z = 10;\n\treturn z;\n}\n"), 0644)
	runGit("add", ".")
	runGit("commit", "-m", "PR: add gr_timer and update zebra")
	prHeadSHA := runGit("rev-parse", "HEAD")

	runGit("checkout", "master")
	runGit("merge", "--no-ff", "-m", "Merge pull request #17000 from pr-branch", "pr-branch")
	mergeSHA := runGit("rev-parse", "HEAD")

	// 3. Fix Commit 1 (Direct line modification in >2,000 line file):
	// Modifies `peer->gr_timer = 100;` to `peer->gr_timer = 200;`
	fix1Content := strings.Replace(
		prLargeContent,
		"peer->gr_timer = 100;",
		"peer->gr_timer = 200;",
		1,
	)
	os.WriteFile(filepath.Join(tempDir, "bgpd", "bgpd_large.c"), []byte(fix1Content), 0644)
	runGit("add", "bgpd/bgpd_large.c")
	runGit("commit", "-m", "bgpd: fix gr_timer value\n\nFixes: "+mergeSHA[:12])
	fix1SHA := runGit("rev-parse", "HEAD")

	// 4. Fix Commit 2 (Unrelated change in same function):
	// Modifies `int x = 1;` to `int x = 2;` (line was in root commit, not PR)
	fix2Content := strings.Replace(
		fix1Content,
		"int x = 1;",
		"int x = 2;",
		1,
	)
	os.WriteFile(filepath.Join(tempDir, "bgpd", "bgpd_large.c"), []byte(fix2Content), 0644)
	runGit("add", "bgpd/bgpd_large.c")
	runGit("commit", "-m", "bgpd: fix x var\n\nFixes: "+mergeSHA[:12])
	fix2SHA := runGit("rev-parse", "HEAD")

	// 5. Fix Commit 3 (Full Git Revert of PR):
	runGit("checkout", "-b", "revert-branch", mergeSHA)
	runGit("revert", "--no-edit", prHeadSHA)
	fix3SHA := runGit("rev-parse", "HEAD")

	// 6. Fix Commit 4 (Merge commit):
	runGit("checkout", "master")
	runGit("checkout", "-b", "feature-merge-branch")
	os.MkdirAll(filepath.Join(tempDir, "doc"), 0755)
	os.WriteFile(filepath.Join(tempDir, "doc", "feature.txt"), []byte("feature doc\n"), 0644)
	runGit("add", "doc/feature.txt")
	runGit("commit", "-m", "add feature doc")
	runGit("checkout", "master")
	runGit("merge", "--no-ff", "-m", "Merge pull request #17500 (Fixes #17000)", "feature-merge-branch")
	fix4SHA := runGit("rev-parse", "HEAD")

	repo := gitx.OpenRepository(filepath.Join(tempDir, ".git"))
	correlator := NewCorrelator(repo)

	t0 := time.Now().Add(-24 * time.Hour)
	obsEnd := time.Now().Add(24 * time.Hour)

	orig := &model.OriginalPR{
		Number:         17000,
		MergedAt:       t0,
		BaseSHA:        rootSHA,
		HeadSHA:        prHeadSHA,
		MergeCommitSHA: mergeSHA,
		CommitSHAs:     []string{prHeadSHA},
		ChangedFiles: []model.ChangedFile{
			{Path: "bgpd/bgpd_large.c", Status: "modified"},
			{Path: "zebra/zebra.c", Status: "modified"},
		},
		ChangedFunctionLocations: []model.ChangedFunctionLocation{
			{Path: "bgpd/bgpd_large.c", Function: "target_gr_func"},
		},
	}

	postCommits := []gitx.CommitLogEntry{
		{SHA: fix1SHA, Date: t0.Add(1 * time.Hour), Subject: "bgpd: fix gr_timer value", FullMessage: "bgpd: fix gr_timer value\n\nFixes: " + mergeSHA[:12]},
		{SHA: fix2SHA, Date: t0.Add(2 * time.Hour), Subject: "bgpd: fix x var", FullMessage: "bgpd: fix x var\n\nFixes: " + mergeSHA[:12]},
		{SHA: fix3SHA, Date: t0.Add(3 * time.Hour), Subject: "Revert PR", FullMessage: "Revert PR\n\nFixes: " + mergeSHA[:12]},
		{SHA: fix4SHA, Date: t0.Add(4 * time.Hour), Subject: "Merge PR #17500", FullMessage: "Merge PR #17500 (Fixes #17000)"},
	}

	evidence := correlator.CorrelatePR(context.Background(), orig, postCommits, obsEnd)

	if len(evidence.CommitRelationships) != 4 {
		t.Fatalf("expected 4 relationship entries, got %d", len(evidence.CommitRelationships))
	}

	relMap := make(map[string]model.CommitRelationshipEvidence)
	for _, rel := range evidence.CommitRelationships {
		relMap[rel.SourceRef] = rel
	}

	// Assert Fix 1 (Direct lineage in >2,000 line file):
	rel1 := relMap[fix1SHA]
	if rel1.AnalysisStatus != "complete" {
		t.Errorf("expected complete status for fix1, got %s (note: %s)", rel1.AnalysisStatus, rel1.AnalysisNote)
	}
	if rel1.Lineage == nil || rel1.Lineage.LinesBlamedToOriginalPR != 1 || rel1.Lineage.DirectLineageFraction != 1.0 {
		t.Errorf("expected direct lineage 1.0 for fix1, got %+v", rel1.Lineage)
	}
	if rel1.Reversal == nil || rel1.Reversal.LineageSupportedRemovedOverlapCount != 1 || rel1.Reversal.ReversalType != "partial_candidate" {
		t.Errorf("expected partial_candidate reversal for fix1, got %+v", rel1.Reversal)
	}

	// Assert Fix 2 (Unrelated change in same function -> 0 lineage to PR):
	rel2 := relMap[fix2SHA]
	if rel2.AnalysisStatus != "complete" {
		t.Errorf("expected complete status for fix2, got %s", rel2.AnalysisStatus)
	}
	if rel2.Lineage == nil || rel2.Lineage.LinesBlamedToOriginalPR != 0 || rel2.Lineage.DirectLineageFraction != 0.0 {
		t.Errorf("expected 0 direct lineage for fix2, got %+v", rel2.Lineage)
	}

	// Assert Fix 3 (Full Git Revert):
	rel3 := relMap[fix3SHA]
	if rel3.AnalysisStatus != "complete" {
		t.Errorf("expected complete status for fix3, got %s", rel3.AnalysisStatus)
	}
	if rel3.Reversal == nil || !rel3.Reversal.FullReversePatchMatch || rel3.Reversal.ReversalType != "full" {
		t.Errorf("expected full reversal for fix3, got %+v", rel3.Reversal)
	}

	// Assert Fix 4 (Merge commit -> skipped_merge):
	rel4 := relMap[fix4SHA]
	if rel4.AnalysisStatus != "skipped_merge" {
		t.Errorf("expected skipped_merge for fix4, got %s", rel4.AnalysisStatus)
	}
	if rel4.Lineage != nil || rel4.Reversal != nil {
		t.Errorf("expected nil lineage/reversal for skipped_merge, got %+v", rel4)
	}

	// Assert deterministic ascending sort of SourceRef
	for i := 0; i < len(evidence.CommitRelationships)-1; i++ {
		if evidence.CommitRelationships[i].SourceRef > evidence.CommitRelationships[i+1].SourceRef {
			t.Errorf("CommitRelationships not sorted ascending at index %d", i)
		}
	}
}

func TestCorrelate_ReversalEdgeCases_GitFixture(t *testing.T) {
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

	os.MkdirAll(filepath.Join(tempDir, "pkg1"), 0755)
	os.MkdirAll(filepath.Join(tempDir, "pkg2"), 0755)

	// Base commit: has int target_a = 100; in base_func1 and int target_b = 200; in base_func2
	os.WriteFile(filepath.Join(tempDir, "pkg1", "a.c"), []byte("/* pkg1 a.c */\nint base_func1() {\n\tint target_a = 100;\n\treturn 0;\n}\n"), 0644)
	os.WriteFile(filepath.Join(tempDir, "pkg2", "b.c"), []byte("/* pkg2 b.c */\nint base_func2() {\n\tint target_b = 200;\n\treturn 0;\n}\n"), 0644)
	runGit("add", ".")
	runGit("commit", "-m", "base")
	baseSHA := runGit("rev-parse", "HEAD")

	// PR branch: introduces new functions with substantive additions int target_a = 100; in pkg1 and int target_b = 200; in pkg2
	runGit("checkout", "-b", "pr-branch")
	os.WriteFile(filepath.Join(tempDir, "pkg1", "a.c"), []byte("/* pkg1 a.c */\nint base_func1() {\n\tint target_a = 100;\n\treturn 0;\n}\nint pr_func1() {\n\tint target_a = 100;\n\treturn 0;\n}\n"), 0644)
	os.WriteFile(filepath.Join(tempDir, "pkg2", "b.c"), []byte("/* pkg2 b.c */\nint base_func2() {\n\tint target_b = 200;\n\treturn 0;\n}\nint pr_func2() {\n\tint target_b = 200;\n\treturn 0;\n}\n"), 0644)
	runGit("add", ".")
	runGit("commit", "-m", "PR commit")
	prHeadSHA := runGit("rev-parse", "HEAD")

	runGit("checkout", "master")
	runGit("merge", "--no-ff", "-m", "Merge PR #18000", "pr-branch")
	mergeSHA := runGit("rev-parse", "HEAD")

	// Commit 1: Cross-path single line matches (1 in a.c, 1 in b.c, 0 lineage support)
	// Modifies base_func1 in a.c (removes int target_a = 100; which originated in baseSHA)
	// Modifies base_func2 in b.c (removes int target_b = 200; which originated in baseSHA)
	os.WriteFile(filepath.Join(tempDir, "pkg1", "a.c"), []byte("/* pkg1 a.c */\nint base_func1() {\n\tint target_a = 999;\n\treturn 0;\n}\nint pr_func1() {\n\tint target_a = 100;\n\treturn 0;\n}\n"), 0644)
	os.WriteFile(filepath.Join(tempDir, "pkg2", "b.c"), []byte("/* pkg2 b.c */\nint base_func2() {\n\tint target_b = 999;\n\treturn 0;\n}\nint pr_func2() {\n\tint target_b = 200;\n\treturn 0;\n}\n"), 0644)
	runGit("add", ".")
	runGit("commit", "-m", "fix: cross-path common lines (Fixes #18000)")
	c1SHA := runGit("rev-parse", "HEAD")

	// Commit 2: Lineage-supported single line reversal (1 line in a.c that is blamed to PR)
	// Modifies pr_func1 in a.c (removes int target_a = 100; which originated in prHeadSHA)
	os.WriteFile(filepath.Join(tempDir, "pkg1", "a.c"), []byte("/* pkg1 a.c */\nint base_func1() {\n\tint target_a = 999;\n\treturn 0;\n}\nint pr_func1() {\n\tint target_a = 888;\n\treturn 0;\n}\n"), 0644)
	runGit("add", "pkg1/a.c")
	runGit("commit", "-m", "fix: modify pr_func1 (Fixes #18000)")
	c2SHA := runGit("rev-parse", "HEAD")

	repo := gitx.OpenRepository(filepath.Join(tempDir, ".git"))
	correlator := NewCorrelator(repo)

	t0 := time.Now().Add(-24 * time.Hour)
	obsEnd := time.Now().Add(24 * time.Hour)

	orig := &model.OriginalPR{
		Number:         18000,
		MergedAt:       t0,
		BaseSHA:        baseSHA,
		HeadSHA:        prHeadSHA,
		MergeCommitSHA: mergeSHA,
		CommitSHAs:     []string{prHeadSHA},
		ChangedFiles: []model.ChangedFile{
			{Path: "pkg1/a.c", Status: "modified"},
			{Path: "pkg2/b.c", Status: "modified"},
		},
	}

	postCommits := []gitx.CommitLogEntry{
		{SHA: c1SHA, Date: t0.Add(1 * time.Hour), Subject: "fix: cross-path", FullMessage: "fix: cross-path\n\nFixes: " + mergeSHA[:12]},
		{SHA: c2SHA, Date: t0.Add(2 * time.Hour), Subject: "fix: modify pr_func1", FullMessage: "fix: modify pr_func1\n\nFixes: " + mergeSHA[:12]},
	}

	evidence := correlator.CorrelatePR(context.Background(), orig, postCommits, obsEnd)

	relMap := make(map[string]model.CommitRelationshipEvidence)
	for _, rel := range evidence.CommitRelationships {
		relMap[rel.SourceRef] = rel
	}

	// Commit 1: Cross-path single line matches without lineage support -> ReversalType must be "none"
	r1 := relMap[c1SHA]
	if r1.Reversal == nil {
		t.Fatalf("expected reversal evidence for c1, got nil (status=%s note=%s)", r1.AnalysisStatus, r1.AnalysisNote)
	}
	if r1.Reversal.ReversedLineOverlapCount != 2 {
		t.Errorf("expected total overlap 2 for c1, got %d", r1.Reversal.ReversedLineOverlapCount)
	}
	if len(r1.Reversal.PathOverlaps) != 2 {
		t.Fatalf("expected 2 path overlaps for c1, got %d", len(r1.Reversal.PathOverlaps))
	}
	if r1.Reversal.PathOverlaps[0].LaterRemovedMatchingOriginalAdditions != 1 || r1.Reversal.PathOverlaps[1].LaterRemovedMatchingOriginalAdditions != 1 {
		t.Errorf("expected 1 removed overlap per path for c1, got %+v", r1.Reversal.PathOverlaps)
	}
	if r1.Reversal.LineageSupportedRemovedOverlapCount != 0 {
		t.Errorf("expected 0 lineage supported overlap for c1, got %d", r1.Reversal.LineageSupportedRemovedOverlapCount)
	}
	if r1.Reversal.ReversalType != "none" {
		t.Errorf("expected reversal_type none for cross-path single line matches, got %s", r1.Reversal.ReversalType)
	}

	// Commit 2: Lineage supported single line reversal -> ReversalType must be "partial_candidate"
	r2 := relMap[c2SHA]
	if r2.Reversal == nil || r2.Reversal.ReversalType != "partial_candidate" || r2.Reversal.LineageSupportedRemovedOverlapCount != 1 {
		t.Errorf("expected partial_candidate with LineageSupportedRemovedOverlapCount=1 for c2, got %+v", r2.Reversal)
	}
}

func TestCorrelate_DirectionSensitiveAndUnavailable_GitFixture(t *testing.T) {
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

	// 1. Base commit
	os.WriteFile(filepath.Join(tempDir, "router.c"), []byte("/* router.c */\nint init_router() {\n\tint existing = 0;\n\treturn existing;\n}\n"), 0644)
	runGit("add", "router.c")
	runGit("commit", "-m", "base")
	baseSHA := runGit("rev-parse", "HEAD")

	// 2. PR commit: adds Line A (int feature_alpha = 100;)
	runGit("checkout", "-b", "pr-branch")
	os.WriteFile(filepath.Join(tempDir, "router.c"), []byte("/* router.c */\nint init_router() {\n\tint existing = 0;\n\tint feature_alpha = 100;\n\treturn existing;\n}\n"), 0644)
	runGit("add", "router.c")
	runGit("commit", "-m", "PR: add feature_alpha")
	prHeadSHA := runGit("rev-parse", "HEAD")

	runGit("checkout", "master")
	runGit("merge", "--no-ff", "-m", "Merge PR #19000", "pr-branch")
	mergeSHA := runGit("rev-parse", "HEAD")

	// 3. Fix commit C: forward diff removes Line A and adds Line B (int feature_alpha = 200;)
	// Its reverse diff (C -> parent) removes Line B and adds Line A
	os.WriteFile(filepath.Join(tempDir, "router.c"), []byte("/* router.c */\nint init_router() {\n\tint existing = 0;\n\tint feature_alpha = 200;\n\treturn existing;\n}\n"), 0644)
	runGit("add", "router.c")
	runGit("commit", "-m", "fix: update feature_alpha value\n\nFixes: "+mergeSHA[:12])
	fixSHA := runGit("rev-parse", "HEAD")

	// 4. Fix commit D: full revert of PR
	runGit("checkout", "-b", "revert-branch", mergeSHA)
	runGit("revert", "--no-edit", prHeadSHA)
	revertSHA := runGit("rev-parse", "HEAD")

	repo := gitx.OpenRepository(filepath.Join(tempDir, ".git"))
	correlator := NewCorrelator(repo)

	t0 := time.Now().Add(-24 * time.Hour)
	obsEnd := time.Now().Add(24 * time.Hour)

	orig := &model.OriginalPR{
		Number:         19000,
		MergedAt:       t0,
		BaseSHA:        baseSHA,
		HeadSHA:        prHeadSHA,
		MergeCommitSHA: mergeSHA,
		CommitSHAs:     []string{prHeadSHA},
		ChangedFiles: []model.ChangedFile{
			{Path: "router.c", Status: "modified"},
		},
	}

	postCommits := []gitx.CommitLogEntry{
		{SHA: fixSHA, Date: t0.Add(1 * time.Hour), Subject: "fix: update feature_alpha", FullMessage: "fix: update feature_alpha\n\nFixes: " + mergeSHA[:12]},
		{SHA: revertSHA, Date: t0.Add(2 * time.Hour), Subject: "Revert PR", FullMessage: "Revert PR\n\nFixes: " + mergeSHA[:12]},
	}

	evidence := correlator.CorrelatePR(context.Background(), orig, postCommits, obsEnd)

	relMap := make(map[string]model.CommitRelationshipEvidence)
	for _, rel := range evidence.CommitRelationships {
		relMap[rel.SourceRef] = rel
	}

	// Assert direction-sensitivity on fix commit:
	// Forward diff removes Line A (which matches original addition A)
	relFix := relMap[fixSHA]
	if relFix.Reversal == nil {
		t.Fatalf("expected reversal evidence for fix commit, got nil")
	}
	if relFix.Reversal.LaterRemovedLinesMatchingOriginalAdditions != 1 {
		t.Errorf("expected 1 later removal matching original addition, got %d", relFix.Reversal.LaterRemovedLinesMatchingOriginalAdditions)
	}
	if relFix.Reversal.LaterAddedLinesMatchingOriginalRemovals != 0 {
		t.Errorf("expected 0 later addition matching original removal, got %d", relFix.Reversal.LaterAddedLinesMatchingOriginalRemovals)
	}
	if relFix.Reversal.LineageSupportedRemovedOverlapCount != 1 {
		t.Errorf("expected 1 lineage supported overlap, got %d", relFix.Reversal.LineageSupportedRemovedOverlapCount)
	}
	if relFix.Reversal.ReversalType != "partial_candidate" {
		t.Errorf("expected partial_candidate for fix commit, got %s", relFix.Reversal.ReversalType)
	}
	if relFix.Reversal.FullReversePatchMatch {
		t.Errorf("expected FullReversePatchMatch=false for non-revert commit")
	}

	// Assert full-reversal detection uses reverse patch ID:
	relRev := relMap[revertSHA]
	if relRev.Reversal == nil || !relRev.Reversal.FullReversePatchMatch || relRev.Reversal.ReversalType != "full" {
		t.Errorf("expected FullReversePatchMatch=true and ReversalType=full for revert commit, got %+v", relRev.Reversal)
	}

	// Assert unavailable status when correlator repo is nil
	correlatorNil := NewCorrelator(nil)
	evNil := correlatorNil.CorrelatePR(context.Background(), orig, postCommits, obsEnd)
	if len(evNil.CommitRelationships) != 2 {
		t.Fatalf("expected 2 unavailable relationships when repo is nil, got %d", len(evNil.CommitRelationships))
	}
	for _, rel := range evNil.CommitRelationships {
		if rel.AnalysisStatus != "unavailable" {
			t.Errorf("expected status unavailable, got %s", rel.AnalysisStatus)
		}
	}

	// Assert partial status and preserved lineage when base_sha is empty
	origNoBase := &model.OriginalPR{
		Number:         19000,
		MergedAt:       t0,
		HeadSHA:        prHeadSHA,
		MergeCommitSHA: mergeSHA,
		CommitSHAs:     []string{prHeadSHA},
	}
	evNoBase := correlator.CorrelatePR(context.Background(), origNoBase, postCommits[:1], obsEnd)
	if len(evNoBase.CommitRelationships) != 1 {
		t.Fatalf("expected 1 relationship for origNoBase, got %d", len(evNoBase.CommitRelationships))
	}
	relNoBase := evNoBase.CommitRelationships[0]
	if relNoBase.AnalysisStatus != "partial" {
		t.Errorf("expected partial status when base_sha is empty, got %s", relNoBase.AnalysisStatus)
	}
	if relNoBase.Lineage == nil || relNoBase.Lineage.LinesBlamedToOriginalPR != 1 {
		t.Errorf("expected lineage to be preserved when base_sha is empty, got %+v", relNoBase.Lineage)
	}
	if relNoBase.Reversal != nil {
		t.Errorf("expected nil reversal when base_sha is empty, got %+v", relNoBase.Reversal)
	}
}

func TestComputeStablePatchID_ErrorHandling(t *testing.T) {
	_, err := computeStablePatchID(context.Background(), "invalid_dir", "")
	if err == nil {
		t.Errorf("expected error on empty diff text, got nil")
	}

	_, err = computeStablePatchID(context.Background(), "invalid_dir", "some diff")
	if err == nil {
		t.Errorf("expected error with invalid repo dir, got nil")
	}
}
