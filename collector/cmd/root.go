package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "austender",
	Short: "Get austender summaries",
	Long:  `Austender CLI tool to scrape and persist tender awards data for various companies`,
	Run: func(cmd *cobra.Command, args []string) {
		companyName, _ := cmd.Flags().GetString("c")
		agencyVal, _ := cmd.Flags().GetString("d")
		keywordVal, _ := cmd.Flags().GetString("k")
		startRaw, _ := cmd.Flags().GetString("start-date")
		endRaw, _ := cmd.Flags().GetString("end-date")
		dateType, _ := cmd.Flags().GetString("date-type")
		lookbackYears, _ := cmd.Flags().GetInt("lookback-years")
		verbose, _ := cmd.Flags().GetBool("verbose")

		start, err := parseDateFlag(startRaw)
		if err != nil {
			fmt.Println(err)
			return
		}
		end, err := parseDateFlag(endRaw)
		if err != nil {
			fmt.Println(err)
			return
		}
		if err := validateDateOrder(start, end); err != nil {
			fmt.Println(err)
			return
		}

		scrapeAncap(keywordVal, companyName, agencyVal, start, end, dateType, lookbackYears, verbose)
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
	rootCmd.PersistentFlags().String("d", "", "Department/agency to scan")
	rootCmd.PersistentFlags().String("k", "", "Keywords to scan")
	rootCmd.PersistentFlags().String("start-date", "", "Optional start date (YYYY-MM-DD or RFC3339)")
	rootCmd.PersistentFlags().String("end-date", "", "Optional end date (YYYY-MM-DD or RFC3339)")
	rootCmd.PersistentFlags().String("date-type", defaultDateType, "OCDS date field: contractPublished, contractStart, contractEnd, contractLastModified")
	rootCmd.PersistentFlags().Int("lookback-years", 0, "Default window (years) when start date not specified; falls back to env AUSTENDER_LOOKBACK_YEARS or 20 years")
	rootCmd.PersistentFlags().Bool("verbose", false, "Stream each matching contract as it is processed")
}

func parseDateFlag(raw string) (time.Time, error) {
	return parseDateInput(raw)
}
