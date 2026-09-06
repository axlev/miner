package storage

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"miner/internal/model"
)

// WriteMarkdownTable exports candidate records as a human-readable GitHub-flavored Markdown table.
func WriteMarkdownTable(filePath string, records []model.PRCandidateRecord) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return fmt.Errorf("failed to create directory for %q: %w", filePath, err)
	}

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create markdown file: %w", err)
	}
	defer file.Close()

	var sb strings.Builder

	sb.WriteString("# Candidate PR Analysis Report\n\n")
	sb.WriteString(fmt.Sprintf("**Total Candidates**: %d  \n", len(records)))
	if len(records) > 0 {
		sb.WriteString(fmt.Sprintf("**Repository**: `%s`  \n", records[0].Original.Repository))
		sb.WriteString(fmt.Sprintf("**Observation Cut-Off**: `%s`  \n", records[0].Provenance.ObservationEnd.Format("2006-01-02")))
	}
	sb.WriteString("\n---\n\n")

	sb.WriteString("| PR # | Title | Merged (T0) | Stateful Score | Fix Signals | Changed Functions | Explanation |\n")
	sb.WriteString("| :---: | :--- | :---: | :---: | :---: | :--- | :--- |\n")

	for _, r := range records {
		prLink := fmt.Sprintf("[%d](https://github.com/%s/pull/%d)", r.Original.Number, r.Original.Repository, r.Original.Number)
		escapedTitle := strings.ReplaceAll(r.Original.Title, "|", "\\|")
		mergedDate := r.Original.MergedAt.Format("2006-01-02")
		scoreBadge := fmt.Sprintf("**%.2f** (%s)", r.Stateful.Score, r.Stateful.Category)

		signalCount := len(r.Retrospective.StrongSignals) + len(r.Retrospective.MediumSignals) + len(r.Retrospective.WeakSignals)
		signalStr := fmt.Sprintf("%d signals", signalCount)
		if len(r.Retrospective.StrongSignals) > 0 {
			signalStr = fmt.Sprintf("🔴 **%d strong**", len(r.Retrospective.StrongSignals))
		} else if len(r.Retrospective.MediumSignals) > 0 {
			signalStr = fmt.Sprintf("🟡 %d medium", len(r.Retrospective.MediumSignals))
		} else if len(r.Retrospective.WeakSignals) > 0 {
			signalStr = fmt.Sprintf("⚪ %d weak", len(r.Retrospective.WeakSignals))
		}

		funcs := strings.Join(r.Original.ChangedFunctions, ", ")
		if len(funcs) > 60 {
			funcs = funcs[:57] + "..."
		}
		if funcs == "" {
			funcs = "-"
		}

		explanation := strings.ReplaceAll(r.Stateful.Explanation, "|", "\\|")

		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | `%s` | %s |\n",
			prLink, escapedTitle, mergedDate, scoreBadge, signalStr, funcs, explanation))
	}

	sb.WriteString("\n\n## Detailed Retrospective Evidence\n\n")
	for _, r := range records {
		totalSignals := len(r.Retrospective.StrongSignals) + len(r.Retrospective.MediumSignals) + len(r.Retrospective.WeakSignals)
		if totalSignals == 0 {
			continue
		}

		sb.WriteString(fmt.Sprintf("### PR #%d: %s\n\n", r.Original.Number, r.Original.Title))
		sb.WriteString(fmt.Sprintf("- **Merged**: `%s` (T0)\n", r.Original.MergedAt.Format("2006-01-02 15:04:05 UTC")))
		sb.WriteString(fmt.Sprintf("- **Stateful Score**: `%.2f` (%s)\n", r.Stateful.Score, r.Stateful.Category))
		sb.WriteString(fmt.Sprintf("- **Explanation**: %s\n\n", r.Stateful.Explanation))

		sb.WriteString("**Later Corrective Signals:**\n")
		for _, s := range r.Retrospective.StrongSignals {
			sb.WriteString(fmt.Sprintf("- 🔴 **[STRONG: %s]** Commit `%s` on `%s`: *%s*\n",
				s.SignalType, s.SourceRef, s.Timestamp.Format("2006-01-02"), s.RawSnippet))
		}
		for _, m := range r.Retrospective.MediumSignals {
			sb.WriteString(fmt.Sprintf("- 🟡 **[MEDIUM: %s]** Commit `%s` on `%s` (Function: `%s`): *%s*\n",
				m.SignalType, m.SourceRef, m.Timestamp.Format("2006-01-02"), m.FunctionOrPath, m.Context))
		}
		for _, w := range r.Retrospective.WeakSignals {
			sb.WriteString(fmt.Sprintf("- ⚪ **[WEAK: %s]** Commit `%s` on `%s` (File: `%s`): *%s*\n",
				w.SignalType, w.SourceRef, w.Timestamp.Format("2006-01-02"), w.FilePath, w.Context))
		}
		sb.WriteString("\n---\n\n")
	}

	_, err = file.WriteString(sb.String())
	return err
}

// WriteCSV exports candidate records as a CSV file for Excel / Google Sheets.
func WriteCSV(filePath string, records []model.PRCandidateRecord) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return fmt.Errorf("failed to create directory for %q: %w", filePath, err)
	}

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create CSV file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Header
	header := []string{
		"PR_Number", "Title", "Author", "Merged_At", "Stateful_Score", "Stateful_Category",
		"Strong_Signals_Count", "Medium_Signals_Count", "Weak_Signals_Count",
		"Summary_Rank_Score", "Changed_Files_Count", "Changed_Functions", "Explanation",
	}
	if err := writer.Write(header); err != nil {
		return err
	}

	for _, r := range records {
		row := []string{
			strconv.Itoa(r.Original.Number),
			r.Original.Title,
			r.Original.Author,
			r.Original.MergedAt.Format("2006-01-02"),
			fmt.Sprintf("%.2f", r.Stateful.Score),
			r.Stateful.Category,
			strconv.Itoa(len(r.Retrospective.StrongSignals)),
			strconv.Itoa(len(r.Retrospective.MediumSignals)),
			strconv.Itoa(len(r.Retrospective.WeakSignals)),
			fmt.Sprintf("%.2f", r.Retrospective.SummaryRankScore),
			strconv.Itoa(len(r.Original.ChangedFiles)),
			strings.Join(r.Original.ChangedFunctions, "; "),
			r.Stateful.Explanation,
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}

	return nil
}
