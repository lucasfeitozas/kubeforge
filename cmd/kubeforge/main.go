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
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/lucasfeitozas/kubeforge/internal/api"
	"github.com/lucasfeitozas/kubeforge/internal/build"
	"github.com/lucasfeitozas/kubeforge/internal/controller"
	"github.com/lucasfeitozas/kubeforge/internal/k8s"
	"github.com/lucasfeitozas/kubeforge/internal/store"
	"github.com/lucasfeitozas/kubeforge/web"
)

type config struct {
	httpPort   int
	kubeconfig string
	dbPath     string
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	loadDotenv()
	// Reconfigurado após loadDotenv: LOG_LEVEL pode vir do .env, igual às
	// demais KUBEFORGE_* env vars lidas por loadConfig/runCleanup.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel()})))

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
		"log_level", envString("LOG_LEVEL", "info"),
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

	components := store.NewComponentRepository(db)
	executions := store.NewExecutionRepository(db)
	broker := &build.Broker{
		Cloner:     build.NewGitCloner(),
		Builder:    build.NewDockerBuilder(),
		Components: components,
		Executions: executions,
	}
	runner := &controller.Runner{
		ClusterProvider: provider,
		Components:      components,
		Executions:      executions,
	}

	apiServer := api.NewServer(components, provider, store.NewCleanupAuditRepository(db), broker, runner, db, cfg.dbPath, web.StaticFS())
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

// loadDotenv carrega variáveis de um arquivo .env no diretório atual para o
// ambiente do processo, se ele existir — conveniência local para não exigir
// export manual das KUBEFORGE_* antes de rodar o binário (docs/ARCHITECTURE.md
// §7, perfil 100% local). godotenv.Load não sobrescreve uma env var já
// definida no processo (ex.: setada explicitamente no shell), então .env só
// preenche o que ainda não foi definido. A ausência do arquivo não é um erro.
func loadDotenv() {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		slog.Warn("falha ao carregar .env", "error", err)
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

// logLevel resolve o nível mínimo de log a partir de LOG_LEVEL (debug, info,
// warn/warning, error — case-insensitive; ver .env.example). "info" é o
// default quando a env var está ausente ou tem um valor não reconhecido.
func logLevel() slog.Level {
	raw := envString("LOG_LEVEL", "info")
	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		slog.Warn("valor inválido para LOG_LEVEL, usando default", "value", raw, "default", "info")
		return slog.LevelInfo
	}
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
