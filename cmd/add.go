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
	"github.com/QX-hao/kubelift/internal/config"
	"github.com/QX-hao/kubelift/internal/workflow"
	"github.com/spf13/cobra"
)

type addCommandOptions struct {
	configPath string
	name       string
	sshUser    string
	sshPort    int
	privateKey string
	dryRun     bool
}

var addCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a master or worker node to the cluster",
	Long:  "Add a remote Ubuntu server to the existing Kubernetes cluster over SSH.",
	Args:  cobra.NoArgs,
}

func init() {
	rootCmd.AddCommand(addCmd)
}

func bindAddFlags(command *cobra.Command, options *addCommandOptions) {
	command.Flags().StringVarP(&options.configPath, "config", "f", defaultClusterConfigPath, "path to the cluster configuration file")
	command.Flags().StringVar(&options.name, "name", "", "Kubernetes node name; defaults to the remote hostname")
	command.Flags().StringVar(&options.sshUser, "user", "", "override the configured SSH user")
	command.Flags().IntVar(&options.sshPort, "port", 0, "override the configured SSH port")
	command.Flags().StringVar(&options.privateKey, "key", "", "override the configured SSH private key path")
	command.Flags().BoolVar(&options.dryRun, "dry-run", false, "print the validated plan without changing the cluster")
}

func runAdd(cmd *cobra.Command, role workflow.Role, address string, options addCommandOptions) error {
	configuration, err := config.Load(options.configPath)
	if err != nil {
		return err
	}
	plan, err := workflow.AddPlan(*configuration, role, workflow.AddOptions{
		Address:    address,
		Name:       options.name,
		SSHUser:    options.sshUser,
		SSHPort:    options.sshPort,
		PrivateKey: options.privateKey,
	})
	if err != nil {
		return err
	}
	if !options.dryRun {
		return executionUnavailable(plan.Action)
	}
	return printPlan(cmd, plan)
}
