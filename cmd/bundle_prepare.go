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
	"path"
	"time"

	"github.com/QX-hao/kubelift/internal/config"
	"github.com/QX-hao/kubelift/internal/distribute"
	"github.com/QX-hao/kubelift/internal/install"
	"github.com/QX-hao/kubelift/internal/remote"
	"github.com/spf13/cobra"
)

var (
	bundlePrepareConfigPath = defaultClusterConfigPath
	bundlePrepareTimeout    = 45 * time.Minute
)

var bundlePrepareCmd = &cobra.Command{
	Use:   "prepare <IPv4>",
	Short: "Upload and prepare a remote Kubernetes node",
	Long: `Validate the configured offline bundle, upload its payloads to the remote
node, install the bundled binaries and containerd runtime, and configure the
containerd and kubelet systemd units. This command does not run kubeadm or
install Cilium.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if bundlePrepareTimeout <= 0 {
			return fmt.Errorf("bundle prepare timeout must be greater than zero")
		}
		address, err := parseSSHAddress(args[0])
		if err != nil {
			return err
		}
		configuration, err := config.Load(bundlePrepareConfigPath)
		if err != nil {
			return err
		}
		if err := requireLocalPreflight(*configuration); err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), bundlePrepareTimeout)
		defer cancel()
		client, err := remote.Connect(ctx, remote.Target{
			Address:        address.String(),
			User:           configuration.Spec.SSH.User,
			Port:           configuration.Spec.SSH.Port,
			PrivateKeyPath: configuration.Spec.SSH.PrivateKey,
		})
		if err != nil {
			return err
		}
		defer client.Close()

		if err := requireRemotePreflight(ctx, client, *configuration); err != nil {
			return err
		}
		remoteRoot := path.Join("/var/lib/kubelift/staging", configuration.Metadata.Name)
		report, err := distribute.Push(ctx, client, configuration.Spec.Offline.Bundle, remoteRoot)
		if err != nil {
			return err
		}
		preparation, err := install.PrepareNode(ctx, client, report.RemoteRoot, report.Manifest)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(
			cmd.OutOrStdout(),
			"Uploaded %d payloads and prepared %s: %d binaries, %d runtime archive, %d configs, %d systemd units\n",
			len(report.Files), address, preparation.BinaryCount, preparation.RuntimeCount, preparation.ConfigCount, preparation.UnitCount,
		)
		return err
	},
}

func init() {
	bundleCmd.AddCommand(bundlePrepareCmd)
	bundlePrepareCmd.Flags().StringVarP(
		&bundlePrepareConfigPath,
		"config",
		"f",
		defaultClusterConfigPath,
		"path to the cluster configuration file",
	)
	bundlePrepareCmd.Flags().DurationVar(
		&bundlePrepareTimeout,
		"timeout",
		bundlePrepareTimeout,
		"maximum time to validate, upload, and prepare the node",
	)
}
