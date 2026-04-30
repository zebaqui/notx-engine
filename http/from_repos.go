package http

import (
	"log/slog"
	"net/http"

	"github.com/zebaqui/notx-engine/config"
	"github.com/zebaqui/notx-engine/repo"
	"github.com/zebaqui/notx-engine/service"
	"github.com/zebaqui/notx-engine/snip"
)

// NewFromRepos builds an HTTP handler directly from repository interfaces.
// This is the public constructor for embedding the engine in a host application
// without the overhead of the gRPC transport layer.
func NewFromRepos(
	cfg *config.Config,
	notes repo.NoteRepository,
	projects repo.ProjectRepository,
	ctxRepo repo.ContextRepository,
	links repo.LinkRepository,
	props repo.PropSchemaRepo,
	plugins []snip.SnipPlugin,
	log *slog.Logger,
) http.Handler {
	if log == nil {
		log = slog.Default()
	}

	eng := service.New(notes, projects, ctxRepo, links, props, 0, 0)

	return New(cfg, eng.Notes, eng.Projects, eng.Folders, eng.Context, eng.Links, log, plugins, eng.Props)
}
