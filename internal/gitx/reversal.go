package gitx

import (
	"sort"
	"strings"

	"pr-analysis/internal/model"
)

// NormalizeSubstantiveLine normalizes a source code line and filters out low-information structural lines.
func NormalizeSubstantiveLine(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false
	}

	// Filter common single-brace, delimiter, or low-information structural lines
	switch line {
	case "{", "}", "};", "()", "{}", "[]", "(", ")", ";", "else", "else {":
		return "", false
	}

	// Filter whole-line comment markers
	if strings.HasPrefix(line, "//") || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "*/") {
		return "", false
	}

	// Filter lines with trimmed length < 3
	if len(line) < 3 {
		return "", false
	}

	return line, true
}

// ExtractSubstantivePatchLines extracts substantive added and removed lines per file path from a unified diff.
func ExtractSubstantivePatchLines(diffText string) (added map[string][]string, removed map[string][]string) {
	added = make(map[string][]string)
	removed = make(map[string][]string)

	diffText = strings.ReplaceAll(diffText, "\r\n", "\n")
	diffText = strings.ReplaceAll(diffText, "\r", "\n")
	lines := strings.Split(diffText, "\n")

	curPath := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			if m := diffGitOldPathRegex.FindStringSubmatch(line); len(m) > 2 {
				curPath = m[2] // destination path
			}
			continue
		} else if strings.HasPrefix(line, "+++ b/") {
			if m := diffPlusRegex.FindStringSubmatch(line); len(m) > 1 {
				curPath = m[1]
			}
			continue
		}

		if curPath == "" {
			continue
		}

		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			if norm, ok := NormalizeSubstantiveLine(line[1:]); ok {
				added[curPath] = append(added[curPath], norm)
			}
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			if norm, ok := NormalizeSubstantiveLine(line[1:]); ok {
				removed[curPath] = append(removed[curPath], norm)
			}
		}
	}

	return added, removed
}

