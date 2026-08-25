package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/store"
	"github.com/OmarAlghafri/netrewind/internal/web"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	var (
		addr       string
		observerID string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Read the record in a browser",
		Long: "The same record the CLI shows, rendered as pages. Server-rendered, no\n" +
			"JavaScript, and read-only: the store is evidence, and the thing that\n" +
			"displays evidence has no business modifying it.\n\n" +
			"It binds to loopback by default. There is no authentication, so the\n" +
			"recorder should not be reachable from the network it is watching.",
		Example: "  netrewind serve\n" +
			"  netrewind serve --addr 127.0.0.1:9000\n" +
			"  netrewind serve --db /var/lib/netrewind/events.db",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dbPath, _ := cmd.Flags().GetString("db")
			st, err := store.OpenSQLiteRead(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			log := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{
				Level: slog.LevelInfo,
			}))
			if observerID == "" {
				if h, err := os.Hostname(); err == nil {
					observerID = h
				} else {
					observerID = "netrewind"
				}
			}

			srv, err := web.New(st, log, observerID)
			if err != nil {
				return err
			}
			httpSrv := srv.Serve(addr)

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			errs := make(chan error, 1)
			go func() {
				fmt.Fprintf(cmd.OutOrStdout(), "reading %s at http://%s\n", dbPath, addr)
				if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					errs <- err
					return
				}
				errs <- nil
			}()

			select {
			case err := <-errs:
				return err
			case <-ctx.Done():
				shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				return httpSrv.Shutdown(shutdown)
			}
		},
	}
	cmd.Flags().StringVar(&addr, "addr", web.DefaultAddr, "address to listen on")
	cmd.Flags().StringVar(&observerID, "observer-id", "", "name shown in the interface; defaults to the hostname")
	return cmd
}
