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
	uninstallConfigPath = defaultClusterConfigPath
	uninstallDryRun     bool
	uninstallForce      bool
	uninstallPurge      bool
	uninstallTimeout    = 60 * time.Minute
)

var uninstallCmd = &cobra.Command{
	Use:     "uninstall",
	Aliases: []string{"reset"},
	Short:   "Remove the Kubernetes cluster and KubeLift-managed runtime state",
	Long: `Remove the cluster from the current control-plane host and all nodes
discovered through the Kubernetes API. The command keeps the cluster
configuration and offline bundle by default so the host can be reused.

This is destructive. Use --force to execute it; use --dry-run to inspect the
steps without changing any host or cluster resource.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if uninstallTimeout <= 0 {
			return fmt.Errorf("uninstall timeout must be greater than zero")
		}
		configuration, err := config.Load(uninstallConfigPath)
		if err != nil {
			return err
		}
		if uninstallDryRun {
			return printPlan(cmd, workflow.UninstallPlan(*configuration))
		}
		if !uninstallForce {
			return fmt.Errorf("uninstall is destructive; pass --force to continue or --dry-run to inspect the plan")
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), uninstallTimeout)
		defer cancel()
		result, err := workflow.NewLocalUninstallExecutor().ExecuteUninstall(ctx, *configuration, workflow.UninstallOptions{
			PurgeConfig: uninstallPurge,
		})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Cluster %q uninstalled successfully; cleaned %d nodes.\n", configuration.Metadata.Name, len(result.Nodes))
		return err
	},
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
	uninstallCmd.Flags().StringVarP(&uninstallConfigPath, "config", "f", defaultClusterConfigPath, "path to the cluster configuration file")
	uninstallCmd.Flags().BoolVar(&uninstallDryRun, "dry-run", false, "print the validated uninstall plan without changing the system")
	uninstallCmd.Flags().BoolVar(&uninstallForce, "force", false, "confirm destructive cluster and host cleanup")
	uninstallCmd.Flags().BoolVar(&uninstallPurge, "purge-config", false, "also remove /etc/kubelift from the current control-plane host")
	uninstallCmd.Flags().DurationVar(&uninstallTimeout, "timeout", uninstallTimeout, "maximum time to uninstall the cluster")
}