// ComputeReversalEvidence computes path-scoped substantive multiset overlap and reversal classification.
func ComputeReversalEvidence(
	origDiffText string,
	laterDiffText string,
	fullReversePatchMatch bool,
	blamedRemovedLines []RemovedLineLocation,
) model.ReversalEvidence {
	origAdds, origDels := ExtractSubstantivePatchLines(origDiffText)
	laterAdds, laterDels := ExtractSubstantivePatchLines(laterDiffText)

	// Build index of blamed removed lines by path:content
	blamedNormMap := make(map[string]map[string]int) // path -> normLine -> count
	for _, r := range blamedRemovedLines {
		if norm, ok := NormalizeSubstantiveLine(r.Content); ok {
			if blamedNormMap[r.Path] == nil {
				blamedNormMap[r.Path] = make(map[string]int)
			}
			blamedNormMap[r.Path][norm]++
		}
	}

	// Collect union of all paths
	pathMap := make(map[string]bool)
	for p := range origAdds {
		pathMap[p] = true
	}
	for p := range origDels {
		pathMap[p] = true
	}
	for p := range laterAdds {
		pathMap[p] = true
	}
	for p := range laterDels {
		pathMap[p] = true
	}

	var paths []string
	for p := range pathMap {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var pathOverlaps []model.PathReversalOverlap
	totalOrigAdds := 0
	totalOrigDels := 0
	totalLaterRemovedMatchingAdds := 0
	totalLaterAddedMatchingDels := 0
	totalLineageSupportedOverlap := 0
	hasMultiLinePathOverlap := false
	hasLineageSupportedOverlap := false

	for _, p := range paths {
		origAddList := origAdds[p]
		origDelList := origDels[p]
		laterAddList := laterAdds[p]
		laterDelList := laterDels[p]

		origAddCount := len(origAddList)
		origDelCount := len(origDelList)
		totalOrigAdds += origAddCount
		totalOrigDels += origDelCount

		// Multiset frequency maps
		origAddFreq := make(map[string]int)
		for _, l := range origAddList {
			origAddFreq[l]++
		}

		origDelFreq := make(map[string]int)
		for _, l := range origDelList {
			origDelFreq[l]++
		}

		laterDelFreq := make(map[string]int)
		for _, l := range laterDelList {
			laterDelFreq[l]++
		}

		laterAddFreq := make(map[string]int)
		for _, l := range laterAddList {
			laterAddFreq[l]++
		}

		// 1. Later removals matching original additions (in this path)
		pathRemovedOverlap := 0
		for normLine, cLater := range laterDelFreq {
			if cOrig := origAddFreq[normLine]; cOrig > 0 {
				if cLater < cOrig {
					pathRemovedOverlap += cLater
				} else {
					pathRemovedOverlap += cOrig
				}
			}
		}

		// 2. Later additions matching original removals (in this path)
		pathAddedOverlap := 0
		for normLine, cLater := range laterAddFreq {
			if cOrig := origDelFreq[normLine]; cOrig > 0 {
				if cLater < cOrig {
					pathAddedOverlap += cLater
				} else {
					pathAddedOverlap += cOrig
				}
			}
		}

		// 3. Lineage-supported removed overlap in this path
		pathLineageOverlap := 0
		if pathBlamed := blamedNormMap[p]; pathBlamed != nil {
			for normLine, cBlamed := range pathBlamed {
				if cOrig := origAddFreq[normLine]; cOrig > 0 {
					if cBlamed < cOrig {
						pathLineageOverlap += cBlamed
					} else {
						pathLineageOverlap += cOrig
					}
				}
			}
		}

		totalLaterRemovedMatchingAdds += pathRemovedOverlap
		totalLaterAddedMatchingDels += pathAddedOverlap
		totalLineageSupportedOverlap += pathLineageOverlap

		if (pathRemovedOverlap + pathAddedOverlap) >= 2 {
			hasMultiLinePathOverlap = true
		}
		if pathLineageOverlap >= 1 {
			hasLineageSupportedOverlap = true
		}

		if origAddCount > 0 || origDelCount > 0 || len(laterAddList) > 0 || len(laterDelList) > 0 {
			pathOverlaps = append(pathOverlaps, model.PathReversalOverlap{
				Path:                                  p,
				OriginalAddedLines:                    origAddCount,
				OriginalRemovedLines:                  origDelCount,
				LaterRemovedMatchingOriginalAdditions: pathRemovedOverlap,
				LaterAddedMatchingOriginalRemovals:    pathAddedOverlap,
				LineageSupportedRemovedOverlapCount:   pathLineageOverlap,
			})
		}
	}

	totalOrigSubstantive := totalOrigAdds + totalOrigDels
	reversedLineOverlapCount := totalLaterRemovedMatchingAdds + totalLaterAddedMatchingDels
	reversedLineOverlapFraction := 0.0
	if totalOrigSubstantive > 0 {
		reversedLineOverlapFraction = float64(reversedLineOverlapCount) / float64(totalOrigSubstantive)
	}

	// Classify ReversalType
	reversalType := "none"
	if fullReversePatchMatch {
		reversalType = "full"
	} else if hasMultiLinePathOverlap || hasLineageSupportedOverlap {
		reversalType = "partial_candidate"
	}

	return model.ReversalEvidence{
		FullReversePatchMatch:                      fullReversePatchMatch,
		ReversalType:                               reversalType,
		OriginalAddedLines:                         totalOrigAdds,
		OriginalRemovedLines:                       totalOrigDels,
		TotalOriginalSubstantiveLines:              totalOrigSubstantive,
		LaterRemovedLinesMatchingOriginalAdditions: totalLaterRemovedMatchingAdds,
		LaterAddedLinesMatchingOriginalRemovals:    totalLaterAddedMatchingDels,
		LineageSupportedRemovedOverlapCount:        totalLineageSupportedOverlap,
		ReversedLineOverlapCount:                   reversedLineOverlapCount,
		ReversedLineOverlapFraction:                reversedLineOverlapFraction,
		PathOverlaps:                               pathOverlaps,
		Method:                                     "path_scoped_substantive_line_multiset",
	}
}
