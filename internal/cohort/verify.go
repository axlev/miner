package cohort

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"miner/internal/model"
	"miner/internal/prospectiveexport"
)

// CaseResult records what happened to one shortlisted PR.
type CaseResult struct {
	PR       int
	CaseID   string
	Cutoff   time.Time
	Exported bool
	// Validated is true only when the engine's own boundary validator ran and passed.
	// It stays false when validation was not run at all, so "not checked" can never be
	// mistaken for "checked and clean".
	Validated     bool
	ValidationRun bool
	BundlePath    string
	Err           string
	// BenchCommand is the exact invocation that validates this bundle, always
	// populated so it can be run by hand when the engine checkout is not wired in.
	BenchCommand string
}

// OK reports whether this case is fit for a cohort: exported cleanly, and either
// validated by the engine or not yet submitted to it.
func (c CaseResult) OK() bool { return c.Exported && c.Err == "" && (!c.ValidationRun || c.Validated) }

// VerifyOptions configures a cohort verification pass.
type VerifyOptions struct {
	Records []model.PRCandidateRecord
	PRs     []int
	// Repo is the local Git clone the snapshot and diff are built from.
	Repo string
	// CacheDir is the collect-time cache root; per-PR bundles are resolved beneath it
	// as <CacheDir>/github/<owner>_<repo>/prs/pr_<n>.json.
	CacheDir string
	// OutDir receives one bundle per case, plus the evaluator-only roots.
	OutDir string
	// Cutoff pins every case to one instant. When zero, each case uses its own
	// merged_at, which is the reviewer-at-merge view the benchmark is built around.
	Cutoff time.Time
	// BenchRepo is the engine-runner checkout. When empty, validation is not run and
	// the command is reported instead — the engine is a separate module and this repo
	// does not assume a sibling checkout exists.
	BenchRepo string
}

// cachePath derives the collect-time cache location for one PR.
func cachePath(cacheDir, repository string, pr int) string {
	owner, name, _ := strings.Cut(repository, "/")
	return filepath.Join(cacheDir, "github", owner+"_"+name, "prs", fmt.Sprintf("pr_%d.json", pr))
}

// Verify exports each shortlisted case for real and, when an engine checkout is
// supplied, submits each resulting bundle to the engine's own boundary validator.
//
// This exists because the selection report's exportability column is a prediction.
// A case that looks fine there can still fail on something the prediction cannot
// see, and discovering that after a cohort is frozen — or worse, after a paid run
// has started — is the failure this pass is meant to prevent.
func Verify(ctx context.Context, opt VerifyOptions) ([]CaseResult, error) {
	if opt.Repo == "" || opt.CacheDir == "" || opt.OutDir == "" {
		return nil, fmt.Errorf("repo, cache-dir, and out are required")
	}
	byPR := map[int]model.PRCandidateRecord{}
	for _, rec := range opt.Records {
		byPR[rec.Original.Number] = rec
	}
	// The correlated input is written to a single temporary file once and reused for
	// every case, so the exporter sees exactly the records it would see in production.
	inputPath := filepath.Join(opt.OutDir, "cohort-input.jsonl")
	if err := os.MkdirAll(opt.OutDir, 0755); err != nil {
		return nil, err
	}
	if err := writeRecords(inputPath, opt.Records); err != nil {
		return nil, err
	}

	var results []CaseResult
	for _, pr := range opt.PRs {
		res := CaseResult{PR: pr}
		rec, ok := byPR[pr]
		if !ok {
			res.Err = fmt.Sprintf("PR #%d is not present in the input", pr)
			results = append(results, res)
			continue
		}
		cutoff := opt.Cutoff
		if cutoff.IsZero() {
			cutoff = rec.Original.MergedAt
		}
		if cutoff.IsZero() {
			res.Err = "no --cutoff given and the record has no merged_at to fall back on"
			results = append(results, res)
			continue
		}
		res.Cutoff = cutoff.UTC()
		res.CaseID = prospectiveexport.GenerateCaseID(rec.Original.Repository, pr, cutoff)
		bundle := filepath.Join(opt.OutDir, res.CaseID)
		res.BundlePath = bundle
		// Printed for a human to run from the engine checkout, so the bundle path must
		// be absolute for the same reason runBench absolutises it.
		absBundle := bundle
		if a, err := filepath.Abs(bundle); err == nil {
			absBundle = a
		}
		res.BenchCommand = fmt.Sprintf("go run ./cmd/bench -bundle %s -case-id %s", absBundle, res.CaseID)

		err := prospectiveexport.Export(ctx, prospectiveexport.Options{
			CorrelatedInput: inputPath,
			CacheFile:       cachePath(opt.CacheDir, rec.Original.Repository, pr),
			Repo:            opt.Repo,
			PR:              pr,
			Cutoff:          cutoff,
			ProspectiveOut:  bundle,
			EvaluatorOut:    filepath.Join(opt.OutDir, res.CaseID+"-evaluator-only"),
		})
		if err != nil {
			res.Err = firstLine(err.Error())
			results = append(results, res)
			continue
		}
		res.Exported = true

		if opt.BenchRepo != "" {
			res.ValidationRun = true
			if err := runBench(ctx, opt.BenchRepo, bundle, res.CaseID, opt.OutDir); err != nil {
				res.Err = firstLine(err.Error())
			} else {
				res.Validated = true
			}
		}
		results = append(results, res)
	}
	return results, nil
}

