package http

import (
	"log/slog"
	"net/http"

	"github.com/zebaqui/notx-engine/config"
	grpcsvc "github.com/zebaqui/notx-engine/internal/server/grpc"
	"github.com/zebaqui/notx-engine/repo"
	"github.com/zebaqui/notx-engine/snip"
)

// NewFromRepos builds an HTTP handler directly from repository interfaces.
// This is the public constructor for embedding the engine in a host application.
func NewFromRepos(
	cfg *config.Config,
	notes repo.NoteRepository,
	projects repo.ProjectRepository,
	context repo.ContextRepository,
	links repo.LinkRepository,
	plugins []snip.SnipPlugin,
	log *slog.Logger,
) http.Handler {
	noteSvc := grpcsvc.NewNoteServerWithContext(notes, context, 0, 0)
	projSvc := grpcsvc.NewProjectServer(projects, 0, 0)
	folderSvc := grpcsvc.NewFolderServer(projects, 0, 0)
	if log == nil {
		log = slog.Default()
	}
	var contextSvc *grpcsvc.ContextServer
	if context != nil {
		contextSvc = grpcsvc.NewContextServer(context, 0, 0)
	}
	var linkSvc *grpcsvc.LinkServer
	if links != nil {
		linkSvc = grpcsvc.NewLinkServer(links)
	}
	return New(cfg, noteSvc, projSvc, folderSvc, contextSvc, linkSvc, log, plugins)
}
