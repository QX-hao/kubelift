package cmd

import (
	"fmt"
	"strings"

	"github.com/QX-hao/kubelift/internal/workflow"
	"github.com/spf13/cobra"
)

func printPlan(cmd *cobra.Command, plan workflow.Plan) error {
	var output strings.Builder
	fmt.Fprintf(&output, "Dry-run plan: %s\n", plan.Action)
	fmt.Fprintf(&output, "Cluster: %s\n", plan.Cluster)
	fmt.Fprintf(&output, "Kubernetes: %s\n", plan.KubernetesVersion)
	if plan.Target != nil {
		fmt.Fprintf(&output, "Target: %s (%s)\n", plan.Target.Address, plan.Target.Role)
		if plan.Target.Name != "" {
			fmt.Fprintf(&output, "Node name: %s\n", plan.Target.Name)
		}
		fmt.Fprintf(
			&output,
			"SSH: %s@%s:%d using %s\n",
			plan.Target.SSHUser,
			plan.Target.Address,
			plan.Target.SSHPort,
			plan.Target.PrivateKey,
		)
	}
	output.WriteString("Steps:\n")
	for index, step := range plan.Steps {
		fmt.Fprintf(&output, "  %d. %s: %s\n", index+1, step.Name, step.Description)
	}

	_, err := fmt.Fprint(cmd.OutOrStdout(), output.String())
	return err
}

func executionUnavailable(action string) error {
	return fmt.Errorf("%s execution is not enabled yet; use --dry-run to inspect the validated plan", action)
}
