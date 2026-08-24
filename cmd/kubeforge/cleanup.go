package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/lucasfeitozas/kubeforge/internal/controller"
	"github.com/lucasfeitozas/kubeforge/internal/k8s"
	"github.com/lucasfeitozas/kubeforge/internal/store"
)

// defaultCleanupNamespace é usado quando `kubeforge cleanup` não recebe
// --namespace — mesmo valor de internal/controller.defaultNamespace, mas
// mantido como constante própria: cleanup --all é namespace-wide, não
// associado a um Componente/targetContext.
const defaultCleanupNamespace = "default"

// runCleanup implementa `kubeforge cleanup --all` (E5.S2): remove todo Job,
// Pod e PersistentVolumeClaim rotulado kubeforge.io/managed=true no
// namespace informado, registrando cada remoção no log de auditoria.
func runCleanup(args []string) {
	fs := flag.NewFlagSet("cleanup", flag.ExitOnError)
	all := fs.Bool("all", false, "remove todos os recursos rotulados kubeforge.io/managed=true")
	namespace := fs.String("namespace", envString("KUBEFORGE_NAMESPACE", defaultCleanupNamespace), "namespace a limpar")
	kubeconfig := fs.String("kubeconfig", envString("KUBEFORGE_KUBECONFIG", defaultKubeconfig()), "caminho do kubeconfig")
	dbPath := fs.String("db-path", envString("KUBEFORGE_DB_PATH", "./kubeforge.db"), "caminho do arquivo SQLite")
	fs.Parse(args)

	if !*all {
		fmt.Fprintln(os.Stderr, "uso: kubeforge cleanup --all")
		os.Exit(1)
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		slog.Error("falha ao abrir banco SQLite", "db_path", *dbPath, "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := store.Migrate(db); err != nil {
		slog.Error("falha ao aplicar migrations", "db_path", *dbPath, "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	provider := k8s.NewMinikubeProvider(*kubeconfig)
	results, runErr := controller.RunCleanup(ctx, provider, k8s.MinikubeClusterKey, *namespace)

	audit := store.NewCleanupAuditRepository(db)
	if auditErr := controller.PersistCleanupAudit(ctx, audit, results); auditErr != nil {
		slog.Error("falha ao registrar auditoria de cleanup", "error", auditErr)
	}

	for _, res := range results {
		slog.Info("recurso removido", "kind", res.Kind, "name", res.Name, "namespace", res.Namespace)
	}
	fmt.Printf("%d recurso(s) removido(s) no namespace %q\n", len(results), *namespace)

	if runErr != nil {
		slog.Error("cleanup interrompido por erro", "namespace", *namespace, "error", runErr)
		os.Exit(1)
	}
}
