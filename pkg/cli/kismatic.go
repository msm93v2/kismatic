package cli

import (
	"io"

	"github.com/spf13/cobra"
)

// NewKismaticCommand creates the kismatic command
func NewKismaticCommand(version string, buildDate string, in io.Reader, out, stderr io.Writer) (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:   "kismatic",
		Short: "kismatic is the main tool for managing your Kubernetes cluster",
		Long: `kismatic is the main tool for managing your Kubernetes cluster
more documentation is available at https://github.com/apprenda/kismatic`,
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(NewCmdVersion(buildDate, out))
	cmd.AddCommand(NewCmdInstall(in, out))
	cmd.AddCommand(NewCmdVolume(in, out))
	cmd.AddCommand(NewCmdIP(out))
	cmd.AddCommand(NewCmdDashboard(in, out))
	cmd.AddCommand(NewCmdSSH(out))
	cmd.AddCommand(NewCmdInfo(out))
	cmd.AddCommand(NewCmdUpgrade(in, out))
	cmd.AddCommand(NewCmdDiagnostic(out))
	cmd.AddCommand(NewCmdCertificates(out))
	cmd.AddCommand(NewCmdSeedRegistry(out, stderr))

	return cmd, nil
}
