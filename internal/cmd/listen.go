package cmd

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"nosygit/internal/config"
	"nosygit/internal/events"
	"nosygit/internal/storage"
)

var listenPort int
var listenHost string

func init() {
	runCmd.AddCommand(listenCmd)
	listenCmd.Flags().IntVar(&listenPort, "port", 0, "Port to listen on (overrides config webhook.port, default 8080)")
	listenCmd.Flags().StringVar(&listenHost, "host", "", "Host/address to bind to (overrides config webhook.host)")
}

var listenCmd = &cobra.Command{
	Use:   "listen",
	Short: "Start an HTTP server to receive incoming webhooks from GitHub and GitLab.",
	Long:  "Start an HTTP server that accepts webhook deliveries from GitHub and GitLab providers, processes them with full git analysis, and emits watcher events.",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.OpenConfig("./config.yml")
		if err != nil {
			log.Fatalf("failed to open config: %s", err)
		}
		state, err := config.OpenState(statePath)
		if err != nil {
			log.Fatalf("failed to open state: %s", err)
		}
		store, err := openStore(statePath)
		if err != nil {
			log.Fatalf("failed to open store: %s", err)
		}

		port := listenPort
		if port == 0 {
			port = cfg.Webhook.Port
		}
		if port == 0 {
			port = 8080
		}

		host := listenHost
		if host == "" {
			host = cfg.Webhook.Host
		}

		baseCtx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
			Config:  cfg,
			State:   state,
			Store:   store,
			Verbose: verbose,
		})

		events.EmitMetaInfo(baseCtx, "listen", "WEBHOOK_SERVER_START", "starting webhook server", map[string]interface{}{
			"host":      host,
			"port":      port,
			"providers": len(cfg.Providers),
		})

		mux := http.NewServeMux()
		mux.HandleFunc("POST /webhook/{provider}", func(w http.ResponseWriter, r *http.Request) {
			handleWebhook(w, r, cfg, state, store)
		})

		addr := fmt.Sprintf("%s:%d", host, port)
		log.Printf("nosygit listening on %s\n", addr)
		srv := &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		if err := srv.ListenAndServe(); err != nil {
			log.Fatalf("webhook server error: %s", err)
		}
	},
}

func handleWebhook(w http.ResponseWriter, r *http.Request, cfg config.Config, state config.State, store *storage.Store) {
	requestedProvider := r.PathValue("provider")
	if providerName != "" && requestedProvider != providerName {
		http.Error(w, fmt.Sprintf("provider %q not served by this instance", requestedProvider), http.StatusNotFound)
		return
	}
	provider, ok := cfg.GetProvider(requestedProvider)
	if !ok {
		http.Error(w, fmt.Sprintf("unknown provider %q", providerName), http.StatusNotFound)
		return
	}

	ctx := context.WithValue(r.Context(), "ctx", config.ContextStruct{
		Config:   cfg,
		State:    state,
		Provider: provider,
		Store:    store,
		Verbose:  verbose,
	})

	switch provider.Driver {
	case "github":
		handleGitHubWebhook(w, r, ctx, provider, store)
	case "gitlab":
		handleGitLabWebhook(w, r, ctx, provider, store)
	default:
		http.Error(w, fmt.Sprintf("provider driver %q does not support webhooks", provider.Driver), http.StatusNotFound)
	}
}
