package cmd

import (
	"context"
	"log"

	"github.com/spf13/cobra"
)

// reindexLakeCmd rebuilds the parquet_files index by scanning existing lake files.
var reindexLakeCmd = &cobra.Command{
	Use:   "reindex-lake",
	Short: "Rebuild the lake index from parquet files on disk",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		cacheDir, err := cmd.Flags().GetString("cache-dir")
		if err != nil {
			return err
		}

		cache, err := newCacheManager(cacheDir)
		if err != nil {
			return err
		}
		defer cache.close()

		if err := cache.lake.rebuildIndex(ctx); err != nil {
			return err
		}

		log.Println("lake index rebuilt")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(reindexLakeCmd)
	reindexLakeCmd.Flags().String("cache-dir", defaultCacheDir(), "cache directory (hosts lake and index)")
}
