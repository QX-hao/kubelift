/*
Copyright © 2026 QX-hao

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package cmd

import (
	"fmt"

	"github.com/QX-hao/kubelift/internal/config"
	"github.com/QX-hao/kubelift/internal/preflight"
	"github.com/spf13/cobra"
)

var checkConfigPath string

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Run KubeLift configuration and local host checks",
	Long: `Validate the KubeLift cluster configuration and check whether the current
Master0 host meets the local requirements. This command does not connect to
other servers or modify the system.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		configuration, err := config.Load(checkConfigPath)
		if err != nil {
			return err
		}

		results := preflight.CheckLocal(*configuration)
		failed := false
		for _, result := range results {
			if result.Err != nil {
				failed = true
				break
			}
		}

		output := cmd.OutOrStdout()
		if failed {
			output = cmd.ErrOrStderr()
		}
		if _, err := fmt.Fprintf(
			output,
			"[PASS] configuration: %q (%s)\n",
			checkConfigPath,
			configuration.Metadata.Name,
		); err != nil {
			return err
		}

		for _, result := range results {
			status := "PASS"
			detail := result.Detail
			if result.Err != nil {
				status = "FAIL"
				detail = result.Err.Error()
			}
			if _, err := fmt.Fprintf(output, "[%s] %s: %s\n", status, result.Name, detail); err != nil {
				return err
			}
		}
		if failed {
			return fmt.Errorf("local preflight checks failed")
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(checkCmd)
	checkCmd.Flags().StringVarP(
		&checkConfigPath,
		"config",
		"f",
		defaultClusterConfigPath,
		"path to the cluster configuration file",
	)
}
