package main

import (
	"flag"
	"fmt"
	"os"

	"gateway/configs"
	"gateway/internal/app"
	"gateway/pkg/api/logger"
)

func main() {
	configPath := flag.String("config", "configs/env.yaml", "specify YAML config file to use")
	flag.Parse()
	if err := run(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "application stopped: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	config, err := configs.Load(configPath)
	if err != nil {
		return err
	}
	log, err := logger.New(os.Stdout, config.Logging)
	if err != nil {
		return fmt.Errorf("create logger: %w", err)
	}
	return app.NewApp(&config).Start(log)
}
