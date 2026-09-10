package prospectiveexport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Bundle layout constants. The engine passes <bundle-root> to its runner and
// allow-list-copies only the three named entries under reviewer/, so control/ is
// unreachable from a reasoner by construction rather than by filtering.
const (
	// ReviewerDir holds everything a reasoner may ever see.
	ReviewerDir = "reviewer"
	// ControlDir is engine-only and is never mounted to a reasoner.
	ControlDir = "control"
	// SnapshotDir is the materialized source tree of the cutoff commit.
	SnapshotDir = "repository"
	// DiffFile is the admissible diff, named as the engine requires.
	DiffFile = "diff.patch"
	// MetadataFile is the reviewer-visible metadata.
	MetadataFile = "metadata.json"
	// ControlManifestFile carries engine-only routing and identity.
	ControlManifestFile = "manifest.json"
	// ChecksumsFile carries integrity over every reviewer/ file.
	ChecksumsFile = "checksums.sha256"
)

// This file deliberately contains no copy of engine-runner's boundary rules.
//
// It used to. The miner mirrored `engine-runner/internal/boundaryvalidator` so a bad
// bundle would fail at export rather than at ingest. That mirror was a liability: two
// hand-maintained rule tables in two repositories with no shared memory and no way to
// compare them, because Go forbids importing another module's internal/ packages. A
// rule tightened on the engine side would leave the miner cheerfully publishing bundles
// the engine rejects, with nothing to detect the divergence.
//
// The engine's validator is now the single gate, exercised on real artifacts by
// `miner cohort-verify --bench-repo`, which runs `go run ./cmd/bench` against every
// bundle it builds. Admissibility is decided in exactly one place.
//
// What remains here is not validation of the engine's rules. It is the miner's own
// production correctness (writing the checksum manifest) and the one contamination
// check the engine explicitly delegates (see evaluatorArtifactBasenames).

// evaluatorArtifactBasenames are exact filenames an evaluator-only bundle's own
// artifacts carry. Finding one inside a source snapshot means oracle material leaked
// into reviewer-visible space.
//
// This is NOT a copy of an engine rule; it is a gap the engine deliberately leaves to
// the miner. The engine's in-snapshot heuristic intentionally omits the `ground_truth`
// fragment, because ML repositories use the term legitimately and a check that fires
// constantly on clean input gets switched off. That reasoning is sound for a substring
// match over third-party source, and it means nothing downstream will catch a literal
// `ground_truth.json` written into a snapshot. Exact basenames stay high-signal where
// substrings do not, so the miner blocks these itself.
var evaluatorArtifactBasenames = map[string]bool{
	"oracle.json": true, "oracle.yaml": true, "oracle.yml": true,
	"ground_truth.json": true, "groundtruth.json": true,
	"answer_key.json": true, "answerkey.json": true,
	"expected_findings.json": true, "expected-findings.json": true,
	"retrospective.json": true,
}

// reviewerFiles returns every regular file under <bundleRoot>/reviewer, as
// bundle-root-relative slash paths, sorted. Irregular entries are reported rather
// than digested: the exporter cannot have produced one, so encountering one means
// something outside this package wrote into the tree.
func reviewerFiles(bundleRoot string) ([]string, error) {
	reviewerRoot := filepath.Join(bundleRoot, ReviewerDir)
	var files []string
	var irregular []string
	err := filepath.WalkDir(reviewerRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(bundleRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !info.Mode().IsRegular() {
			irregular = append(irregular, fmt.Sprintf("%s (%s)", rel, info.Mode().Type()))
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(irregular) > 0 {
		sort.Strings(irregular)
		return nil, fmt.Errorf("reviewer tree contains %d irregular file(s): %s", len(irregular), strings.Join(irregular, ", "))
	}
	sort.Strings(files)
	return files, nil
}

// buildChecksums renders control/checksums.sha256: one sha256sum-format line per
// regular file under reviewer/, with paths relative to the bundle root. The engine
// requires this file to describe exactly that set, in both directions.
func buildChecksums(bundleRoot string) ([]byte, error) {
	files, err := reviewerFiles(bundleRoot)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, rel := range files {
		f, err := os.Open(filepath.Join(bundleRoot, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return nil, err
		}
		f.Close()
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(h.Sum(nil)), rel)
	}
	return []byte(b.String()), nil
}

// checkEvaluatorArtifactNames fails the export when a snapshot file carries the exact
// filename of an evaluator-only artifact. See evaluatorArtifactBasenames for why this
// one check stays on the miner rather than being left to the engine.
func checkEvaluatorArtifactNames(bundleRoot string) error {
	files, err := reviewerFiles(bundleRoot)
	if err != nil {
		return err
	}
	var found []string
	for _, rel := range files {
		if evaluatorArtifactBasenames[strings.ToLower(filepath.Base(rel))] {
			found = append(found, rel)
		}
	}
	if len(found) > 0 {
		sort.Strings(found)
		return fmt.Errorf("evaluator-only artifact name(s) in reviewer-visible space: %s", strings.Join(found, ", "))
	}
	return nil
}
