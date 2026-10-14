package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/portainer/kubesolo/internal/cli/service"
	"github.com/spf13/cobra"
)

func listCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the KubeSolo container instances on the container engine",
		RunE: func(cmd *cobra.Command, args []string) error {
			instances, err := service.ListInstances()
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if instances == nil {
					instances = []service.Instance{}
				}
				return enc.Encode(instances)
			}
			if len(instances) == 0 {
				fmt.Println("No KubeSolo container instances found.")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			_, _ = fmt.Fprintln(w, "NAME\tCONTAINER\tIMAGE\tSTATE\tAPI SERVER")
			for _, i := range instances {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", i.Name, i.Container, i.Image, i.State, i.APIServer)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the instances as JSON")
	return cmd
}
