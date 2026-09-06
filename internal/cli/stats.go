package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"miner/internal/storage"
)

var statsInFlag string

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Print statistical summary of mined PR cohort",
	Long:  `Calculates distribution statistics of stateful scores, retrospective signals, and top touched subsystems across a dataset.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		inFile := "./data/candidates.jsonl"
		if statsInFlag != "" {
			inFile = statsInFlag
		}

		records, err := storage.ReadJSONL(inFile)
		if err != nil {
			return fmt.Errorf("failed to read records from %q: %w", inFile, err)
		}

		total := len(records)
		if total == 0 {
			fmt.Println("No records found.")
			return nil
		}

		highCount, medCount, lowCount := 0, 0, 0
		strongFixCount, mediumFixCount, weakFixCount := 0, 0, 0
		subsystemCounts := make(map[string]int)

		for _, r := range records {
			switch r.Stateful.Category {
			case "HIGH":
				highCount++
			case "MEDIUM":
				medCount++
			default:
				lowCount++
			}

			if len(r.Retrospective.StrongSignals) > 0 {
				strongFixCount++
			} else if len(r.Retrospective.MediumSignals) > 0 {
				mediumFixCount++
			} else if len(r.Retrospective.WeakSignals) > 0 {
				weakFixCount++
			}

			for _, f := range r.Original.ChangedFiles {
				parts := strings.Split(f.Path, "/")
				if len(parts) > 1 {
					subsystemCounts[parts[0]]++
				}
			}
		}

		fmt.Println("================================================================================")
		fmt.Printf("Dataset Summary: %s (%d Total Merged PRs)\n", inFile, total)
		fmt.Println("--------------------------------------------------------------------------------")
		fmt.Println("Stateful / Distributed Score Distribution:")
		fmt.Printf("  HIGH   (>= 0.60): %4d (%5.1f%%)\n", highCount, float64(highCount)/float64(total)*100)
		fmt.Printf("  MEDIUM (>= 0.30): %4d (%5.1f%%)\n", medCount, float64(medCount)/float64(total)*100)
		fmt.Printf("  LOW    (< 0.30):  %4d (%5.1f%%)\n", lowCount, float64(lowCount)/float64(total)*100)
		fmt.Println("--------------------------------------------------------------------------------")
		fmt.Println("Retrospective Corrective Evidence Distribution:")
		fmt.Printf("  Strong Fix Signals  (Fixes: SHA, revert, regression): %4d (%5.1f%%)\n",
			strongFixCount, float64(strongFixCount)/float64(total)*100)
		fmt.Printf("  Medium Fix Signals  (same-function bugfix):           %4d (%5.1f%%)\n",
			mediumFixCount, float64(mediumFixCount)/float64(total)*100)
		fmt.Printf("  Weak Fix Signals    (same-file overlap):              %4d (%5.1f%%)\n",
			weakFixCount, float64(weakFixCount)/float64(total)*100)
		fmt.Printf("  No Fix Signals:                                       %4d (%5.1f%%)\n",
			total-strongFixCount-mediumFixCount-weakFixCount,
			float64(total-strongFixCount-mediumFixCount-weakFixCount)/float64(total)*100)
		fmt.Println("================================================================================")

		return nil
	},
}

func init() {
	statsCmd.Flags().StringVarP(&statsInFlag, "in", "i", "./data/candidates.jsonl", "Input candidates JSONL file")
}
