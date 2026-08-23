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
	"sort"
	"strings"

	"github.com/QX-hao/kubelift/internal/bundle"
	"github.com/spf13/cobra"
)

var inspectShowFiles bool

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
		if !inspectShowFiles {
			return nil
		}

		files := append([]bundle.File(nil), manifest.Spec.Files...)
		sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Payloads:"); err != nil {
			return err
		}
		for _, file := range files {
			if _, err := fmt.Fprintf(
				cmd.OutOrStdout(),
				"  %s\t%s\t%d\t%s\n",
				file.Path,
				file.Kind,
				file.Size,
				file.SHA256,
			); err != nil {
				return err
			}
		}
		return nil
	},
}

func init() {
	bundleCmd.AddCommand(inspectCmd)
	inspectCmd.Flags().BoolVar(&inspectShowFiles, "files", false, "list every verified payload file")
}
