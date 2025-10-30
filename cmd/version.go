/*
Copyright © 2025 TheMachine <592858548@qq.com>
*/
package cmd

import (
	"fmt"
	"txt2geo/internal/version"

	"github.com/spf13/cobra"
)

// aboutCmd represents the about command
var aboutCmd = &cobra.Command{
	Use:   "about",
	Short: "Display information about",
	Long:  "Display basic information about TXT2GEO tool",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(version.GetAbout())
	},
}

func init() {
	rootCmd.AddCommand(aboutCmd)
}