func writeRecords(path string, records []model.PRCandidateRecord) error {
	var buf bytes.Buffer
	for _, rec := range records {
		b, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// runBench invokes the engine's own validator. The engine is a separate Go module
// and its validator lives under internal/, which Go forbids importing across module
// boundaries — so shelling out is the only mechanism available, not a shortcut.
//
// Every path handed to bench is made absolute first. The command runs with its working
// directory set to the engine checkout, so a relative path would resolve against that
// repository instead of this one: bench would fail to find the bundle and would write
// its results into the engine's tree. Both happened before this was fixed.
func runBench(ctx context.Context, benchRepo, bundle, caseID, outDir string) error {
	absBundle, err := filepath.Abs(bundle)
	if err != nil {
		return fmt.Errorf("resolve bundle path: %w", err)
	}
	resultsRoot, err := filepath.Abs(filepath.Join(outDir, "bench-results"))
	if err != nil {
		return fmt.Errorf("resolve results root: %w", err)
	}
	workspaceRoot, err := filepath.Abs(filepath.Join(outDir, "bench-workspace"))
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	cmd := exec.CommandContext(ctx, "go", "run", "./cmd/bench",
		"-bundle", absBundle, "-case-id", caseID,
		"-results-root", resultsRoot,
		"-workspace-root", workspaceRoot)
	cmd.Dir = benchRepo
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	// A run can fail well past boundary validation (no fixture scenario, no adapter,
	// no credentials). Only a boundary rejection disqualifies a case here, so the
	// validator's own report is what gets consulted, not the process exit code.
	if report, rerr := findValidationReport(resultsRoot, caseID); rerr == nil {
		if report.Result == "pass" {
			return nil
		}
		var parts []string
		for _, v := range report.Violations {
			if v.Path != "" {
				parts = append(parts, fmt.Sprintf("%s (%s)", v.Rule, v.Path))
				continue
			}
			parts = append(parts, v.Rule)
		}
		return fmt.Errorf("boundary validation failed: %s", strings.Join(parts, "; "))
	}
	return fmt.Errorf("bench did not produce a boundary-validation report: %s", firstLine(strings.TrimSpace(string(out))))
}

// validationReport mirrors only the fields needed to read a verdict. It is
// deliberately a loose, additive-tolerant view of the engine's schema: this repo
// consumes that report, it does not define it.
type validationReport struct {
	Result     string `json:"result"`
	Violations []struct {
		Rule   string `json:"rule"`
		Path   string `json:"path"`
		Detail string `json:"detail"`
	} `json:"violations"`
}

func findValidationReport(resultsRoot, caseID string) (validationReport, error) {
	var found []string
	_ = filepath.Walk(resultsRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Base(p) != "boundary-validation.json" {
			return nil
		}
		if strings.Contains(p, caseID) {
			found = append(found, p)
		}
		return nil
	})
	if len(found) == 0 {
		return validationReport{}, fmt.Errorf("no boundary-validation.json for %s", caseID)
	}
	// Most recent run wins if a case was verified more than once.
	sort.Strings(found)
	raw, err := os.ReadFile(found[len(found)-1])
	if err != nil {
		return validationReport{}, err
	}
	var rep validationReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		return validationReport{}, err
	}
	return rep, nil
}

// RenderVerification summarizes a verification pass for a human deciding whether
// the cohort is ready.
func RenderVerification(results []CaseResult, validationRun bool) string {
	var sb strings.Builder
	sb.WriteString("# Cohort verification\n\n")
	pass, fail := 0, 0
	for _, r := range results {
		if r.OK() {
			pass++
		} else {
			fail++
		}
	}
	fmt.Fprintf(&sb, "**%d of %d cases usable**", pass, len(results))
	if fail > 0 {
		fmt.Fprintf(&sb, " — %d blocked", fail)
	}
	sb.WriteString("  \n")
	if validationRun {
		sb.WriteString("**Boundary validation**: run against the engine's own validator.  \n\n")
	} else {
		sb.WriteString("**Boundary validation**: NOT run. Each case below reports the exact command;\n")
		sb.WriteString("a case is not cohort-ready until its bundle passes the engine's validator.  \n\n")
	}

	sb.WriteString("| PR | Case ID | Cutoff | Exported | Validated | Notes |\n")
	sb.WriteString("| ---: | :--- | :--- | :--- | :--- | :--- |\n")
	for _, r := range results {
		exported := "yes"
		if !r.Exported {
			exported = "**no**"
		}
		validated := "not run"
		switch {
		case r.Validated:
			validated = "pass"
		case r.ValidationRun:
			validated = "**FAIL**"
		}
		notes := r.Err
		cutoff := ""
		if !r.Cutoff.IsZero() {
			cutoff = r.Cutoff.Format(time.RFC3339)
		}
		fmt.Fprintf(&sb, "| %d | `%s` | %s | %s | %s | %s |\n",
			r.PR, r.CaseID, cutoff, exported, validated, truncate(notes, 100))
	}

	if !validationRun {
		sb.WriteString("\n## Validate these bundles\n\nFrom the engine-runner checkout:\n\n```bash\n")
		for _, r := range results {
			if r.Exported {
				fmt.Fprintf(&sb, "%s\n", r.BenchCommand)
			}
		}
		sb.WriteString("```\n")
	}
	return sb.String()
}
