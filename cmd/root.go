/*
Copyright © 2025 Darko Luketic <info@icod.de>
*/
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "gotodon",
	Short: "A Mastodon-compatible ActivityPub server written in Go",
	Long: `Gotodon is a 100% API-compatible Mastodon backend implementation in Go.

It provides:
  - Full Mastodon REST API compatibility (v1 and v2)
  - ActivityPub federation support
  - Real-time streaming via WebSockets and Server-Sent Events
  - OAuth 2.0 authentication

Use 'gotodon serve' to start the server.`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	// rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.gotodon.yaml)")

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
