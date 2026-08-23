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
	"fmt"

	"github.com/QX-hao/kubelift/internal/bundle"
	"github.com/spf13/cobra"
)

var bundleManifestOptions bundle.ManifestOptions

var bundleManifestCmd = &cobra.Command{
	Use:   "manifest <source-directory>",
	Short: "Generate a manifest for prepared offline payloads",
	Long: `Scan prepared payloads under bin, images, manifests, and packages. Write
manifest.yaml with deterministic paths, file sizes, and SHA-256 checksums.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := bundle.WriteManifest(args[0], bundleManifestOptions)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Created bundle manifest at %q.\n", path)
		return err
	},
}

func init() {
	bundleCmd.AddCommand(bundleManifestCmd)
	bundleManifestCmd.Flags().StringVar(&bundleManifestOptions.Name, "name", "", "bundle name as a lowercase DNS label")
	bundleManifestCmd.Flags().StringVar(&bundleManifestOptions.KubernetesVersion, "kubernetes-version", "", "exact Kubernetes version, for example v1.28.15")
	bundleManifestCmd.Flags().StringVar(&bundleManifestOptions.Architecture, "architecture", "", "bundle CPU architecture: amd64 or arm64")
	bundleManifestCmd.Flags().StringSliceVar(&bundleManifestOptions.UbuntuVersions, "ubuntu-version", nil, "supported Ubuntu version; may be repeated or comma-separated")
	bundleManifestCmd.Flags().StringVar(&bundleManifestOptions.ContainerdVersion, "containerd-version", "", "exact containerd version")
	bundleManifestCmd.Flags().StringVar(&bundleManifestOptions.CiliumVersion, "cilium-version", "", "exact Cilium version")
	bundleManifestCmd.Flags().StringVar(&bundleManifestOptions.RegistryVersion, "registry-version", "", "exact Registry version")
	for _, name := range []string{
		"name",
		"kubernetes-version",
		"architecture",
		"ubuntu-version",
		"containerd-version",
		"cilium-version",
		"registry-version",
	} {
		if err := bundleManifestCmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
}
