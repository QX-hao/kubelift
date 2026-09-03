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
	"github.com/QX-hao/kubelift/internal/kubeadm"
	"github.com/spf13/cobra"
)

var kubeadmConfigPath = defaultClusterConfigPath

var kubeadmConfigCmd = &cobra.Command{
	Use:   "kubeadm",
	Short: "Render the kubeadm init configuration",
	Long: `Render the kubeadm InitConfiguration, ClusterConfiguration, and
KubeletConfiguration derived from the KubeLift cluster configuration. The
result is written to standard output and does not modify the host.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		configuration, err := config.Load(kubeadmConfigPath)
		if err != nil {
			return err
		}
		contents, err := kubeadm.GenerateInitConfig(*configuration)
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(contents)
		return err
	},
}

func init() {
	configCmd.AddCommand(kubeadmConfigCmd)
	kubeadmConfigCmd.Flags().StringVarP(
		&kubeadmConfigPath,
		"config",
		"f",
		defaultClusterConfigPath,
		"path to the cluster configuration file",
	)
}
