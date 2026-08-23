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
	"github.com/QX-hao/kubelift/internal/workflow"
	"github.com/spf13/cobra"
)

var addNodeOptions addCommandOptions

var nodeCmd = &cobra.Command{
	Use:     "node <IPv4>",
	Short:   "Add a worker node",
	Args:    cobra.ExactArgs(1),
	Example: "  kubelift add node 10.0.0.21 --dry-run",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAdd(cmd, workflow.RoleNode, args[0], addNodeOptions)
	},
}

func init() {
	addCmd.AddCommand(nodeCmd)
	bindAddFlags(nodeCmd, &addNodeOptions)
}
