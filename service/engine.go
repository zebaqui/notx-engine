package service

import (
	"github.com/zebaqui/notx-engine/repo"
	"github.com/zebaqui/notx-engine/snip"
)

// Engine groups all service instances for the notx engine.
//
// It is the primary entry point for embedded use: create one with New and
// access the domain services via its exported fields.  Any service whose
// backing repository is nil at construction time is left nil in the Engine —
// callers should guard with a nil check before using optional services.
//
// Example (embedded use without HTTP or gRPC):
//
//	eng := service.New(noteRepo, projRepo, ctxRepo, linkRepo, propRepo, 0, 0)
//	note, events, err := eng.Notes.Get(ctx, urn)
type Engine struct {
	// Notes handles note lifecycle, event history, content search, and snip hooks.
	Notes NoteService

	// Projects handles project CRUD.
	Projects ProjectService

	// Folders handles folder CRUD (backed by the same repo as Projects).
	Folders FolderService

	// Context handles the context graph: bursts, candidates, and inferences.
	// Nil when no ContextRepository is provided.
	Context ContextService

	// Links handles anchor, backlink, and external-link management.
	// Nil when no LinkRepository is provided.
	Links LinkService

	// Props handles prop schema CRUD.
	// Nil when no PropSchemaRepo is provided.
	Props PropService
}

// New constructs an Engine from the provided repositories.
//
//   - notes and projects are required; passing nil causes a panic.
//   - context, links, and props are optional; their corresponding service
//     fields are left nil when the repository is nil.
//   - defaultPage / maxPage control list pagination across all services.
//     Pass 0 for both to use the built-in defaults (50 / 200).
func New(
	notes repo.NoteRepository,
	projects repo.ProjectRepository,
	context repo.ContextRepository,
	links repo.LinkRepository,
	props repo.PropSchemaRepo,
	defaultPage, maxPage int,
) *Engine {
	e := &Engine{}

	if notes != nil {
		e.Notes = newNoteService(notes, context, defaultPage, maxPage)
	}
	if projects != nil {
		e.Projects = newProjectService(projects, defaultPage, maxPage)
		e.Folders = newFolderService(projects, defaultPage, maxPage)
	}
	if context != nil {
		e.Context = newContextService(context, defaultPage, maxPage)
	}
	if links != nil {
		e.Links = newLinkService(links)
	}
	if props != nil {
		e.Props = newPropService(props)
	}

	return e
}

// WireSnipRegistry attaches a snip plugin registry to the NoteService so that
// plugin hooks (OnNoteCreated, OnEventAppended) are dispatched after writes.
// Must be called after New and before the engine begins serving requests.
// Safe to call even when Engine.Notes is nil (no-op in that case).
func (e *Engine) WireSnipRegistry(r *snip.Registry) {
	if e.Notes != nil {
		e.Notes.SetSnipRegistry(r)
	}
}
