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
	"context"
	"fmt"
	"time"

	"github.com/QX-hao/kubelift/internal/config"
	"github.com/QX-hao/kubelift/internal/workflow"
	"github.com/spf13/cobra"
)

var (
	createConfigPath string
	createDryRun     bool
	createResume     bool
	createTimeout    = 60 * time.Minute
)

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a Kubernetes cluster on the current Master0 host",
	Long: `Create a single-Master Kubernetes cluster from the configured offline
bundle. Additional masters and workers are joined later with kubelift add.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if createTimeout <= 0 {
			return fmt.Errorf("create timeout must be greater than zero")
		}
		if createDryRun && createResume {
			return fmt.Errorf("--dry-run and --resume cannot be used together")
		}
		configuration, err := config.Load(createConfigPath)
		if err != nil {
			return err
		}
		plan := workflow.CreatePlan(*configuration)
		if createDryRun {
			return printPlan(cmd, plan)
		}
		if err := requireLocalPreflight(*configuration); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), createTimeout)
		defer cancel()
		result, err := workflow.NewLocalCreateExecutor().ExecuteCreate(ctx, *configuration, workflow.CreateOptions{Resume: createResume})
		if err != nil {
			return err
		}
		if result.AlreadyComplete {
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Cluster %q creation is already complete.\n", configuration.Metadata.Name)
			return err
		}
		if result.KubeadmOutput != "" {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), result.KubeadmOutput); err != nil {
				return err
			}
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Cluster %q created successfully. State: %s.\n", configuration.Metadata.Name, result.Phase)
		return err
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
		"print the validated plan without changing the system",
	)
	createCmd.Flags().BoolVar(&createResume, "resume", false, "continue a previously interrupted cluster creation")
	createCmd.Flags().DurationVar(&createTimeout, "timeout", createTimeout, "maximum time to create the cluster")
}
