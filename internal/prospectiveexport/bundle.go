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

var reviewerAllowedEntries = map[string]bool{SnapshotDir: true, DiffFile: true, MetadataFile: true}

var controlAllowedEntries = map[string]bool{ControlManifestFile: true, ChecksumsFile: true}

// gitMetadataNames mirrors the consumer's no_git_metadata rule. gitx.ListTree
// already refuses to materialize these, so reaching one here means a file entered
// the bundle from somewhere other than the object database.
var gitMetadataNames = map[string]bool{
	".git": true, ".gitmodules": true, "packed-refs": true, "HEAD": true,
	"ORIG_HEAD": true, "FETCH_HEAD": true, "MERGE_HEAD": true, "shallow": true,
	"objects": true, "refs": true, "reflogs": true, "worktrees": true, "alternates": true,
}

// oracleShapedSubstrings is the consumer's blunt heuristic, applied here to
// miner-authored surfaces only: the entries directly under reviewer/. On those
// paths the miner controls every name, so a match is a defect, not a coincidence.
var oracleShapedSubstrings = []string{
	"oracle", "ground_truth", "groundtruth", "expected_finding", "expected-finding",
	"answer_key", "answerkey", "solution", "postmortem", "post_mortem",
	"regression_report", "fix_commit", "final_state", "review_comments", "ci_result", "verdict",
}

// snapshotOracleBasenames are exact filenames an evaluator-only bundle's own
// artifacts carry. Unlike the substring list they stay high-signal inside
// third-party source, where "oracle" means Oracle Database and "solution" means a
// .sln file.
//
// ground_truth.json is listed here deliberately: the consumer excludes
// "ground_truth" from the fragments it matches inside a snapshot, because ML
// repositories use the term legitimately, which leaves this exact-filename case to
// the miner.
var snapshotOracleBasenames = map[string]bool{
	"oracle.json": true, "oracle.yaml": true, "oracle.yml": true,
	"ground_truth.json": true, "groundtruth.json": true,
	"answer_key.json": true, "answerkey.json": true,
	"expected_findings.json": true, "expected-findings.json": true,
	"retrospective.json": true,
}

// snapshotOracleSubstrings are the fragments specific enough to this benchmark's
// vocabulary to be worth matching even inside third-party source.
var snapshotOracleSubstrings = []string{"expected_finding", "expected-finding", "answer_key", "answerkey", "fix_commit"}

// reviewerFiles returns every regular file under <bundleRoot>/reviewer, as
// bundle-root-relative slash paths, sorted. Irregular entries are reported rather
// than digested: they are inadmissible, and hashing them would only mask that.
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
// regular file under reviewer/, with paths relative to the bundle root.
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

