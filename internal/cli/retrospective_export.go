package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"miner/internal/retrospectiveexport"
)

var retrospectiveExportFlags struct {
	input, repo, out string
	pr               int
}

var retrospectiveExportCmd = &cobra.Command{
	Use:   "retrospective-export",
	Short: "Materialize complete retrospective evidence for a selected PR",
	RunE: func(cmd *cobra.Command, args []string) error {
		f := retrospectiveExportFlags
		if err := retrospectiveexport.Export(cmd.Context(), retrospectiveexport.Options{Input: f.input, Repo: f.repo, Out: f.out, PR: f.pr}); err != nil {
			return err
		}
		fmt.Printf("Exported retrospective evidence for PR #%d to %s\n", f.pr, f.out)
		return nil
	},
}

func init() {
	f := &retrospectiveExportFlags
	retrospectiveExportCmd.Flags().StringVarP(&f.input, "input", "i", "", "Input benchmark candidates JSONL file")
	retrospectiveExportCmd.Flags().IntVarP(&f.pr, "pr", "p", 0, "Original PR number")
	retrospectiveExportCmd.Flags().StringVar(&f.repo, "repo", "", "Local Git repository or bare repository")
	retrospectiveExportCmd.Flags().StringVarP(&f.out, "out", "o", "", "Retrospective output directory")
	_ = retrospectiveExportCmd.MarkFlagRequired("input")
	_ = retrospectiveExportCmd.MarkFlagRequired("pr")
	_ = retrospectiveExportCmd.MarkFlagRequired("repo")
	_ = retrospectiveExportCmd.MarkFlagRequired("out")
}
