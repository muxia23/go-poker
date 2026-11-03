package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/evanofslack/go-poker/server"
	"github.com/joho/godotenv"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := godotenv.Load(); err != nil {
		slog.Default().Info("no .env file loaded", "error", err)
	}

	// reset-root sets a temporary root password, for when it's lost
	if len(os.Args) > 1 && os.Args[1] == "reset-root" {
		username, password, err := server.ResetRoot(ctx)
		if err != nil {
			slog.Default().Error("reset root", "error", err)
			os.Exit(1)
		}
		fmt.Printf("root %s now has the temporary password %s\nit has to be changed at the next login\n", username, password)
		return
	}

	s, err := server.New()
	if err != nil {
		slog.Default().Error("create http server", "error", err)
		os.Exit(1)
	}

	select {
	case err := <-s.Run():
		slog.Default().Error("http server", "error", err)
		os.Exit(1)
	case <-ctx.Done():
	}

	slog.Default().Info("Shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Shutdown(shutdownCtx); err != nil {
		slog.Default().Error("shutdown http server", "error", err)
	}
}
