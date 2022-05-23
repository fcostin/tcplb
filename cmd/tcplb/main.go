package main

import (
	"os"
	"tcplb/lib/slog"
)

func main() {
	logger := slog.GetDefaultLogger()

	cfg, err := newConfigFromFlags(os.Args)
	if err != nil {
		logger.Error(&slog.LogRecord{Msg: "failed to parse flags", Error: err})
		os.Exit(2)
	}

	logger.Info(&slog.LogRecord{Msg: "loaded config", Details: cfg})

	err = cfg.Validate()
	if err != nil {
		logger.Error(&slog.LogRecord{Msg: "configuration is invalid", Error: err})
		os.Exit(2)
	}

	server, err := NewServer(logger, cfg)
	if err != nil {
		logger.Error(&slog.LogRecord{Msg: "failed to create server", Error: err})
		os.Exit(1)
	}
	err = server.Serve()
	if err != nil {
		logger.Error(&slog.LogRecord{Msg: "server terminated abnormally", Error: err})
		os.Exit(1)
	}
	logger.Info(&slog.LogRecord{Msg: "server terminated normally"})
	os.Exit(0)
}
