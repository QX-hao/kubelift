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
	"github.com/spf13/cobra"
)

var configInitOutputPath string

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a cluster configuration template",
	Long: `Create a KubeLift cluster configuration template without overwriting an
existing file. Edit the generated values before running check or create.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.WriteTemplate(configInitOutputPath); err != nil {
			return err
		}

		_, err := fmt.Fprintf(
			cmd.OutOrStdout(),
			"Created cluster configuration template at %q.\n",
			configInitOutputPath,
		)
		return err
	},
}

func init() {
	configCmd.AddCommand(configInitCmd)
	configInitCmd.Flags().StringVarP(
		&configInitOutputPath,
		"output",
		"o",
		defaultClusterConfigPath,
		"path for the generated cluster configuration",
	)
}
