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

var bundleCreateOutputPath string

var bundleCreateCmd = &cobra.Command{
	Use:     "create <source-directory>",
	Short:   "Create a checksum-verified offline bundle",
	Args:    cobra.ExactArgs(1),
	Example: "  kubelift bundle create ./bundle-source -o ./kubernetes-v1.28.15-amd64.tar.zst",
	RunE: func(cmd *cobra.Command, args []string) error {
		if bundleCreateOutputPath == "" {
			return fmt.Errorf("--output is required")
		}
		if err := bundle.Create(args[0], bundleCreateOutputPath); err != nil {
			return err
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "Created checksum-verified offline bundle at %q.\n", bundleCreateOutputPath)
		return err
	},
}

func init() {
	bundleCmd.AddCommand(bundleCreateCmd)
	bundleCreateCmd.Flags().StringVarP(&bundleCreateOutputPath, "output", "o", "", "output path for the bundle tar.zst")
}
