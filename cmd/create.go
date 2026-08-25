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
	"github.com/QX-hao/kubelift/internal/config"
	"github.com/QX-hao/kubelift/internal/workflow"
	"github.com/spf13/cobra"
)

var (
	createConfigPath string
	createDryRun     bool
)

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a Kubernetes cluster on the current Master0 host",
	Long: `Create a single-Master Kubernetes cluster from the configured offline
bundle. Additional masters and workers are joined later with kubelift add.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		configuration, err := config.Load(createConfigPath)
		if err != nil {
			return err
		}
		plan := workflow.CreatePlan(*configuration)
		if !createDryRun {
			return executionUnavailable(plan.Action)
		}
		return printPlan(cmd, plan)
	},
}

func init() {
	rootCmd.AddCommand(createCmd)
	createCmd.Flags().StringVarP(
		&createConfigPath,
		"config",
		"f",
		defaultClusterConfigPath,
		"path to the cluster configuration file",
	)
	
	createCmd.Flags().BoolVar(
		&createDryRun, 
		"dry-run", 
		false, 
		"print the validated plan without changing the system")
}
