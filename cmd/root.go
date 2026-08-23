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
	"os"

	"github.com/QX-hao/kubelift/internal/buildinfo"
	"github.com/spf13/cobra"
)

const defaultClusterConfigPath = "/etc/kubelift/cluster.yaml"

var rootCmd = &cobra.Command{
	Use:          "kubelift",
	Short:        "Bootstrap Kubernetes clusters on existing Ubuntu servers",
	SilenceUsage: true,
	Version:      buildinfo.Version,
	Long: `KubeLift bootstraps Kubernetes clusters on existing Ubuntu servers.

Run KubeLift on the first control-plane node to prepare and join the remaining
nodes over SSH. It uses containerd and Cilium and installs a selected Kubernetes
version from a fully offline bundle.`,
}

// Execute 运行根命令，并在 Cobra 返回错误时设置非零退出状态。
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}
