package http

import (
	"log/slog"
	"net/http"

	"github.com/zebaqui/notx-engine/config"
	grpcsvc "github.com/zebaqui/notx-engine/internal/server/grpc"
	"github.com/zebaqui/notx-engine/repo"
)

// NewFromRepos builds an HTTP handler from repository interfaces rather than
// the internal grpc service types. This is the public constructor intended for
// embedding the engine in a host application (e.g. the notx platform server)
// that cannot import the internal/server/grpc package.
//
// All repository arguments may satisfy multiple interfaces on a single concrete
// type — e.g. the platform's TenantScopedProvider implements NoteRepository,
// ProjectRepository, DeviceRepository, UserRepository, and ServerRepository
// all at once.
//
// pairingSvc and relaySvc are optional; pass nil to disable those HTTP routes.
// secretStore is optional; pass nil when the pairing HTTP routes are not needed.
func NewFromRepos(
	cfg *config.Config,
	notes repo.NoteRepository,
	projects repo.ProjectRepository,
	devices repo.DeviceRepository,
	users repo.UserRepository,
	servers repo.ServerRepository,
	secretStore repo.PairingSecretStore,
	context repo.ContextRepository,
	links repo.LinkRepository,
	log *slog.Logger,
) http.Handler {
	noteSvc := grpcsvc.NewNoteServerWithContext(notes, context, 0, 0)
	projSvc := grpcsvc.NewProjectServer(projects, 0, 0)
	folderSvc := grpcsvc.NewFolderServer(projects, 0, 0)
	deviceSvc := grpcsvc.NewDeviceServer(devices)
	deviceAdminSvc := grpcsvc.NewDeviceAdminServer(devices)
	userSvc := grpcsvc.NewUserServer(users, 0, 0)
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

	return New(cfg, noteSvc, projSvc, folderSvc, deviceSvc, deviceAdminSvc, userSvc, log, nil, secretStore, nil, contextSvc, linkSvc)
}
