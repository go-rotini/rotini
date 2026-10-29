package edge

import "github.com/spf13/cobra"

func addServer(parent *cobra.Command) {
	server := &cobra.Command{Use: "server", Short: "Manage the server", Hidden: true, Annotations: map[string]string{"owner": "ops"}}
	server.PersistentFlags().String("region", "", "server region")
	server.AddCommand(newStart(), newStop())
	parent.AddCommand(server)
}

func newStart() *cobra.Command {
	c := &cobra.Command{
		Use:        "start MODE",
		Short:      "Start the server",
		Version:    "2.0.0",
		Args:       cobra.ExactArgs(1),
		ValidArgs:  []string{"fast", "safe"},
		ArgAliases: []string{"quick"},
		RunE:       func(*cobra.Command, []string) error { return nil },
	}
	c.Flags().String("token", "", "auth token")
	c.Flags().String("token-file", "", "auth token file")
	c.MarkFlagsRequiredTogether("token", "token-file")
	c.Flags().Int("port", 8080, "listen port")
	_ = c.MarkFlagRequired("port")
	c.Flags().String("tls-cert", "", "certificate")
	c.Flags().Bool("insecure", false, "no TLS")
	c.MarkFlagsMutuallyExclusive("tls-cert", "insecure")
	c.MarkFlagsOneRequired("tls-cert", "insecure")
	return c
}

func newStop() *cobra.Command {
	c := &cobra.Command{Use: "stop", Aliases: []string{"halt", "kill"}, Short: "Stop the server", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { return nil }}
	c.Flags().CountP("force", "f", "force harder (repeatable)")
	c.Flags().String("signal", "TERM", "signal to send")
	_ = c.RegisterFlagCompletionFunc("signal", cobra.FixedCompletions([]string{"TERM", "KILL", "INT"}, cobra.ShellCompDirectiveNoFileComp))
	return c
}

func newCopy() *cobra.Command {
	c := &cobra.Command{Use: "copy SRC... DST", Short: "Copy files", Args: cobra.MinimumNArgs(2), DisableFlagsInUseLine: true,
		RunE: func(*cobra.Command, []string) error { return nil }}
	c.Flags().BoolP("recursive", "R", false, "copy directories")
	c.Flags().Lookup("recursive").NoOptDefVal = "true"
	c.Flags().String("mode", "0644", "file mode")
	c.LocalNonPersistentFlags()
	return c
}

func newGet() *cobra.Command {
	return &cobra.Command{Use: "get [key]", Short: "Get a key", Args: cobra.MaximumNArgs(1), ValidArgs: []string{"name", "size"},
		RunE: func(*cobra.Command, []string) error { return nil }}
}
