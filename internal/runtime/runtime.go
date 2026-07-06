package runtime

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

var Version = "dev"

func SetupLogger() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
}

func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
