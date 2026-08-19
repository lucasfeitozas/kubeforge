package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/lucasfeitozas/kubeforge/internal/k8s"
)

type config struct {
	httpPort   int
	kubeconfig string
	dbPath     string
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	cfg := loadConfig()

	slog.Info("kubeforge iniciado",
		"http_port", cfg.httpPort,
		"kubeconfig", cfg.kubeconfig,
		"db_path", cfg.dbPath,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	provider := k8s.NewMinikubeProvider(cfg.kubeconfig)
	clientset, err := provider.GetClientset(ctx, k8s.MinikubeClusterKey)
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
