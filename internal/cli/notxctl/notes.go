package notxctl

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/zebaqui/notx-engine/internal/server/proto"
)

// ─────────────────────────────────────────────────────────────────────────────
// notes — top-level group
// ─────────────────────────────────────────────────────────────────────────────

var notesCmd = &cobra.Command{
	Use:   "notes",
	Short: "Manage notes (NoteService)",
	Long: `Commands for the NoteService gRPC endpoints.

Sub-commands:
  get      GetNote       — fetch a single note and all its events
  list     ListNotes     — list note headers with optional filters
  create   CreateNote    — create a new note (header only)
  delete   DeleteNote    — soft-delete a note
  search   SearchNotes   — full-text search across normal notes
  events   StreamEvents  — stream events for a note`,
}

func init() {
	notesCmd.AddCommand(notesGetCmd)
	notesCmd.AddCommand(notesListCmd)
	notesCmd.AddCommand(notesCreateCmd)
	notesCmd.AddCommand(notesDeleteCmd)
	notesCmd.AddCommand(notesSearchCmd)
	notesCmd.AddCommand(notesEventsCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// notes get <urn>
// ─────────────────────────────────────────────────────────────────────────────

var notesGetCmd = &cobra.Command{
	Use:   "get <urn>",
	Short: "Fetch a note and all its events",
	Long: `Calls GetNote and prints the header plus every event.

Example:
  notxctl notes get notx:note:1a9670dd-1a65-481a-ad17-03d77de021e5`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Notes().GetNote(ctx, &pb.GetNoteRequest{Urn: args[0]})
		if err != nil {
			return fmt.Errorf("GetNote: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			type eventOut struct {
				URN       string    `json:"urn"`
				Sequence  int32     `json:"sequence"`
				AuthorURN string    `json:"author_urn"`
				CreatedAt time.Time `json:"created_at"`
				Entries   int       `json:"entries"`
			}
			type out struct {
				URN        string     `json:"urn"`
				Name       string     `json:"name"`
				Type       string     `json:"type"`
				ProjectURN string     `json:"project_urn,omitempty"`
				FolderURN  string     `json:"folder_urn,omitempty"`
				Deleted    bool       `json:"deleted"`
				CreatedAt  time.Time  `json:"created_at"`
				UpdatedAt  time.Time  `json:"updated_at"`
				Events     []eventOut `json:"events"`
			}
			o := out{
				URN:       resp.Header.Urn,
				Name:      resp.Header.Name,
				Type:      noteTypeStr(resp.Header.NoteType),
				Deleted:   resp.Header.Deleted,
				CreatedAt: resp.Header.CreatedAt.AsTime(),
				UpdatedAt: resp.Header.UpdatedAt.AsTime(),
			}
			if resp.Header.ProjectUrn != "" {
				o.ProjectURN = resp.Header.ProjectUrn
			}
			if resp.Header.FolderUrn != "" {
				o.FolderURN = resp.Header.FolderUrn
			}
			for _, e := range resp.Events {
				o.Events = append(o.Events, eventOut{
					URN:       e.Urn,
					Sequence:  e.Sequence,
					AuthorURN: e.AuthorUrn,
					CreatedAt: e.CreatedAt.AsTime(),
					Entries:   len(e.Entries),
				})
			}
			return printJSON(o)

		default: // table
			tw := newTabWriter()
			defer tw.Flush()

			h := resp.Header
			fmt.Fprintf(tw, "URN\t%s\n", h.Urn)
			fmt.Fprintf(tw, "Name\t%s\n", h.Name)
			fmt.Fprintf(tw, "Type\t%s\n", noteTypeStr(h.NoteType))
			fmt.Fprintf(tw, "Deleted\t%s\n", fmtBool(h.Deleted, "yes", "no"))
			fmt.Fprintf(tw, "Project\t%s\n", orDash(h.ProjectUrn))
			fmt.Fprintf(tw, "Folder\t%s\n", orDash(h.FolderUrn))
			fmt.Fprintf(tw, "Created\t%s\n", fmtTime(h.CreatedAt.AsTime()))
			fmt.Fprintf(tw, "Updated\t%s\n", fmtTime(h.UpdatedAt.AsTime()))
			fmt.Fprintf(tw, "Events\t%d\n", len(resp.Events))

			if len(resp.Events) > 0 {
				fmt.Fprintln(tw)
				header(tw, "SEQ", "AUTHOR", "ENTRIES", "CREATED")
				for _, e := range resp.Events {
					row(tw,
						fmt.Sprintf("%d", e.Sequence),
						shortURN(e.AuthorUrn),
						fmt.Sprintf("%d", len(e.Entries)),
						fmtTime(e.CreatedAt.AsTime()),
					)
				}
			}
		}
		return nil
	},
}

// ─────────────────────────────────────────────────────────────────────────────
// notes list
// ─────────────────────────────────────────────────────────────────────────────

var notesListFlags struct {
	projectURN     string
	folderURN      string
	noteType       string
	includeDeleted bool
	pageSize       int32
	pageToken      string
}

var notesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List note headers",
	Long: `Calls ListNotes and prints a table of note headers.

Examples:
  notxctl notes list
  notxctl notes list --project notx:proj:…
  notxctl notes list --type secure --deleted`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		req := &pb.ListNotesRequest{
			ProjectUrn:     notesListFlags.projectURN,
			FolderUrn:      notesListFlags.folderURN,
			IncludeDeleted: notesListFlags.includeDeleted,
			PageSize:       notesListFlags.pageSize,
			PageToken:      notesListFlags.pageToken,
		}
		switch strings.ToLower(notesListFlags.noteType) {
		case "normal":
			req.NoteType = pb.NoteType_NOTE_TYPE_NORMAL
		case "secure":
			req.NoteType = pb.NoteType_NOTE_TYPE_SECURE
		}

		resp, err := conn.Notes().ListNotes(ctx, req)
		if err != nil {
			return fmt.Errorf("ListNotes: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			type row struct {
				URN        string    `json:"urn"`
				Name       string    `json:"name"`
				Type       string    `json:"type"`
				ProjectURN string    `json:"project_urn,omitempty"`
				FolderURN  string    `json:"folder_urn,omitempty"`
				Deleted    bool      `json:"deleted"`
				CreatedAt  time.Time `json:"created_at"`
				UpdatedAt  time.Time `json:"updated_at"`
			}
			type out struct {
				Notes         []row  `json:"notes"`
				NextPageToken string `json:"next_page_token,omitempty"`
			}
			o := out{NextPageToken: resp.NextPageToken}
			for _, n := range resp.Notes {
				r := row{
					URN:       n.Urn,
					Name:      n.Name,
					Type:      noteTypeStr(n.NoteType),
					Deleted:   n.Deleted,
					CreatedAt: n.CreatedAt.AsTime(),
					UpdatedAt: n.UpdatedAt.AsTime(),
				}
				if n.ProjectUrn != "" {
					r.ProjectURN = n.ProjectUrn
				}
				if n.FolderUrn != "" {
					r.FolderURN = n.FolderUrn
				}
				o.Notes = append(o.Notes, r)
			}
			return printJSON(o)

		default:
			tw := newTabWriter()
			defer tw.Flush()
			header(tw, "URN", "NAME", "TYPE", "DELETED", "UPDATED")
			for _, n := range resp.Notes {
				row(tw,
					shortURN(n.Urn),
					n.Name,
					noteTypeStr(n.NoteType),
					fmtBool(n.Deleted, "yes", "—"),
					fmtTime(n.UpdatedAt.AsTime()),
				)
			}
			if resp.NextPageToken != "" {
				fmt.Printf("\nnext-page-token: %s\n", resp.NextPageToken)
			}
		}
		return nil
	},
}

