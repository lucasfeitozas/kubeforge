package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/lucasfeitozas/kubeforge/internal/api"
	"github.com/lucasfeitozas/kubeforge/internal/k8s"
	"github.com/lucasfeitozas/kubeforge/internal/store"
)

type config struct {
	httpPort   int
	kubeconfig string
	dbPath     string
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	if len(os.Args) > 1 && os.Args[1] == "cleanup" {
		runCleanup(os.Args[2:])
		return
	}
	runServer()
}

func runServer() {
	cfg := loadConfig()

	slog.Info("kubeforge iniciado",
		"http_port", cfg.httpPort,
		"kubeconfig", cfg.kubeconfig,
		"db_path", cfg.dbPath,
	)

	db, err := store.Open(cfg.dbPath)
	if err != nil {
		slog.Error("falha ao abrir banco SQLite", "db_path", cfg.dbPath, "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := store.Migrate(db); err != nil {
		slog.Error("falha ao aplicar migrations", "db_path", cfg.dbPath, "error", err)
		os.Exit(1)
	}
	slog.Info("migrations aplicadas com sucesso", "db_path", cfg.dbPath)

	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStartup()

	provider := k8s.NewMinikubeProvider(cfg.kubeconfig)
	clientset, err := provider.GetClientset(startupCtx, k8s.MinikubeClusterKey)
	if err != nil {
		slog.Error("falha ao conectar ao cluster Minikube", "kubeconfig", cfg.kubeconfig, "error", err)
		os.Exit(1)
	}

	serverVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		slog.Error("falha ao obter versão do cluster Kubernetes", "error", err)
		os.Exit(1)
	}

	slog.Info("conectado ao cluster Minikube",
		"kubernetes_version", serverVersion.GitVersion,
		"platform", serverVersion.Platform,
	)

	apiServer := api.NewServer(store.NewComponentRepository(db), provider, store.NewCleanupAuditRepository(db))
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.httpPort),
		Handler: apiServer,
	}

	serveErrCh := make(chan error, 1)
	go func() {
		slog.Info("servidor HTTP escutando", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrCh <- err
			return
		}
		serveErrCh <- nil
	}()

	sigCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	select {
	case <-sigCtx.Done():
		slog.Info("sinal de encerramento recebido, desligando servidor HTTP")
	case err := <-serveErrCh:
		if err != nil {
			slog.Error("falha ao iniciar servidor HTTP", "addr", httpServer.Addr, "error", err)
			os.Exit(1)
		}
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("erro ao desligar servidor HTTP", "error", err)
		os.Exit(1)
	}
}

func loadConfig() config {
	httpPort := flag.Int("http-port", envInt("KUBEFORGE_HTTP_PORT", 8080), "porta HTTP do servidor")
	kubeconfig := flag.String("kubeconfig", envString("KUBEFORGE_KUBECONFIG", defaultKubeconfig()), "caminho do kubeconfig")
	dbPath := flag.String("db-path", envString("KUBEFORGE_DB_PATH", "./kubeforge.db"), "caminho do arquivo SQLite")
	flag.Parse()

	return config{
		httpPort:   *httpPort,
		kubeconfig: *kubeconfig,
		dbPath:     *dbPath,
	}
}

func defaultKubeconfig() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kube", "config")
}

func envString(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("valor inválido para env var, usando default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return n
}
