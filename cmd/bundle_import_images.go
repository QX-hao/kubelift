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
	bundleImportImagesConfigPath = defaultClusterConfigPath
	bundleImportImagesTimeout    = 45 * time.Minute
)

var bundleImportImagesCmd = &cobra.Command{
	Use:   "import-images <IPv4>",
	Short: "Upload and import offline images on a remote node",
	Long: `Validate the configured offline bundle, upload its payloads to the remote
node, and import Kubernetes and Cilium image archives into containerd's k8s.io
namespace. This command does not pull from a registry or run kubeadm.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if bundleImportImagesTimeout <= 0 {
			return fmt.Errorf("bundle import-images timeout must be greater than zero")
		}
		address, err := parseSSHAddress(args[0])
		if err != nil {
			return err
		}
		configuration, err := config.Load(bundleImportImagesConfigPath)
		if err != nil {
			return err
		}
		if err := requireLocalPreflight(*configuration); err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), bundleImportImagesTimeout)
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
		images, err := install.ImportImages(ctx, client, report.RemoteRoot, report.Manifest)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(
			cmd.OutOrStdout(),
			"Uploaded %d payloads and imported images on %s: Kubernetes %d, Cilium %d, Registry %d\n",
			len(report.Files), address, images.KubernetesCount, images.CiliumCount, images.RegistryCount,
		)
		return err
	},
}

func init() {
	bundleCmd.AddCommand(bundleImportImagesCmd)
	bundleImportImagesCmd.Flags().StringVarP(
		&bundleImportImagesConfigPath,
		"config",
		"f",
		defaultClusterConfigPath,
		"path to the cluster configuration file",
	)
	bundleImportImagesCmd.Flags().DurationVar(
		&bundleImportImagesTimeout,
		"timeout",
		bundleImportImagesTimeout,
		"maximum time to validate, upload, and import images",
	)
}
