// Package retrospectiveexport materializes retrospective evidence without judging it.
package retrospectiveexport

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"miner/internal/gitx"
)

const SchemaVersion = "1.0.0"

var fullSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

type Options struct {
	Input, Repo, Out string
	PR               int
	ExportedAt       time.Time
}

type signalExport struct {
	SignalStrength string          `json:"signal_strength"`
	SignalIndex    int             `json:"signal_index"`
	SignalID       string          `json:"signal_id"`
	RawSignal      json.RawMessage `json:"raw_signal"`
}

type commitExport struct {
	MaterializationStatus string                     `json:"materialization_status"`
	Error                 string                     `json:"error,omitempty"`
	Metadata              *gitx.ExportCommitMetadata `json:"metadata,omitempty"`
	PatchFile             string                     `json:"patch_file,omitempty"`
	PatchMethod           string                     `json:"patch_method,omitempty"`
	PatchSHA256           string                     `json:"patch_sha256,omitempty"`
	SignalReferences      []string                   `json:"signal_references"`
	RelationshipIndexes   []int                      `json:"commit_relationship_indexes"`
}

type artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	SchemaVersion           string          `json:"schema_version"`
	OriginalPRNumber        int             `json:"original_pr_number"`
	OriginalHeadSHA         string          `json:"original_head_sha"`
	OriginalMergeCommitSHA  string          `json:"original_merge_commit_sha"`
	InputFile               string          `json:"input_file"`
	InputRecordHash         string          `json:"input_record_hash"`
	ExportTimestamp         string          `json:"export_timestamp"`
	ExporterVersion         string          `json:"exporter_version"`
	RepositoryPath          string          `json:"repository_path"`
	RepositoryIdentity      string          `json:"repository_identity,omitempty"`
	RepositoryHEAD          string          `json:"repository_head,omitempty"`
	ObservationEnd          json.RawMessage `json:"observation_end,omitempty"`
	HarvestedAt             json.RawMessage `json:"harvested_at,omitempty"`
	SourceMinerProvenance   json.RawMessage `json:"source_miner_provenance"`
	SignalCounts            map[string]int  `json:"signal_counts"`
	UniqueSourceRefCount    int             `json:"unique_source_ref_count"`
	MaterializedCommitCount int             `json:"materialized_commit_count"`
	MissingCommitCount      int             `json:"missing_commit_count"`
	LogicalPatchIDs         []string        `json:"logical_patch_ids"`
	Command                 string          `json:"command"`
	PatchPolicy             string          `json:"patch_policy"`
	Files                   []artifact      `json:"files"`
}

func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func writeJSON(root, rel string, v any, files *[]artifact) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeArtifact(root, rel, b, files)
}
func writeArtifact(root, rel string, b []byte, files *[]artifact) error {
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(p, b, 0644); err != nil {
		return err
	}
	*files = append(*files, artifact{Path: rel, SHA256: hash(b)})
	return nil
}

func findRecord(path string, pr int) ([]byte, map[string]json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open input: %w", err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1024*1024), 32*1024*1024)
	line := 0
	for s.Scan() {
		line++
		raw := append([]byte(nil), s.Bytes()...)
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, nil, fmt.Errorf("parse JSONL line %d: %w", line, err)
		}
		var original struct {
			Number int `json:"number"`
		}
		if err := json.Unmarshal(obj["original"], &original); err != nil {
			return nil, nil, fmt.Errorf("parse original on line %d: %w", line, err)
		}
		if original.Number == pr {
			return raw, obj, nil
		}
	}
	if err := s.Err(); err != nil {
		return nil, nil, fmt.Errorf("read input: %w", err)
	}
	return nil, nil, fmt.Errorf("PR #%d not found in %q", pr, path)
}

func rawString(obj map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(obj[key], &s)
	return s
}

