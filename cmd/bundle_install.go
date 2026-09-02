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
	bundleInstallConfigPath = defaultClusterConfigPath
	bundleInstallTimeout    = 45 * time.Minute
)

var bundleInstallCmd = &cobra.Command{
	Use:   "install <IPv4>",
	Short: "Upload and install offline Debian packages on a remote node",
	Long: `Validate the configured offline bundle, upload its payloads to the remote
node, and install the Debian package payloads without downloading from a
package repository. This command does not configure containerd or run kubeadm.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if bundleInstallTimeout <= 0 {
			return fmt.Errorf("bundle install timeout must be greater than zero")
		}
		address, err := parseSSHAddress(args[0])
		if err != nil {
			return err
		}
		configuration, err := config.Load(bundleInstallConfigPath)
		if err != nil {
			return err
		}
		if err := requireLocalPreflight(*configuration); err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), bundleInstallTimeout)
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

		if err := requireRemotePreflight(ctx, client); err != nil {
			return err
		}
		remoteRoot := path.Join("/var/lib/kubelift/staging", configuration.Metadata.Name)
		report, err := distribute.Push(ctx, client, configuration.Spec.Offline.Bundle, remoteRoot)
		if err != nil {
			return err
		}
		packageCount, err := install.InstallDebPackages(ctx, client, report.RemoteRoot, report.Files)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(
			cmd.OutOrStdout(),
			"Uploaded %d payloads and installed %d Debian packages on %s\n",
			len(report.Files), packageCount, address,
		)
		return err
	},
}

func init() {
	bundleCmd.AddCommand(bundleInstallCmd)
	bundleInstallCmd.Flags().StringVarP(
		&bundleInstallConfigPath,
		"config",
		"f",
		defaultClusterConfigPath,
		"path to the cluster configuration file",
	)
	bundleInstallCmd.Flags().DurationVar(
		&bundleInstallTimeout,
		"timeout",
		bundleInstallTimeout,
		"maximum time to validate, upload, and install packages",
	)
}