func init() {
	f := notesListCmd.Flags()
	f.StringVar(&notesListFlags.projectURN, "project", "", "filter by project URN")
	f.StringVar(&notesListFlags.folderURN, "folder", "", "filter by folder URN")
	f.StringVar(&notesListFlags.noteType, "type", "", "filter by type: normal | secure")
	f.BoolVar(&notesListFlags.includeDeleted, "deleted", false, "include soft-deleted notes")
	f.Int32Var(&notesListFlags.pageSize, "page-size", 0, "max results per page (0 = server default)")
	f.StringVar(&notesListFlags.pageToken, "page-token", "", "pagination token from previous response")
}

// ─────────────────────────────────────────────────────────────────────────────
// notes create
// ─────────────────────────────────────────────────────────────────────────────

var notesCreateFlags struct {
	urn        string
	name       string
	noteType   string
	projectURN string
	folderURN  string
}

var notesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new note (header only)",
	Long: `Calls CreateNote to register a new note.

If --urn is omitted a random notx:note:<uuid> is generated.
Use 'notes append-event' (or the notx CLI) to add content after creation.

Examples:
  notxctl notes create --name "Meeting notes"
  notxctl notes create --name "Secrets" --type secure --project notx:proj:…`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		if notesCreateFlags.name == "" {
			return fmt.Errorf("--name is required")
		}

		urn := notesCreateFlags.urn
		if urn == "" {
			urn = "notx:note:" + uuid.New().String()
		}

		nt := pb.NoteType_NOTE_TYPE_NORMAL
		if strings.ToLower(notesCreateFlags.noteType) == "secure" {
			nt = pb.NoteType_NOTE_TYPE_SECURE
		}

		now := timestamppb.Now()
		resp, err := conn.Notes().CreateNote(ctx, &pb.CreateNoteRequest{
			Header: &pb.NoteHeader{
				Urn:        urn,
				Name:       notesCreateFlags.name,
				NoteType:   nt,
				ProjectUrn: notesCreateFlags.projectURN,
				FolderUrn:  notesCreateFlags.folderURN,
				CreatedAt:  now,
				UpdatedAt:  now,
			},
		})
		if err != nil {
			return fmt.Errorf("CreateNote: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"urn":        resp.Header.Urn,
				"name":       resp.Header.Name,
				"type":       noteTypeStr(resp.Header.NoteType),
				"created_at": resp.Header.CreatedAt.AsTime(),
			})
		default:
			fmt.Printf("created  %s\n", resp.Header.Urn)
			fmt.Printf("name     %s\n", resp.Header.Name)
			fmt.Printf("type     %s\n", noteTypeStr(resp.Header.NoteType))
		}
		return nil
	},
}