// validateBundle re-derives the consumer's admissibility rules against the fully
// built bundle, before it is published. It is deliberately a second, independent
// pass over the bytes on disk rather than an assertion about the values that were
// used to write them.
//
// Errors are the rules a consumer cannot waive: layout, irregular files, Git
// metadata, pinned schema versions, and checksum agreement. Warnings are the
// lexical oracle-name heuristic applied inside the source snapshot, whose
// vocabulary this repository does not control — those are reported for a human to
// judge, per the ingest contract's instruction to report false positives rather
// than work around them.
func validateBundle(bundleRoot string, metadata, controlManifest, checksums []byte) ([]string, error) {
	top, err := os.ReadDir(bundleRoot)
	if err != nil {
		return nil, err
	}
	seenTop := map[string]bool{}
	for _, e := range top {
		if !e.IsDir() || (e.Name() != ReviewerDir && e.Name() != ControlDir) {
			return nil, fmt.Errorf("unexpected bundle entry %q; the root holds exactly %s/ and %s/", e.Name(), ReviewerDir, ControlDir)
		}
		seenTop[e.Name()] = true
	}
	if !seenTop[ReviewerDir] || !seenTop[ControlDir] {
		return nil, fmt.Errorf("bundle root is missing %s/ or %s/", ReviewerDir, ControlDir)
	}

	reviewerEntries, err := os.ReadDir(filepath.Join(bundleRoot, ReviewerDir))
	if err != nil {
		return nil, err
	}
	if len(reviewerEntries) != len(reviewerAllowedEntries) {
		return nil, fmt.Errorf("%s/ has %d entries, expected exactly %d", ReviewerDir, len(reviewerEntries), len(reviewerAllowedEntries))
	}
	for _, e := range reviewerEntries {
		if !reviewerAllowedEntries[e.Name()] {
			return nil, fmt.Errorf("unexpected reviewer-visible entry %q", e.Name())
		}
		if (e.Name() == SnapshotDir) != e.IsDir() {
			return nil, fmt.Errorf("reviewer entry %q has the wrong type", e.Name())
		}
	}
	controlEntries, err := os.ReadDir(filepath.Join(bundleRoot, ControlDir))
	if err != nil {
		return nil, err
	}
	if len(controlEntries) != len(controlAllowedEntries) {
		return nil, fmt.Errorf("%s/ has %d entries, expected exactly %d", ControlDir, len(controlEntries), len(controlAllowedEntries))
	}
	for _, e := range controlEntries {
		if e.IsDir() || !controlAllowedEntries[e.Name()] {
			return nil, fmt.Errorf("unexpected control entry %q", e.Name())
		}
	}

	// Miner-authored surfaces: the three reviewer entries, whose names the miner
	// chooses, are held to the full lexical heuristic.
	for _, e := range reviewerEntries {
		lower := strings.ToLower(ReviewerDir + "/" + e.Name())
		for _, frag := range oracleShapedSubstrings {
			if strings.Contains(lower, frag) {
				return nil, fmt.Errorf("reviewer entry %q contains %q, which reads as retrospective or outcome material", e.Name(), frag)
			}
		}
	}

	files, err := reviewerFiles(bundleRoot)
	if err != nil {
		return nil, err
	}
	snapshotPrefix := ReviewerDir + "/" + SnapshotDir + "/"
	var warnings []string
	for _, rel := range files {
		for _, seg := range strings.Split(rel, "/") {
			if gitMetadataNames[seg] {
				return nil, fmt.Errorf("%s: path segment %q indicates Git history or worktree leakage", rel, seg)
			}
		}
		if !strings.HasPrefix(rel, snapshotPrefix) {
			continue
		}
		lower := strings.ToLower(rel)
		if snapshotOracleBasenames[strings.ToLower(filepath.Base(rel))] {
			return nil, fmt.Errorf("%s is the filename of an evaluator-only bundle artifact; a source snapshot must not contain one", rel)
		}
		for _, frag := range snapshotOracleSubstrings {
			if strings.Contains(lower, frag) {
				warnings = append(warnings, fmt.Sprintf("%s contains %q; the engine's oracle-name heuristic will flag it, and it needs either a protocol waiver or a report upstream", rel, frag))
				break
			}
		}
	}

	// Checksum agreement, recomputed from disk in both directions.
	recomputed, err := buildChecksums(bundleRoot)
	if err != nil {
		return nil, err
	}
	if string(recomputed) != string(checksums) {
		return nil, fmt.Errorf("%s/%s does not describe exactly the regular files under %s/", ControlDir, ChecksumsFile, ReviewerDir)
	}

	// Pinned schema versions, read back from the published bytes.
	onDiskMeta, err := os.ReadFile(filepath.Join(bundleRoot, ReviewerDir, MetadataFile))
	if err != nil {
		return nil, err
	}
	if string(onDiskMeta) != string(metadata) {
		return nil, fmt.Errorf("%s/%s on disk differs from the validated metadata", ReviewerDir, MetadataFile)
	}
	onDiskManifest, err := os.ReadFile(filepath.Join(bundleRoot, ControlDir, ControlManifestFile))
	if err != nil {
		return nil, err
	}
	if string(onDiskManifest) != string(controlManifest) {
		return nil, fmt.Errorf("%s/%s on disk differs from the validated manifest", ControlDir, ControlManifestFile)
	}
	return warnings, nil
}
