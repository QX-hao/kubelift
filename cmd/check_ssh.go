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
	"net/netip"
	"time"

	"github.com/QX-hao/kubelift/internal/bundle"
	"github.com/QX-hao/kubelift/internal/config"
	"github.com/QX-hao/kubelift/internal/preflight"
	"github.com/QX-hao/kubelift/internal/remote"
	"github.com/spf13/cobra"
)

var (
	checkSSHConfigPath = defaultClusterConfigPath
	checkSSHTimeout    = 15 * time.Second
)

var checkSSHCmd = &cobra.Command{
	Use:   "ssh <IPv4>",
	Short: "Test an SSH connection to a remote node",
	Long: `Connect to a remote node with the private key from the cluster
configuration and run read-only host preflight checks. Password and
keyboard-interactive authentication are disabled.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if checkSSHTimeout <= 0 {
			return fmt.Errorf("SSH timeout must be greater than zero")
		}
		address, err := parseSSHAddress(args[0])
		if err != nil {
			return err
		}
		configuration, err := config.Load(checkSSHConfigPath)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), checkSSHTimeout)
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

		results := preflight.CheckRemote(ctx, client)
		bundleReport, err := bundle.Inspect(configuration.Spec.Offline.Bundle)
		if err != nil {
			return err
		}
		results = append(results, preflight.CheckRemoteBundle(ctx, client, bundleReport.Manifest)...)
		failed := false
		for _, result := range results {
			if result.Err != nil {
				failed = true
				break
			}
		}

		output := cmd.OutOrStdout()
		if failed {
			output = cmd.ErrOrStderr()
		}
		if _, err := fmt.Fprintf(
			output,
			"SSH connection succeeded: %s@%s:%d\n",
			configuration.Spec.SSH.User,
			address,
			configuration.Spec.SSH.Port,
		); err != nil {
			return err
		}
		for _, result := range results {
			status := "PASS"
			detail := result.Detail
			if result.Err != nil {
				status = "FAIL"
				detail = result.Err.Error()
			}
			if _, err := fmt.Fprintf(output, "[%s] remote %s: %s\n", status, result.Name, detail); err != nil {
				return err
			}
		}
		if failed {
			return fmt.Errorf("remote preflight checks failed")
		}
		return nil
	},
}

func init() {
	checkCmd.AddCommand(checkSSHCmd)
	checkSSHCmd.Flags().StringVarP(
		&checkSSHConfigPath,
		"config",
		"f",
		defaultClusterConfigPath,
		"path to the cluster configuration file",
	)
	checkSSHCmd.Flags().DurationVar(
		&checkSSHTimeout,
		"timeout",
		checkSSHTimeout,
		"maximum time to wait for the SSH check",
	)
}

func parseSSHAddress(value string) (netip.Addr, error) {
	address, err := netip.ParseAddr(value)
	if err != nil || !address.Is4() || address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() || address.IsLinkLocalUnicast() {
		return netip.Addr{}, fmt.Errorf("SSH target must be a usable IPv4 address")
	}
	return address, nil
}
