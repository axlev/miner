package heuristics

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"miner/internal/config"
	"miner/internal/model"
)

// Engine runs the pre-merge stateful/distributed heuristic evaluation.
type Engine struct {
	cfg         *config.HeuristicsConfig
	pathRegexes []compiledPathRule
	kwRegexes   []compiledKeywordRule
}

type compiledPathRule struct {
	rule  config.PathRule
	regex *regexp.Regexp
}

type compiledKeywordRule struct {
	rule  config.KeywordRule
	regex *regexp.Regexp
}

// NewEngine compiles and initializes the heuristic engine.
func NewEngine(cfg *config.HeuristicsConfig) (*Engine, error) {
	var pathRules []compiledPathRule
	for _, r := range cfg.PathRules {
		rx, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid path rule regex %q (%s): %w", r.Pattern, r.Name, err)
		}
		pathRules = append(pathRules, compiledPathRule{rule: r, regex: rx})
	}

	var kwRules []compiledKeywordRule
	for _, r := range cfg.KeywordRules {
		rx, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid keyword rule regex %q (%s): %w", r.Pattern, r.Name, err)
		}
		kwRules = append(kwRules, compiledKeywordRule{rule: r, regex: rx})
	}

	return &Engine{
		cfg:         cfg,
		pathRegexes: pathRules,
		kwRegexes:   kwRules,
	}, nil
}

// Evaluate performs deterministic scoring on an OriginalPR (pre-merge facts only).
func (e *Engine) Evaluate(orig *model.OriginalPR) model.StatefulEvaluation {
	var matchedPathRules []model.MatchedRule
	var matchedKwRules []model.MatchedRule
	matchedSymbolsMap := make(map[string]bool)
	var rawScore float64

	// 1. Evaluate Path Rules against changed files
	for _, pr := range e.pathRegexes {
		for _, f := range orig.ChangedFiles {
			if pr.regex.MatchString(f.Path) {
				matchedPathRules = append(matchedPathRules, model.MatchedRule{
					RuleName: pr.rule.Name,
					Pattern:  pr.rule.Pattern,
					Weight:   pr.rule.Weight,
					Location: "path",
					Matched:  f.Path,
				})
				rawScore += pr.rule.Weight
				break // Match at most once per rule across files
			}
		}
	}

	// 2. Evaluate Keyword Rules against allowed fields
	for _, kr := range e.kwRegexes {
		matchedThisRule := false
		for _, field := range kr.rule.MatchFields {
			if matchedThisRule {
				break
			}
			switch field {
			case "pr_title":
				if match := kr.regex.FindString(orig.Title); match != "" {
					matchedKwRules = append(matchedKwRules, model.MatchedRule{
						RuleName: kr.rule.Name,
						Pattern:  kr.rule.Pattern,
						Weight:   kr.rule.Weight,
						Location: "title",
						Matched:  match,
					})
					rawScore += kr.rule.Weight
					matchedThisRule = true
				}
			case "pr_body":
				if match := kr.regex.FindString(orig.Body); match != "" {
					matchedKwRules = append(matchedKwRules, model.MatchedRule{
						RuleName: kr.rule.Name,
						Pattern:  kr.rule.Pattern,
						Weight:   kr.rule.Weight,
						Location: "body",
						Matched:  match,
					})
					rawScore += kr.rule.Weight
					matchedThisRule = true
				}
			case "diff_symbols":
				for _, sym := range orig.ChangedFunctions {
					if match := kr.regex.FindString(sym); match != "" {
						matchedSymbolsMap[sym] = true
						if !matchedThisRule {
							matchedKwRules = append(matchedKwRules, model.MatchedRule{
								RuleName: kr.rule.Name,
								Pattern:  kr.rule.Pattern,
								Weight:   kr.rule.Weight,
								Location: "symbol",
								Matched:  sym,
							})
							rawScore += kr.rule.Weight
							matchedThisRule = true
						}
					}
				}
			case "commit_messages":
				for _, msg := range orig.CommitMessages {
					if match := kr.regex.FindString(msg); match != "" {
						matchedKwRules = append(matchedKwRules, model.MatchedRule{
							RuleName: kr.rule.Name,
							Pattern:  kr.rule.Pattern,
							Weight:   kr.rule.Weight,
							Location: "commit_msg",
							Matched:  match,
						})
						rawScore += kr.rule.Weight
						matchedThisRule = true
						break
					}
				}
			}
		}
	}

	// 3. Normalization and categorization
	score := rawScore
	if score > e.cfg.Scoring.MaxCap {
		score = e.cfg.Scoring.MaxCap
	}

	category := "LOW"
	if score >= e.cfg.Scoring.HighThreshold {
		category = "HIGH"
	} else if score >= e.cfg.Scoring.MediumThreshold {
		category = "MEDIUM"
	}

	var matchedSymbols []string
	for s := range matchedSymbolsMap {
		matchedSymbols = append(matchedSymbols, s)
	}
	sort.Strings(matchedSymbols)

	// Construct human-readable explanation
	var explanationParts []string
	for _, r := range matchedPathRules {
		explanationParts = append(explanationParts, fmt.Sprintf("path:%s (+%.2f)", r.RuleName, r.Weight))
	}
	for _, r := range matchedKwRules {
		explanationParts = append(explanationParts, fmt.Sprintf("%s:%s (+%.2f)", r.Location, r.RuleName, r.Weight))
	}

	explanation := strings.Join(explanationParts, ", ")
	if explanation == "" {
		explanation = "No stateful or distributed rules matched"
	}

	return model.StatefulEvaluation{
		Score:               score,
		RawPoints:           rawScore,
		Category:            category,
		MatchedPathRules:    matchedPathRules,
		MatchedKeywordRules: matchedKwRules,
		MatchedSymbols:      matchedSymbols,
		Explanation:         explanation,
	}
}
