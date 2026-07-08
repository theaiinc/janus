package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/theaiinc/janus/internal/api"
	"github.com/theaiinc/janus/internal/app"
	"github.com/theaiinc/janus/internal/config"
	"github.com/theaiinc/janus/internal/mcp"
)

var (
	version = "dev"
	commit  = "none"
)

func NewRootCommand() *cobra.Command {
	var configPath string

	root := &cobra.Command{
		Use:   "janus",
		Short: "Intelligent guardian for Cloudflared tunnels",
	}
	root.PersistentFlags().StringVarP(&configPath, "config", "c", "janus.yaml", "path to Janus YAML configuration")

	root.AddCommand(runCommand(&configPath))
	root.AddCommand(mcpCommand())
	root.AddCommand(validateConfigCommand(&configPath))
	root.AddCommand(versionCommand())
	return root
}

func Execute() error {
	return NewRootCommand().Execute()
}

func runCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run the Janus daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}

			janus, err := app.New(cfg, *configPath)
			if err != nil {
				return err
			}
			server := api.New(cfg.Server.Address, janus)
			janus.SetAPIServer(server)

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			fmt.Fprintf(cmd.OutOrStdout(), "janus listening on %s\n", cfg.Server.Address)
			err = janus.Run(ctx)
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		},
	}
}

func mcpCommand() *cobra.Command {
	var baseURL string
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the Janus MCP server over stdio",
		Long:  "Run a Model Context Protocol server over stdio so agents can inspect and operate a running Janus daemon through its REST API.",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := mcp.NewJanusClient(baseURL, timeout)
			if err != nil {
				return err
			}
			server := mcp.NewServer(client)
			return server.Serve(cmd.Context(), os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&baseURL, "base-url", "http://127.0.0.1:8088", "Janus daemon base URL")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second, "Janus API request timeout")
	return cmd
}

func validateConfigCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "validate-config",
		Short: "Validate the Janus configuration file",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "configuration valid: %d tunnel(s), %d service(s)\n", len(cfg.Tunnels), len(cfg.Services))
			return nil
		},
	}
}

func versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print Janus version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "janus %s (%s)\n", version, commit)
			return nil
		},
	}
}
