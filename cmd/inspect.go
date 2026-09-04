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
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/QX-hao/kubelift/internal/bundle"
	"github.com/QX-hao/kubelift/internal/cilium"
	"github.com/QX-hao/kubelift/internal/config"
	"github.com/QX-hao/kubelift/internal/registry"
	"github.com/spf13/cobra"
)

var inspectShowFiles bool
var inspectConfigPath string

var inspectCmd = &cobra.Command{
	Use:     "inspect <bundle.tar.zst>",
	Short:   "Verify checksums and describe an offline bundle",
	Args:    cobra.ExactArgs(1),
	Example: "  kubelift bundle inspect /opt/kubelift/kubernetes-v1.28.15-amd64.tar.zst",
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := bundle.Inspect(args[0])
		if err != nil {
			return err
		}
		manifest := report.Manifest
		if inspectConfigPath != "" {
			configuration, err := config.Load(inspectConfigPath)
			if err != nil {
				return err
			}
			if manifest.Spec.KubernetesVersion != configuration.Spec.Kubernetes.Version {
				return fmt.Errorf("bundle Kubernetes version %q does not match configured version %q", manifest.Spec.KubernetesVersion, configuration.Spec.Kubernetes.Version)
			}
			if err := bundle.ValidateClusterProfile(manifest, configuration.Spec.Registry.Enabled); err != nil {
				return err
			}
			if err := validateBundleTemplates(args[0], *configuration); err != nil {
				return err
			}
		}
		_, err = fmt.Fprintf(
			cmd.OutOrStdout(),
			"Bundle: %s\nKubernetes: %s\nArchitecture: %s\nUbuntu: %s\nContainerd: %s\nCilium: %s\nRegistry: %s\nFiles: %d\nPayload bytes: %d\nChecksums: verified\nAuthenticity: not verified\n",
			manifest.Metadata.Name,
			manifest.Spec.KubernetesVersion,
			manifest.Spec.Architecture,
			strings.Join(manifest.Spec.UbuntuVersions, ", "),
			manifest.Spec.Components["containerd"],
			manifest.Spec.Components["cilium"],
			manifest.Spec.Components["registry"],
			report.FileCount,
			report.TotalSize,
		)
		if err != nil {
			return err
		}
		if inspectConfigPath != "" {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Cluster profile: verified for %s\n", inspectConfigPath); err != nil {
				return err
			}
		}
		if !inspectShowFiles {
			return nil
		}

		files := append([]bundle.File(nil), manifest.Spec.Files...)
		sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Payloads:"); err != nil {
			return err
		}
		for _, file := range files {
			role := file.Role
			if role == "" {
				role = "-"
			}
			if _, err := fmt.Fprintf(
				cmd.OutOrStdout(),
				"  %s\t%s\t%s\t%d\t%s\n",
				file.Path,
				file.Kind,
				role,
				file.Size,
				file.SHA256,
			); err != nil {
				return err
			}
		}
		return nil
	},
}

func validateBundleTemplates(bundlePath string, configuration config.Config) error {
	directory, err := os.MkdirTemp("", "kubelift-inspect-")
	if err != nil {
		return fmt.Errorf("create temporary bundle inspection directory: %w", err)
	}
	defer os.RemoveAll(directory)
	report, err := bundle.Extract(bundlePath, directory)
	if err != nil {
		return err
	}
	ciliumFile := report.Manifest.FilesForRole("cilium-manifest")[0]
	ciliumTemplate, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(ciliumFile.Path)))
	if err != nil {
		return fmt.Errorf("read Cilium manifest template: %w", err)
	}
	if _, err := cilium.RenderManifest(configuration, ciliumTemplate); err != nil {
		return err
	}
	if configuration.Spec.Registry.Enabled {
		registryFile := report.Manifest.FilesForRole("registry-manifest")[0]
		registryTemplate, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(registryFile.Path)))
		if err != nil {
			return fmt.Errorf("read Registry manifest template: %w", err)
		}
		if _, err := registry.RenderManifest(configuration, "/var/lib/kubelift/registry", registryTemplate); err != nil {
			return err
		}
	}
	return nil
}

func init() {
	bundleCmd.AddCommand(inspectCmd)
	inspectCmd.Flags().BoolVar(&inspectShowFiles, "files", false, "list every verified payload file")
	inspectCmd.Flags().StringVarP(&inspectConfigPath, "config", "f", "", "validate install payloads against a cluster configuration")
}
