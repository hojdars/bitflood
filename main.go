package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/hojdars/bitflood/client"
	"github.com/hojdars/bitflood/logging"
)

const LogFile = "testdata/log/all.log"
const Port = 6881

func main() {
	if len(os.Args) != 2 {
		fmt.Printf("invalid number of arguments, expected 2, got %v", len(os.Args))
		os.Exit(1)
	}

	f, err := os.OpenFile(LogFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
	if err != nil {
		fmt.Printf("error opening '%s', err=%s", LogFile, err)
		os.Exit(1)
	}

	logWriter := io.MultiWriter(f, os.Stderr)

	logOptions := &slog.HandlerOptions{
		Level:       slog.LevelDebug,
		AddSource:   false,
		ReplaceAttr: nil,
	}

	prettyHandler := logging.New(logOptions, logging.WithColor(), logging.WithDestinationWriter(logWriter))
	logger := slog.New(prettyHandler)
	slog.SetDefault(logger)

	filename := os.Args[1]

	client.Main(filename, Port)

	slog.Info("teardown complete, exit", slog.Int("code", 0))
	os.Exit(0)
}
