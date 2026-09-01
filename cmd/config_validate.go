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

var configValidatePath string

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate cluster configuration syntax and values",
	Long:  "Validate only the cluster configuration schema and values without checking the current host or offline bundle files.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// 配置加载和校验
		configuration, err := config.Load(configValidatePath)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Configuration %q is valid for cluster %q.\n", configValidatePath, configuration.Metadata.Name)
		return err
	},
}

func init() {
	configCmd.AddCommand(configValidateCmd)
	configValidateCmd.Flags().StringVarP(
		&configValidatePath,
		"config",
		"f",
		defaultClusterConfigPath,
		"path to the cluster configuration file",
	)
}
