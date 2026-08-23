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

	"github.com/QX-hao/kubelift/internal/clusterstatus"
	"github.com/spf13/cobra"
)

var (
	statusKubeconfigPath = "/etc/kubernetes/admin.conf"
	statusTimeout        = 15 * time.Second
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show Kubernetes node status",
	Long:  "Query the current cluster through kubectl and display all Kubernetes nodes.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if statusTimeout <= 0 {
			return fmt.Errorf("timeout must be greater than zero")
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), statusTimeout)
		defer cancel()

		output, err := clusterstatus.Run(ctx, statusKubeconfigPath)
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), string(output))
		return err
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().StringVar(&statusKubeconfigPath, "kubeconfig", statusKubeconfigPath, "path to the Kubernetes admin kubeconfig")
	statusCmd.Flags().DurationVar(&statusTimeout, "timeout", statusTimeout, "maximum time to wait for the cluster response")
}
