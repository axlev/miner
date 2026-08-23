package gitx

import (
	"regexp"
	"sort"
	"strings"

	"pr-analysis/internal/model"
)

var (
	diffGitRegex        = regexp.MustCompile(`^diff --git a/(\S+) b/(\S+)`)
	diffPlusRegex       = regexp.MustCompile(`^\+\+\+ b/(\S+)`)
	hunkHeaderFuncRegex = regexp.MustCompile(`^@@\s+-\d+(?:,\d+)?\s+\+\d+(?:,\d+)?\s+@@\s*(.*)$`)
	cFuncIdentRegex     = regexp.MustCompile(`\b([a-zA-Z_][a-zA-Z0-9_]*)\s*\([^;]*\)?`)
	cDefRegex           = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_* \t]+\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*\([^;]*\)`)
)

// ParsedDiffSymbols contains both flat and path-associated function symbols.
type ParsedDiffSymbols struct {
	Symbols           []string
	FunctionLocations []model.ChangedFunctionLocation
	PathSymbols       map[string][]string
}

// ExtractHunkSymbols parses a unified diff string and returns deduplicated C function/symbol names (backward compatible).
func ExtractHunkSymbols(diffText string) []string {
	return ExtractFileHunkSymbols(diffText).Symbols
}

// ExtractFileHunkSymbols parses a unified diff string and returns language-aware C symbols with file paths.
func ExtractFileHunkSymbols(diffText string) ParsedDiffSymbols {
	symbolMap := make(map[string]bool)
	locationMap := make(map[string]map[string]bool) // path -> func -> bool

	diffText = strings.ReplaceAll(diffText, "\r\n", "\n")
	diffText = strings.ReplaceAll(diffText, "\r", "\n")
	lines := strings.Split(diffText, "\n")
	curFile := ""
	isCFile := false

	for idx, line := range lines {
		// Track current file path
		if strings.HasPrefix(line, "diff --git") {
			if m := diffGitRegex.FindStringSubmatch(line); len(m) > 2 {
				curFile = m[2]
				isCFile = strings.HasSuffix(curFile, ".c") || strings.HasSuffix(curFile, ".h")
			}
			continue
		} else if strings.HasPrefix(line, "+++ b/") {
			if m := diffPlusRegex.FindStringSubmatch(line); len(m) > 1 {
				curFile = m[1]
				isCFile = strings.HasSuffix(curFile, ".c") || strings.HasSuffix(curFile, ".h")
			}
			continue
		}

		// Only parse C symbols from C source and header files (.c / .h)
		if !isCFile || curFile == "" {
			continue
		}

		// 1. Check hunk header line
		if strings.HasPrefix(line, "@@") {
			matches := hunkHeaderFuncRegex.FindStringSubmatch(line)
			if len(matches) > 1 {
				headerContext := strings.TrimSpace(matches[1])
				if headerContext != "" {
					funcName := extractCFunctionName(headerContext)
					if funcName != "" && isValidCSymbol(funcName) {
						symbolMap[funcName] = true
						if locationMap[curFile] == nil {
							locationMap[curFile] = make(map[string]bool)
						}
						locationMap[curFile][funcName] = true
					}
				}
			}
			continue
		}

		// 2. Check added lines for new function definitions with strict definition evidence
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			if funcName := extractAddedFunctionDefinition(lines, idx); funcName != "" {
				symbolMap[funcName] = true
				if locationMap[curFile] == nil {
					locationMap[curFile] = make(map[string]bool)
				}
				locationMap[curFile][funcName] = true
			}
		}
	}

	var symbols []string
	for s := range symbolMap {
		symbols = append(symbols, s)
	}
	sort.Strings(symbols)

	pathSymbols := make(map[string][]string)
	var locations []model.ChangedFunctionLocation

	// Sort paths for deterministic output
	var paths []string
	for p := range locationMap {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		var funcs []string
		for f := range locationMap[p] {
			funcs = append(funcs, f)
		}
		sort.Strings(funcs)
		pathSymbols[p] = funcs
		for _, f := range funcs {
			locations = append(locations, model.ChangedFunctionLocation{
				Path:     p,
				Function: f,
			})
		}
	}

	return ParsedDiffSymbols{
		Symbols:           symbols,
		FunctionLocations: locations,
		PathSymbols:       pathSymbols,
	}
}

func extractAddedFunctionDefinition(lines []string, i int) string {
	line := lines[i]
	if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
		return ""
	}
	trimmed := strings.TrimSpace(line[1:])
	if trimmed == "" {
		return ""
	}

	// Reject comments
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
		return ""
	}

	// Must NOT contain semicolon (rejects function calls, prototype declarations, return calls)
	if strings.Contains(trimmed, ";") {
		return ""
	}

	// Must NOT contain assignment (rejects value = helper_call(arg))
	if strings.Contains(trimmed, "=") {
		return ""
	}

	// Must NOT start with control flow keywords, return, or labels
	for _, kw := range []string{"return", "if", "for", "while", "switch", "case", "else", "goto", "break", "continue", "typedef"} {
		if strings.HasPrefix(trimmed, kw+" ") || strings.HasPrefix(trimmed, kw+"(") || trimmed == kw {
			return ""
		}
	}

	// Match C function signature candidate: return_type func_name(params...)
	matches := cDefRegex.FindStringSubmatch(trimmed)
	if len(matches) < 2 {
		return ""
	}
	funcName := matches[1]
	if !isValidCSymbol(funcName) {
		return ""
	}

	// Verify that it is followed by or ends with opening brace '{'
	if strings.HasSuffix(trimmed, "{") {
		return funcName
	}

	// Check subsequent added or context lines (up to 3 lines) for opening brace '{'
	for k := i + 1; k < len(lines) && k <= i+3; k++ {
		nextTrimmed := strings.TrimSpace(lines[k])
		if nextTrimmed == "" {
			continue
		}
		if strings.HasPrefix(nextTrimmed, "@@") || strings.HasPrefix(nextTrimmed, "diff ") {
			break
		}
		if strings.HasPrefix(nextTrimmed, "+") {
			nextTrimmed = strings.TrimSpace(nextTrimmed[1:])
		}
		if strings.HasPrefix(nextTrimmed, "{") {
			return funcName
		}
		// If another statement or semicolon is encountered before '{', reject
		if strings.Contains(nextTrimmed, ";") {
			break
		}
	}

	return ""
}

func extractCFunctionName(signature string) string {
	// Remove comments if any
	if idx := strings.Index(signature, "/*"); idx != -1 {
		signature = signature[:idx]
	}
	if idx := strings.Index(signature, "//"); idx != -1 {
		signature = signature[:idx]
	}
	signature = strings.TrimSpace(signature)

	// If it has parentheses, extract identifier right before '('
	if idx := strings.Index(signature, "("); idx != -1 {
		prefix := strings.TrimSpace(signature[:idx])
		parts := strings.Fields(prefix)
		if len(parts) > 0 {
			last := parts[len(parts)-1]
			// Trim pointers or address symbols e.g. *func_name
			last = strings.TrimLeft(last, "*&")
			return last
		}
	}

	// Fallback to regex identifier match
	if matches := cFuncIdentRegex.FindStringSubmatch(signature); len(matches) > 1 {
		return matches[1]
	}

	return ""
}

func isValidCSymbol(name string) bool {
	if len(name) < 2 {
		return false
	}
	// Filter common C keywords / macros / scaffolding / main
	switch name {
	case "if", "for", "while", "switch", "return", "sizeof", "typeof",
		"struct", "union", "enum", "typedef", "else", "do", "case", "default",
		"main",
		"DEFUN", "DEFUN_NOSH", "DEFUN_HIDDEN", "ALIAS", "INSTALL_ELEMENT",
		"DEFPY", "DEFINE_HOOK", "DECLARE_HOOK", "HOOK_REGISTER", "MEMTYPE_DECLARE":
		return false
	}
	return true
}