// Export creates one self-contained retrospective evidence directory.
func Export(ctx context.Context, opt Options) error {
	if opt.PR <= 0 || opt.Input == "" || opt.Repo == "" || opt.Out == "" {
		return fmt.Errorf("input, pr, repo, and out are required")
	}
	raw, root, err := findRecord(opt.Input, opt.PR)
	if err != nil {
		return err
	}
	var original map[string]json.RawMessage
	if err := json.Unmarshal(root["original"], &original); err != nil {
		return err
	}
	var retro map[string]json.RawMessage
	if err := json.Unmarshal(root["retrospective"], &retro); err != nil {
		return fmt.Errorf("parse retrospective: %w", err)
	}
	var provenance map[string]json.RawMessage
	if err := json.Unmarshal(root["provenance"], &provenance); err != nil {
		return fmt.Errorf("parse provenance: %w", err)
	}

	files := []artifact{}
	// Keep the selected JSON object byte-for-byte (apart from the JSONL separator newline).
	if err := writeArtifact(opt.Out, "source_record.json", append(append([]byte(nil), raw...), '\n'), &files); err != nil {
		return err
	}

	strengthKeys := []struct{ strength, key string }{{"strong", "strong_signals"}, {"medium", "medium_signals"}, {"weak", "weak_signals"}}
	all := []signalExport{}
	byStrength := map[string][]signalExport{}
	refs := map[string]bool{}
	commitRefs := map[string]bool{}
	logical := map[string][]signalExport{}
	refSignals := map[string][]string{}
	counts := map[string]int{"strong": 0, "medium": 0, "weak": 0, "total": 0}
	for _, sk := range strengthKeys {
		var signals []json.RawMessage
		if len(retro[sk.key]) > 0 {
			if err := json.Unmarshal(retro[sk.key], &signals); err != nil {
				return fmt.Errorf("parse %s: %w", sk.key, err)
			}
		}
		for i, rawSignal := range signals {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(rawSignal, &obj); err != nil {
				return err
			}
			id := fmt.Sprintf("%s:%d", sk.strength, i)
			entry := signalExport{sk.strength, i, id, rawSignal}
			all = append(all, entry)
			byStrength[sk.strength] = append(byStrength[sk.strength], entry)
			ref := rawString(obj, "source_ref")
			if ref != "" {
				refs[ref] = true
				refKey := ref
				if fullSHA.MatchString(ref) {
					refKey = strings.ToLower(ref)
				}
				refSignals[refKey] = append(refSignals[refKey], id)
			}
			sourceType := strings.ToUpper(rawString(obj, "source_type"))
			if fullSHA.MatchString(ref) && (sourceType == "" || sourceType == "COMMIT") {
				commitRefs[strings.ToLower(ref)] = true
			}
			if id := rawString(obj, "logical_patch_id"); id != "" {
				logical[id] = append(logical[id], entry)
			}
		}
		counts[sk.strength] = len(signals)
		counts["total"] += len(signals)
		if err := writeJSON(opt.Out, "signals/"+sk.strength+".json", byStrength[sk.strength], &files); err != nil {
			return err
		}
	}
	if err := writeJSON(opt.Out, "signals/all.json", all, &files); err != nil {
		return err
	}

	var relationships []json.RawMessage
	if len(retro["commit_relationships"]) > 0 {
		if err := json.Unmarshal(retro["commit_relationships"], &relationships); err != nil {
			return err
		}
	}
	refRels := map[string][]int{}
	for i, rr := range relationships {
		var obj map[string]json.RawMessage
		_ = json.Unmarshal(rr, &obj)
		ref := rawString(obj, "source_ref")
		if ref != "" {
			refs[ref] = true
			refKey := ref
			if fullSHA.MatchString(ref) {
				refKey = strings.ToLower(ref)
			}
			refRels[refKey] = append(refRels[refKey], i)
			if fullSHA.MatchString(ref) {
				commitRefs[strings.ToLower(ref)] = true
			}
		}
	}
	if err := writeJSON(opt.Out, "relationships/commit_relationships.json", relationships, &files); err != nil {
		return err
	}

	repo := gitx.OpenRepository(opt.Repo)
	head, _ := repo.HeadSHA(ctx)
	commitList := make([]string, 0, len(commitRefs))
	for ref := range commitRefs {
		commitList = append(commitList, ref)
	}
	sort.Strings(commitList)
	materialized, missing := 0, 0
	patchAvailable := map[string]bool{}
	for _, ref := range commitList {
		ce := commitExport{SignalReferences: refSignals[ref], RelationshipIndexes: refRels[ref]}
		meta, e := repo.ExportCommit(ctx, ref)
		if e != nil {
			ce.MaterializationStatus = "missing"
			ce.Error = e.Error()
			missing++
		} else {
			materialized++
			ce.MaterializationStatus = "materialized"
			ce.Metadata = meta
			patch, method, pe := repo.ExportPatch(ctx, meta.Commit, meta.Parents)
			if pe != nil {
				ce.MaterializationStatus = "metadata_only"
				ce.Error = pe.Error()
			} else {
				rel := "patches/" + meta.Commit + ".patch"
				if err := writeArtifact(opt.Out, rel, patch, &files); err != nil {
					return err
				}
				ce.PatchFile = rel
				ce.PatchMethod = method
				ce.PatchSHA256 = hash(patch)
				patchAvailable[ref] = true
			}
		}
		if err := writeJSON(opt.Out, "commits/"+ref+".json", ce, &files); err != nil {
			return err
		}
	}

	logicalIDs := make([]string, 0, len(logical))
	for id := range logical {
		logicalIDs = append(logicalIDs, id)
	}
	sort.Strings(logicalIDs)
	for _, id := range logicalIDs {
		sources := map[string]bool{}
		sigIDs := []string{}
		for _, s := range logical[id] {
			sigIDs = append(sigIDs, s.SignalID)
			var obj map[string]json.RawMessage
			_ = json.Unmarshal(s.RawSignal, &obj)
			if ref := rawString(obj, "source_ref"); ref != "" {
				sources[ref] = true
			}
		}
		sourceList := []string{}
		for s := range sources {
			sourceList = append(sourceList, s)
		}
		sort.Strings(sourceList)
		lm := map[string]any{"logical_patch_id": id, "signal_references": sigIDs, "source_refs": sourceList, "commit_metadata_files": prefixedExisting(sourceList, "commits/", ".json", commitRefs), "patch_files": prefixedExisting(sourceList, "patches/", ".patch", patchAvailable)}
		if err := writeJSON(opt.Out, "logical_patches/"+safeComponent(id)+"/manifest.json", lm, &files); err != nil {
			return err
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	exportedAt := opt.ExportedAt
	if exportedAt.IsZero() {
		exportedAt = time.Now().UTC()
	}
	repoIdentity := rawString(original, "repository")
	m := manifest{SchemaVersion: SchemaVersion, OriginalPRNumber: opt.PR, OriginalHeadSHA: rawString(original, "head_sha"), OriginalMergeCommitSHA: rawString(original, "merge_commit_sha"), InputFile: opt.Input, InputRecordHash: hash(raw), ExportTimestamp: exportedAt.UTC().Format(time.RFC3339Nano), ExporterVersion: SchemaVersion, RepositoryPath: opt.Repo, RepositoryIdentity: repoIdentity, RepositoryHEAD: head, ObservationEnd: provenance["observation_end"], HarvestedAt: provenance["harvested_at"], SourceMinerProvenance: root["provenance"], SignalCounts: counts, UniqueSourceRefCount: len(refs), MaterializedCommitCount: materialized, MissingCommitCount: missing, LogicalPatchIDs: logicalIDs, Command: "miner retrospective-export --input <jsonl> --pr <number> --repo <repository> --out <directory>", PatchPolicy: "ordinary/root: git show patch; merge: first-parent git diff; binary and full-index enabled", Files: files}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(opt.Out, "manifest.json"), append(b, '\n'), 0644)
}

func prefixedExisting(refs []string, prefix, suffix string, commits map[string]bool) []string {
	out := []string{}
	for _, ref := range refs {
		if commits[strings.ToLower(ref)] {
			out = append(out, prefix+strings.ToLower(ref)+suffix)
		}
	}
	return out
}

func safeComponent(value string) string {
	if fullSHA.MatchString(value) {
		return strings.ToLower(value)
	}
	return "id-sha256-" + hash([]byte(value))
}
