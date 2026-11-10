package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"code.cestc.cn/ai-socket/server-go/version"
	"echo-service/internal/auth"
	"echo-service/internal/client"
	"echo-service/internal/config"
	"echo-service/internal/storage"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

func main() {
	var path string
	root := &cobra.Command{Use: "echoservice", Short: "Echo service administration", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(&cobra.Command{Use: "version", Args: cobra.NoArgs, Run: func(cmd *cobra.Command, args []string) { fmt.Fprintln(cmd.OutOrStdout(), version.GetVersionString()) }})
	check := &cobra.Command{Use: "check-config", Short: "Validate configuration without connecting to infrastructure", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		v := viper.New()
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("cannot read config: check path and TOML syntax")
		}
		if _, err := config.Load(v); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Configuration is valid")
		return nil
	}}
	defaultPath := os.Getenv("SERVER_CONFIG_PATH")
	if defaultPath == "" {
		defaultPath = "conf/server/config.toml"
	}
	check.Flags().StringVar(&path, "config", defaultPath, "TOML config file")
	root.AddCommand(check)
	var username string
	bootstrap := &cobra.Command{Use: "bootstrap-admin", Short: "Create the first administrator with an interactive password", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		v := viper.New()
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("cannot read configuration")
		}
		cfg, err := config.Load(v)
		if err != nil {
			return err
		}
		clients, err := client.Open(cfg.Database)
		if err != nil {
			return err
		}
		defer clients.Close()
		if err = storage.Migrate(clients.Database); err != nil {
			return err
		}
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return fmt.Errorf("bootstrap-admin requires an interactive terminal")
		}
		fmt.Fprint(cmd.ErrOrStderr(), "Password (12..256 bytes): ")
		password, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		defer clear(password)
		fmt.Fprint(cmd.ErrOrStderr(), "Confirm password: ")
		confirm, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		defer clear(confirm)
		if string(password) != string(confirm) {
			return fmt.Errorf("passwords do not match")
		}
		u, err := auth.New(clients.Database, cfg.Auth).BootstrapAdmin(context.Background(), strings.TrimSpace(username), string(password))
		if err != nil {
			return fmt.Errorf("administrator creation failed; database must have no users")
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Administrator created:", u.Username)
		return nil
	}}
	bootstrap.Flags().StringVar(&username, "username", "admin", "Initial administrator username")
	bootstrap.Flags().StringVar(&path, "config", defaultPath, "TOML config file")
	root.AddCommand(bootstrap)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
