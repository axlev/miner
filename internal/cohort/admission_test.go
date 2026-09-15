package cohort

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v62/github"
	"miner/internal/collector"
	"miner/internal/model"
	"miner/internal/prospectiveexport"
	"miner/internal/storage"
)

func writeBundle(t *testing.T, cache *storage.DiskCache, pr int, b collector.RawPRBundle) {
	t.Helper()
	b.PR.Number = github.Int(pr)
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.WritePR("owner/repo", pr, raw); err != nil {
		t.Fatal(err)
	}
}

func TestCohortExportRecordsAdmissionWhenGivenTheCache(t *testing.T) {
	records := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 20, 0.7, 1, 0, 0, 400),
		cohortRecord(2, "bgpd", 20, 0.7, 0, 0, 0, 400),
	}
	merged := records[0].Original.MergedAt
	cacheDir := t.TempDir()
	cache := storage.NewDiskCache(cacheDir)
	created := &github.Timestamp{Time: merged.Add(-48 * time.Hour)}
	// PR 1: complete histories, body edited after merge -> title admitted, body omitted.
	writeBundle(t, cache, 1, collector.RawPRBundle{
		PR:                  &github.PullRequest{Title: github.String("t"), Body: github.String("b"), CreatedAt: created},
		IssueEventsComplete: true,
		BundleVersion:       collector.BundleVersion,
		BodyEditsComplete:   true,
		BodyEdits:           []collector.BodyEdit{{EditedAt: merged.Add(time.Hour)}},
	})
	// PR 2: old bundle -> body unverifiable, title unverifiable (events incomplete).
	writeBundle(t, cache, 2, collector.RawPRBundle{PR: &github.PullRequest{Title: github.String("t"), Body: github.String("b"), CreatedAt: created}})

	opt := options(t, "out")
	opt.CacheDir = cacheDir
	m, err := Export(records, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	cases := readCases(t, opt.Out)
	if cases[0].Admission["title"].Outcome != prospectiveexport.OutcomeAdmitted || cases[0].Admission["title"].Method != prospectiveexport.MethodCurrent {
		t.Errorf("PR1 title admission = %+v", cases[0].Admission["title"])
	}
	if cases[0].Admission["description"].Outcome != prospectiveexport.OutcomeOmittedPostCutoff {
		t.Errorf("PR1 description admission = %+v", cases[0].Admission["description"])
	}
	if cases[1].Admission["description"].Outcome != prospectiveexport.OutcomeOmittedUnverifiable || cases[1].Admission["title"].Outcome != prospectiveexport.OutcomeOmittedUnverifiable {
		t.Errorf("PR2 admission = %+v", cases[1].Admission)
	}
	if m.Admission["title"]["admitted"] != 1 || m.Admission["title"]["omitted-unverifiable"] != 1 || m.Admission["description"]["omitted-post-cutoff-edit"] != 1 || m.Admission["description"]["omitted-unverifiable"] != 1 {
		t.Errorf("manifest admission counts = %+v", m.Admission)
	}

	// Without the cache: nothing is recorded, nothing is guessed.
	plain := options(t, "plain")
	mp, err := Export(records, nil, plain)
	if err != nil {
		t.Fatal(err)
	}
	if mp.Admission != nil || readCases(t, plain.Out)[0].Admission != nil {
		t.Errorf("admission must be absent without a cache")
	}

	// A case whose bundle is missing fails the export rather than being skipped.
	missing := options(t, "missing")
	missing.CacheDir = t.TempDir()
	if _, err := Export(records, nil, missing); err == nil || !strings.Contains(err.Error(), "cache bundle needed") {
		t.Errorf("missing bundle: err = %v", err)
	}
}
