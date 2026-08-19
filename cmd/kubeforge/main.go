package main

import (
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
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