func init() {
	f := notesCreateCmd.Flags()
	f.StringVar(&notesCreateFlags.urn, "urn", "", "URN for the new note (auto-generated if omitted)")
	f.StringVar(&notesCreateFlags.name, "name", "", "note name (required)")
	f.StringVar(&notesCreateFlags.noteType, "type", "normal", "note type: normal | secure")
	f.StringVar(&notesCreateFlags.projectURN, "project", "", "project URN to assign the note to")
	f.StringVar(&notesCreateFlags.folderURN, "folder", "", "folder URN to assign the note to")
}

// ─────────────────────────────────────────────────────────────────────────────
// notes delete <urn>
// ─────────────────────────────────────────────────────────────────────────────

var notesDeleteCmd = &cobra.Command{
	Use:   "delete <urn>",
	Short: "Soft-delete a note",
	Long: `Calls DeleteNote to soft-delete a note by URN.

The note is not permanently erased — it is marked deleted and excluded from
list results unless --deleted is passed.

Example:
  notxctl notes delete notx:note:1a9670dd-1a65-481a-ad17-03d77de021e5`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Notes().DeleteNote(ctx, &pb.DeleteNoteRequest{Urn: args[0]})
		if err != nil {
			return fmt.Errorf("DeleteNote: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{"urn": args[0], "deleted": resp.Deleted})
		default:
			fmt.Printf("deleted  %s\n", args[0])
		}
		return nil
	},
}

// ─────────────────────────────────────────────────────────────────────────────
// notes search <query>
// ─────────────────────────────────────────────────────────────────────────────

var notesSearchFlags struct {
	pageSize  int32
	pageToken string
}

var notesSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Full-text search across normal notes",
	Long: `Calls SearchNotes and prints matching note headers with excerpts.

Secure notes are never included in search results.

Examples:
  notxctl notes search "meeting"
  notxctl notes search "project alpha" --page-size 10`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Notes().SearchNotes(ctx, &pb.SearchNotesRequest{
			Query:     args[0],
			PageSize:  notesSearchFlags.pageSize,
			PageToken: notesSearchFlags.pageToken,
		})
		if err != nil {
			return fmt.Errorf("SearchNotes: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			type result struct {
				URN     string `json:"urn"`
				Name    string `json:"name"`
				Excerpt string `json:"excerpt"`
			}
			type out struct {
				Results       []result `json:"results"`
				NextPageToken string   `json:"next_page_token,omitempty"`
			}
			o := out{NextPageToken: resp.NextPageToken}
			for _, r := range resp.Results {
				o.Results = append(o.Results, result{
					URN:     r.Header.Urn,
					Name:    r.Header.Name,
					Excerpt: r.Excerpt,
				})
			}
			return printJSON(o)

		default:
			tw := newTabWriter()
			defer tw.Flush()
			header(tw, "URN", "NAME", "EXCERPT")
			for _, r := range resp.Results {
				excerpt := r.Excerpt
				if len(excerpt) > 60 {
					excerpt = excerpt[:57] + "..."
				}
				row(tw, shortURN(r.Header.Urn), r.Header.Name, excerpt)
			}
			if resp.NextPageToken != "" {
				fmt.Printf("\nnext-page-token: %s\n", resp.NextPageToken)
			}
		}
		return nil
	},
}

