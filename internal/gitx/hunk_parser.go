package gitx

import (
	"regexp"
	"sort"
	"strings"
)

var (
	// regex to capture function names in hunk headers: @@ -10,5 +10,8 @@ return_type function_name(...)
	hunkHeaderFuncRegex = regexp.MustCompile(`^@@\s+-\d+(?:,\d+)?\s+\+\d+(?:,\d+)?\s+@@\s*(.*)$`)

	// regex to extract C function identifier from a declaration/definition or hunk header line
	// e.g. "int bgp_fsm_change_status(struct peer *peer, int status)" -> "bgp_fsm_change_status"
	cFuncIdentRegex = regexp.MustCompile(`\b([a-zA-Z_][a-zA-Z0-9_]*)\s*\([^;]*\)?`)

	// regex to match C function definition start on added lines
	cDefRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_* \t]+\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*\([^;]*\)`)
)

// ExtractHunkSymbols parses a unified diff string and returns deduplicated C function/symbol names.
func ExtractHunkSymbols(diffText string) []string {
	symbolMap := make(map[string]bool)
	lines := strings.Split(diffText, "\n")

	for _, line := range lines {
		// 1. Check hunk header line
		if strings.HasPrefix(line, "@@") {
			matches := hunkHeaderFuncRegex.FindStringSubmatch(line)
			if len(matches) > 1 {
				headerContext := strings.TrimSpace(matches[1])
				if headerContext != "" {
					funcName := extractCFunctionName(headerContext)
					if funcName != "" && isValidCSymbol(funcName) {
						symbolMap[funcName] = true
					}
				}
			}
			continue
		}

		// 2. Check added lines for new function definitions
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			trimmed := strings.TrimSpace(line[1:])
			if matches := cDefRegex.FindStringSubmatch(trimmed); len(matches) > 1 {
				funcName := matches[1]
				if isValidCSymbol(funcName) {
					symbolMap[funcName] = true
				}
			}
		}
	}

	var symbols []string
	for s := range symbolMap {
		symbols = append(symbols, s)
	}
	sort.Strings(symbols)
	return symbols
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
	// Filter common C keywords / macros that appear before parens
	switch name {
		case "if", "for", "while", "switch", "return", "sizeof", "typeof",
			"DEFINE_HOOK", "DECLARE_HOOK", "HOOK_REGISTER", "MEMTYPE_DECLARE":
		return false
	}
	return true
}
