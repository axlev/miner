package storage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"pr-analysis/internal/model"
)

// WriteJSONL writes candidate records to a JSONL file with deterministic sorting by PR number.
func WriteJSONL(filePath string, records []model.PRCandidateRecord) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return fmt.Errorf("failed to create directory for %q: %w", filePath, err)
	}

	// Deterministic sort by PR Number ascending
	sorted := make([]model.PRCandidateRecord, len(records))
	copy(sorted, records)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Original.Number < sorted[j].Original.Number
	})

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create JSONL file %q: %w", filePath, err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	for _, rec := range sorted {
		data, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("failed to marshal record for PR #%d: %w", rec.Original.Number, err)
		}
		if _, err := writer.Write(append(data, '\n')); err != nil {
			return fmt.Errorf("failed to write record for PR #%d: %w", rec.Original.Number, err)
		}
	}

	return writer.Flush()
}

// ReadJSONL reads all PRCandidateRecord lines from a JSONL file.
func ReadJSONL(filePath string) ([]model.PRCandidateRecord, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open JSONL file %q: %w", filePath, err)
	}
	defer file.Close()

	var records []model.PRCandidateRecord
	scanner := bufio.NewScanner(file)
	// Buffer size up to 10MB per line for large diff records if needed
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var rec model.PRCandidateRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, fmt.Errorf("failed to parse JSONL line %d in %q: %w", lineNum, filePath, err)
		}
		records = append(records, rec)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading JSONL file %q: %w", filePath, err)
	}

	return records, nil
}

// FindPRInJSONL searches a JSONL file line-by-line and returns immediately upon finding the PR number.
func FindPRInJSONL(filePath string, prNumber int) (*model.PRCandidateRecord, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open %q: %w", filePath, err)
	}
	defer file.Close()

	targetSubstr := fmt.Sprintf(`"number":%d`, prNumber)
	targetSubstrSpaced := fmt.Sprintf(`"number": %d`, prNumber)

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 32*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		lineStr := string(line)
		if strings.Contains(lineStr, targetSubstr) || strings.Contains(lineStr, targetSubstrSpaced) {
			var rec model.PRCandidateRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				return nil, fmt.Errorf("failed to unmarshal JSON line: %w", err)
			}
			if rec.Original.Number == prNumber {
				return &rec, nil
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading %q: %w", filePath, err)
	}

	return nil, fmt.Errorf("PR #%d not found in %q", prNumber, filePath)
}