func init() {
	f := notesSearchCmd.Flags()
	f.Int32Var(&notesSearchFlags.pageSize, "page-size", 0, "max results per page (0 = server default)")
	f.StringVar(&notesSearchFlags.pageToken, "page-token", "", "pagination token from previous response")
}

// ─────────────────────────────────────────────────────────────────────────────
// notes events <urn>
// ─────────────────────────────────────────────────────────────────────────────

var notesEventsFlags struct {
	fromSequence int32
}

var notesEventsCmd = &cobra.Command{
	Use:   "events <urn>",
	Short: "Stream all events for a note",
	Long: `Calls StreamEvents and collects the full event stream into a list.

Use --from to start from a specific sequence number.

Examples:
  notxctl notes events notx:note:…
  notxctl notes events notx:note:… --from 5`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)

		// StreamEvents is a server-streaming RPC; give it the full timeout.
		ctx, cancel := context.WithTimeout(cmd.Context(), globalFlags.timeout)
		defer cancel()

		stream, err := conn.Notes().StreamEvents(ctx, &pb.StreamEventsRequest{
			NoteUrn:      args[0],
			FromSequence: notesEventsFlags.fromSequence,
		})
		if err != nil {
			return fmt.Errorf("StreamEvents: %w", err)
		}

		var events []*pb.EventProto
		for {
			evt, err := stream.Recv()
			if err != nil {
				// io.EOF is the normal end-of-stream signal.
				if isEOF(err) {
					break
				}
				return fmt.Errorf("StreamEvents recv: %w", err)
			}
			events = append(events, evt)
		}

		switch outputFromCtx(cmd) {
		case "json":
			type entry struct {
				Op         int32  `json:"op"`
				LineNumber int32  `json:"line_number"`
				Content    string `json:"content,omitempty"`
			}
			type eventOut struct {
				URN       string    `json:"urn"`
				Sequence  int32     `json:"sequence"`
				AuthorURN string    `json:"author_urn"`
				CreatedAt time.Time `json:"created_at"`
				Entries   []entry   `json:"entries,omitempty"`
				Encrypted bool      `json:"encrypted,omitempty"`
			}
			var out []eventOut
			for _, e := range events {
				eo := eventOut{
					URN:       e.Urn,
					Sequence:  e.Sequence,
					AuthorURN: e.AuthorUrn,
					CreatedAt: e.CreatedAt.AsTime(),
					Encrypted: e.Encrypted != nil,
				}
				for _, en := range e.Entries {
					eo.Entries = append(eo.Entries, entry{
						Op:         en.Op,
						LineNumber: en.LineNumber,
						Content:    en.Content,
					})
				}
				out = append(out, eo)
			}
			return printJSON(map[string]any{
				"note_urn": args[0],
				"count":    len(events),
				"events":   out,
			})

		default:
			tw := newTabWriter()
			defer tw.Flush()
			header(tw, "SEQ", "URN", "AUTHOR", "ENTRIES", "ENCRYPTED", "CREATED")
			for _, e := range events {
				row(tw,
					fmt.Sprintf("%d", e.Sequence),
					shortURN(e.Urn),
					shortURN(e.AuthorUrn),
					fmt.Sprintf("%d", len(e.Entries)),
					fmtBool(e.Encrypted != nil, "yes", "—"),
					fmtTime(e.CreatedAt.AsTime()),
				)
			}
			fmt.Printf("\ntotal: %d event(s)\n", len(events))
		}
		return nil
	},
}

func init() {
	notesEventsCmd.Flags().Int32Var(&notesEventsFlags.fromSequence, "from", 0,
		"start streaming from this sequence number (0 = first event)")
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers local to notes
// ─────────────────────────────────────────────────────────────────────────────

func noteTypeStr(nt pb.NoteType) string {
	switch nt {
	case pb.NoteType_NOTE_TYPE_NORMAL:
		return "normal"
	case pb.NoteType_NOTE_TYPE_SECURE:
		return "secure"
	default:
		return "unspecified"
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// isEOF returns true for both io.EOF and the gRPC status that wraps it.
func isEOF(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "EOF")
}
