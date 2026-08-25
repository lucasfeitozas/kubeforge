// Package web embute os assets do Console Web (E7.S1) no binário do
// kubeforge, para servi-los sem depender de Nginx nem de um build step
// separado. Fica em web/, não em cmd/kubeforge/, porque //go:embed não
// pode referenciar um caminho fora da árvore do pacote que contém a
// diretiva — web/static precisa continuar na raiz do repo, já documentada
// desde o Epic E1 (ver README.md, docs/adrs/0018-console-web-embed-fs.md).
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var staticFS embed.FS

// StaticFS devolve o conteúdo de web/static/ com o prefixo "static/" já
// removido dos caminhos (via fs.Sub), pronto para http.FileServer servir
// index.html na raiz do site.
func StaticFS() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// Só falha se o diretório "static" não existir no momento do build
		// (erro de build, não de runtime) — panic é apropriado aqui.
		panic(err)
	}
	return sub
}
