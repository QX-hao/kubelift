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
	"github.com/QX-hao/kubelift/internal/localexec"
	"github.com/QX-hao/kubelift/internal/preflight"
	"github.com/QX-hao/kubelift/internal/remote"
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
	resume     bool
	timeout    time.Duration
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
	command.Flags().BoolVar(&options.resume, "resume", false, "continue a previously interrupted node addition")
	command.Flags().DurationVar(&options.timeout, "timeout", 60*time.Minute, "maximum time to add the node")
}

func runAdd(cmd *cobra.Command, role workflow.Role, address string, options addCommandOptions) error {
	if options.dryRun && options.resume {
		return fmt.Errorf("--dry-run and --resume cannot be used together")
	}
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
		if options.timeout <= 0 {
			return fmt.Errorf("add %s timeout must be greater than zero", role)
		}
		if err := requireLocalPreflight(*configuration); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), options.timeout)
		defer cancel()
		client, err := remote.Connect(ctx, remote.Target{
			Address: plan.Target.Address, User: plan.Target.SSHUser, Port: plan.Target.SSHPort,
			PrivateKeyPath: plan.Target.PrivateKey,
		})
		if err != nil {
			return err
		}
		defer client.Close()
		if err := requireRemotePreflight(ctx, client, *configuration); err != nil {
			return err
		}
		// 恢复执行时节点可能已经完成 join，Kubernetes 端口被占用属于正常状态。
		if !options.resume {
			ports := []int{10250}
			if role == workflow.RoleMaster {
				ports = []int{2379, 2380, 6443, 10250, 10257, 10259}
			}
			if result := preflight.CheckRemotePorts(ctx, client, ports); result.Err != nil {
				return fmt.Errorf("remote preflight %s failed: %w", result.Name, result.Err)
			}
		}
		remoteRoot := path.Join("/var/lib/kubelift/staging", configuration.Metadata.Name)
		if role == workflow.RoleNode {
			result, err := (workflow.AddNodeExecutor{
				MasterRunner: localexec.Runner{}, Remote: client, RemoteRoot: remoteRoot,
				RemoteJoinConfig: "/etc/kubernetes/kubelift-join.yaml", AdminKubeconfig: "/etc/kubernetes/admin.conf",
				StateRoot: "/var/lib/kubelift/state",
			}).Execute(ctx, *configuration, *plan.Target, workflow.JoinOptions{Resume: options.resume})
			if err != nil {
				return err
			}
			if result.AlreadyComplete {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Worker %q (%s) addition is already complete.\n", result.NodeName, plan.Target.Address)
				return err
			}
			if result.KubeadmOutput != "" {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), result.KubeadmOutput); err != nil {
					return err
				}
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Worker %q (%s) joined cluster %q successfully.\n", result.NodeName, plan.Target.Address, configuration.Metadata.Name)
			return err
		}
		result, err := (workflow.AddMasterExecutor{
			MasterRunner: localexec.Runner{}, Remote: client, RemoteRoot: remoteRoot,
			RemoteJoinConfig: "/etc/kubernetes/kubelift-join.yaml", AdminKubeconfig: "/etc/kubernetes/admin.conf",
			StateRoot: "/var/lib/kubelift/state",
		}).Execute(ctx, *configuration, *plan.Target, workflow.JoinOptions{Resume: options.resume})
		if err != nil {
			return err
		}
		if result.AlreadyComplete {
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Control-plane node %q (%s) addition is already complete.\n", result.NodeName, plan.Target.Address)
			return err
		}
		if result.KubeadmOutput != "" {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), result.KubeadmOutput); err != nil {
				return err
			}
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Control-plane node %q (%s) joined cluster %q successfully.\n", result.NodeName, plan.Target.Address, configuration.Metadata.Name)
		return err
	}
	return printPlan(cmd, plan)
}
