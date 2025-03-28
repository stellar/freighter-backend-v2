package cmd

import (
	"github.com/stellar/freighter-backend-v2/internal/config"
	"github.com/spf13/cobra"
)

type serveCmd struct {}

func (s *serveCmd) Command() *cobra.Command {
	cfg := config.Config{}
	cmd := &cobra.Command{
		Use:           "serve",
		Short:         "Start the server",
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return initializeConfig(cmd)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return s.Run()
		},
	}
	cmd.Flags().StringVar(&cfg.RpcConfig.RpcPubnetURL, "rpc-pubnet-url", "", "The URL of the pubnet RPC")
	cmd.Flags().StringVar(&cfg.RpcConfig.RpcTestnetURL, "rpc-testnet-url", "", "The URL of the testnet RPC")
	cmd.Flags().StringVar(&cfg.HorizonConfig.HorizonPubnetURL, "horizon-pubnet-url", "", "The URL of the pubnet Horizon")
	cmd.Flags().StringVar(&cfg.HorizonConfig.HorizonTestnetURL, "horizon-testnet-url", "", "The URL of the testnet Horizon")
	return cmd
}

func (s *serveCmd) Run() error {
	return nil
}
