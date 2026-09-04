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

	"github.com/QX-hao/kubelift/internal/bundle"
	"github.com/QX-hao/kubelift/internal/config"
	"github.com/QX-hao/kubelift/internal/distribute"
	"github.com/QX-hao/kubelift/internal/preflight"
	"github.com/QX-hao/kubelift/internal/remote"
	"github.com/spf13/cobra"
)

var (
	bundlePushConfigPath = defaultClusterConfigPath
	bundlePushTimeout    = 30 * time.Minute
)

var bundlePushCmd = &cobra.Command{
	Use:   "push <IPv4>",
	Short: "Upload the configured offline bundle to a remote node",
	Long: `Validate the local host and remote node, then upload every payload from
the configured offline bundle. This command only stages files under
/var/lib/kubelift and does not install components or modify Kubernetes.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if bundlePushTimeout <= 0 {
			return fmt.Errorf("bundle push timeout must be greater than zero")
		}
		address, err := parseSSHAddress(args[0])
		if err != nil {
			return err
		}
		configuration, err := config.Load(bundlePushConfigPath)
		if err != nil {
			return err
		}
		if err := requireLocalPreflight(*configuration); err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), bundlePushTimeout)
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
		_, err = fmt.Fprintf(
			cmd.OutOrStdout(),
			"Uploaded %d bundle payloads to %s (%s)\n",
			len(report.Files), report.RemoteRoot, report.Manifest.Metadata.Name,
		)
		return err
	},
}

func init() {
	bundleCmd.AddCommand(bundlePushCmd)
	bundlePushCmd.Flags().StringVarP(
		&bundlePushConfigPath,
		"config",
		"f",
		defaultClusterConfigPath,
		"path to the cluster configuration file",
	)
	bundlePushCmd.Flags().DurationVar(
		&bundlePushTimeout,
		"timeout",
		bundlePushTimeout,
		"maximum time to validate and upload the bundle",
	)
}

func requireLocalPreflight(configuration config.Config) error {
	for _, result := range preflight.CheckLocal(configuration) {
		if result.Err != nil {
			return fmt.Errorf("local preflight %s failed: %w", result.Name, result.Err)
		}
	}
	return nil
}

func requireRemotePreflight(ctx context.Context, client *remote.Client, configuration config.Config) error {
	for _, result := range preflight.CheckRemote(ctx, client) {
		if result.Err != nil {
			return fmt.Errorf("remote preflight %s failed: %w", result.Name, result.Err)
		}
	}
	report, err := bundle.Inspect(configuration.Spec.Offline.Bundle)
	if err != nil {
		return err
	}
	if report.Manifest.Spec.KubernetesVersion != configuration.Spec.Kubernetes.Version {
		return fmt.Errorf("bundle Kubernetes version %q does not match configured version %q", report.Manifest.Spec.KubernetesVersion, configuration.Spec.Kubernetes.Version)
	}
	for _, result := range preflight.CheckRemoteBundle(ctx, client, report.Manifest) {
		if result.Err != nil {
			return fmt.Errorf("remote preflight %s failed: %w", result.Name, result.Err)
		}
	}
	return nil
}
