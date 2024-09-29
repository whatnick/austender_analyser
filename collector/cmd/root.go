package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "austender",
	Short: "Get austender summaries",
	Long:  `Austender CLI tool to scrape and persist tender awards data for various companies`,
	Run: func(cmd *cobra.Command, args []string) {
		companyName, _ := cmd.Flags().GetString("c")
		keywordVal, _ := cmd.Flags().GetString("d")
		agencyVal, _ := cmd.Flags().GetString("k")

		scrapeAncap(keywordVal, companyName, agencyVal)
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().String("c", "", "Company to scan")
	rootCmd.PersistentFlags().String("d", "", "Department to scan")
	rootCmd.PersistentFlags().String("k", "", "Keywords to scan")
}
