package notxctl

import (
	"fmt"

	"github.com/spf13/cobra"

	pb "github.com/zebaqui/notx-engine/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

// fmtTimestamp formats a *timestamppb.Timestamp into a compact human-readable
// string. Nil timestamps are rendered as "—".
func fmtTimestamp(t *timestamppb.Timestamp) string {
	if t == nil {
		return "—"
	}
	return t.AsTime().UTC().Format("2006-01-02 15:04:05Z")
}

// truncate shortens s to at most maxLen runes, appending "..." if trimmed.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-3]) + "..."
}

// ─────────────────────────────────────────────────────────────────────────────
// links — top-level group
// ─────────────────────────────────────────────────────────────────────────────

var linksCmd = &cobra.Command{
	Use:   "links",
	Short: "Manage links (LinkService)",
	Long: `Commands for the LinkService gRPC endpoints.

Sub-command groups:
  anchors    Manage note anchors (positions within a note)
  backlinks  Manage internal note-to-note backlinks
  external   Manage external URI links from notes`,
}

func init() {
	linksCmd.AddCommand(linksAnchorsCmd)
	linksCmd.AddCommand(linksBacklinksCmd)
	linksCmd.AddCommand(linksExternalCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// links anchors — sub-group
// ─────────────────────────────────────────────────────────────────────────────

var linksAnchorsCmd = &cobra.Command{
	Use:   "anchors",
	Short: "Manage note anchors",
	Long: `Commands for anchor management within the LinkService.

Sub-commands:
  list    ListAnchors  — list all anchors for a note
  get     GetAnchor    — fetch a single anchor
  upsert  UpsertAnchor — create or update an anchor
  delete  DeleteAnchor — delete (or tombstone) an anchor`,
}

func init() {
	linksAnchorsCmd.AddCommand(anchorsListCmd)
	linksAnchorsCmd.AddCommand(anchorsGetCmd)
	linksAnchorsCmd.AddCommand(anchorsUpsertCmd)
	linksAnchorsCmd.AddCommand(anchorsDeleteCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// links anchors list
// ─────────────────────────────────────────────────────────────────────────────

var anchorsListFlags struct {
	noteURN string
}

var anchorsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all anchors for a note",
	Long: `Calls ListAnchors and prints a table of anchors.

Examples:
  notxctl links anchors list --note-urn notx:note:…
  notxctl links anchors list --note-urn notx:note:… -o json`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if anchorsListFlags.noteURN == "" {
			return fmt.Errorf("--note-urn is required")
		}

		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Links().ListAnchors(ctx, &pb.ListAnchorsRequest{
			NoteUrn: anchorsListFlags.noteURN,
		})
		if err != nil {
			return fmt.Errorf("ListAnchors: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			type anchorOut struct {
				NoteURN   string `json:"note_urn"`
				AnchorID  string `json:"anchor_id"`
				Line      int32  `json:"line"`
				CharStart int32  `json:"char_start"`
				CharEnd   int32  `json:"char_end"`
				Preview   string `json:"preview"`
				Status    string `json:"status"`
				UpdatedAt string `json:"updated_at"`
			}
			type out struct {
				Anchors []anchorOut `json:"anchors"`
			}
			o := out{}
			for _, a := range resp.Anchors {
				o.Anchors = append(o.Anchors, anchorOut{
					NoteURN:   a.NoteUrn,
					AnchorID:  a.AnchorId,
					Line:      a.Line,
					CharStart: a.CharStart,
					CharEnd:   a.CharEnd,
					Preview:   a.Preview,
					Status:    a.Status,
					UpdatedAt: fmtTimestamp(a.UpdatedAt),
				})
			}
			return printJSON(o)

		default:
			tw := newTabWriter()
			defer tw.Flush()
			header(tw, "ANCHOR-ID", "LINE", "STATUS", "PREVIEW", "UPDATED")
			for _, a := range resp.Anchors {
				row(tw,
					a.AnchorId,
					fmt.Sprintf("%d", a.Line),
					orDash(a.Status),
					orDash(truncate(a.Preview, 40)),
					fmtTimestamp(a.UpdatedAt),
				)
			}
			fmt.Printf("\ntotal: %d anchor(s)\n", len(resp.Anchors))
		}
		return nil
	},
}

func init() {
	f := anchorsListCmd.Flags()
	f.StringVar(&anchorsListFlags.noteURN, "note-urn", "", "note URN to list anchors for (required)")
}

// ─────────────────────────────────────────────────────────────────────────────
// links anchors get <note-urn> <anchor-id>
// ─────────────────────────────────────────────────────────────────────────────

var anchorsGetCmd = &cobra.Command{
	Use:   "get <note-urn> <anchor-id>",
	Short: "Fetch a single anchor",
	Long: `Calls GetAnchor and prints the anchor details.

Example:
  notxctl links anchors get notx:note:… my-anchor`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Links().GetAnchor(ctx, &pb.GetAnchorRequest{
			NoteUrn:  args[0],
			AnchorId: args[1],
		})
		if err != nil {
			return fmt.Errorf("GetAnchor: %w", err)
		}

		a := resp.Anchor

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"note_urn":   a.NoteUrn,
				"anchor_id":  a.AnchorId,
				"line":       a.Line,
				"char_start": a.CharStart,
				"char_end":   a.CharEnd,
				"preview":    a.Preview,
				"status":     a.Status,
				"updated_at": fmtTimestamp(a.UpdatedAt),
			})
		default:
			tw := newTabWriter()
			defer tw.Flush()
			fmt.Fprintf(tw, "Note URN\t%s\n", a.NoteUrn)
			fmt.Fprintf(tw, "Anchor ID\t%s\n", a.AnchorId)
			fmt.Fprintf(tw, "Line\t%d\n", a.Line)
			fmt.Fprintf(tw, "Char Start\t%d\n", a.CharStart)
			fmt.Fprintf(tw, "Char End\t%d\n", a.CharEnd)
			fmt.Fprintf(tw, "Preview\t%s\n", orDash(a.Preview))
			fmt.Fprintf(tw, "Status\t%s\n", orDash(a.Status))
			fmt.Fprintf(tw, "Updated\t%s\n", fmtTimestamp(a.UpdatedAt))
		}
		return nil
	},
}

// ─────────────────────────────────────────────────────────────────────────────
// links anchors upsert
// ─────────────────────────────────────────────────────────────────────────────

var anchorsUpsertFlags struct {
	noteURN   string
	anchorID  string
	line      int32
	charStart int32
	charEnd   int32
	preview   string
	status    string
}

var anchorsUpsertCmd = &cobra.Command{
	Use:   "upsert",
	Short: "Create or update an anchor",
	Long: `Calls UpsertAnchor to create or update an anchor within a note.

--note-urn, --anchor-id, and --line are required.

Examples:
  notxctl links anchors upsert --note-urn notx:note:… --anchor-id my-anchor --line 12
  notxctl links anchors upsert --note-urn notx:note:… --anchor-id my-anchor --line 12 \
      --char-start 0 --char-end 42 --preview "First sentence..." --status ok`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if anchorsUpsertFlags.noteURN == "" {
			return fmt.Errorf("--note-urn is required")
		}
		if anchorsUpsertFlags.anchorID == "" {
			return fmt.Errorf("--anchor-id is required")
		}
		if !cmd.Flags().Changed("line") {
			return fmt.Errorf("--line is required")
		}

		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Links().UpsertAnchor(ctx, &pb.UpsertAnchorRequest{
			NoteUrn:   anchorsUpsertFlags.noteURN,
			AnchorId:  anchorsUpsertFlags.anchorID,
			Line:      anchorsUpsertFlags.line,
			CharStart: anchorsUpsertFlags.charStart,
			CharEnd:   anchorsUpsertFlags.charEnd,
			Preview:   anchorsUpsertFlags.preview,
			Status:    anchorsUpsertFlags.status,
		})
		if err != nil {
			return fmt.Errorf("UpsertAnchor: %w", err)
		}

		a := resp.Anchor

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"note_urn":   a.NoteUrn,
				"anchor_id":  a.AnchorId,
				"line":       a.Line,
				"char_start": a.CharStart,
				"char_end":   a.CharEnd,
				"preview":    a.Preview,
				"status":     a.Status,
				"updated_at": fmtTimestamp(a.UpdatedAt),
			})
		default:
			fmt.Printf("upserted  %s / %s\n", a.NoteUrn, a.AnchorId)
			fmt.Printf("line      %d\n", a.Line)
			fmt.Printf("status    %s\n", orDash(a.Status))
			fmt.Printf("updated   %s\n", fmtTimestamp(a.UpdatedAt))
		}
		return nil
	},
}

func init() {
	f := anchorsUpsertCmd.Flags()
	f.StringVar(&anchorsUpsertFlags.noteURN, "note-urn", "", "note URN (required)")
	f.StringVar(&anchorsUpsertFlags.anchorID, "anchor-id", "", "anchor identifier (required)")
	f.Int32Var(&anchorsUpsertFlags.line, "line", 0, "line number within the note (required)")
	f.Int32Var(&anchorsUpsertFlags.charStart, "char-start", 0, "character start offset")
	f.Int32Var(&anchorsUpsertFlags.charEnd, "char-end", 0, "character end offset")
	f.StringVar(&anchorsUpsertFlags.preview, "preview", "", "preview text for the anchor")
	f.StringVar(&anchorsUpsertFlags.status, "status", "", "anchor status (e.g. ok, broken)")
}

// ─────────────────────────────────────────────────────────────────────────────
// links anchors delete <note-urn> <anchor-id>
// ─────────────────────────────────────────────────────────────────────────────

var anchorsDeleteFlags struct {
	tombstone bool
}

var anchorsDeleteCmd = &cobra.Command{
	Use:   "delete <note-urn> <anchor-id>",
	Short: "Delete (or tombstone) an anchor",
	Long: `Calls DeleteAnchor to remove an anchor from a note.

Pass --tombstone to soft-delete (mark as deleted) instead of a hard delete.

Examples:
  notxctl links anchors delete notx:note:… my-anchor
  notxctl links anchors delete notx:note:… my-anchor --tombstone`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Links().DeleteAnchor(ctx, &pb.DeleteAnchorRequest{
			NoteUrn:   args[0],
			AnchorId:  args[1],
			Tombstone: anchorsDeleteFlags.tombstone,
		})
		if err != nil {
			return fmt.Errorf("DeleteAnchor: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"note_urn":  args[0],
				"anchor_id": args[1],
				"deleted":   resp.Deleted,
			})
		default:
			fmt.Printf("deleted  %s / %s\n", args[0], args[1])
		}
		return nil
	},
}

func init() {
	f := anchorsDeleteCmd.Flags()
	f.BoolVar(&anchorsDeleteFlags.tombstone, "tombstone", false, "soft-delete (tombstone) instead of hard delete")
}

// ─────────────────────────────────────────────────────────────────────────────
// links backlinks — sub-group
// ─────────────────────────────────────────────────────────────────────────────

var linksBacklinksCmd = &cobra.Command{
	Use:   "backlinks",
	Short: "Manage internal note-to-note backlinks",
	Long: `Commands for backlink management within the LinkService.

Sub-commands:
  list      ListBacklinks      — list inbound backlinks for a target note
  outbound  ListOutboundLinks  — list outbound links from a source note
  referrers GetReferrers       — get source URNs that reference a target anchor
  upsert    UpsertBacklink     — create or update a backlink
  delete    DeleteBacklink     — remove a backlink`,
}

func init() {
	linksBacklinksCmd.AddCommand(backlinksListCmd)
	linksBacklinksCmd.AddCommand(backlinksOutboundCmd)
	linksBacklinksCmd.AddCommand(backlinksReferrersCmd)
	linksBacklinksCmd.AddCommand(backlinksUpsertCmd)
	linksBacklinksCmd.AddCommand(backlinksDeleteCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// links backlinks list
// ─────────────────────────────────────────────────────────────────────────────

var backlinksListFlags struct {
	targetURN string
	anchorID  string
}

var backlinksListCmd = &cobra.Command{
	Use:   "list",
	Short: "List inbound backlinks for a target note",
	Long: `Calls ListBacklinks and prints a table of backlinks pointing at the target note.

Examples:
  notxctl links backlinks list --target-urn notx:note:…
  notxctl links backlinks list --target-urn notx:note:… --anchor-id my-anchor -o json`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if backlinksListFlags.targetURN == "" {
			return fmt.Errorf("--target-urn is required")
		}

		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Links().ListBacklinks(ctx, &pb.ListBacklinksRequest{
			TargetUrn: backlinksListFlags.targetURN,
			AnchorId:  backlinksListFlags.anchorID,
		})
		if err != nil {
			return fmt.Errorf("ListBacklinks: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(backlinkRecordsToJSON(resp.Backlinks))
		default:
			tw := newTabWriter()
			defer tw.Flush()
			header(tw, "SOURCE", "TARGET", "ANCHOR", "LABEL", "CREATED")
			for _, bl := range resp.Backlinks {
				row(tw,
					shortURN(bl.SourceUrn),
					shortURN(bl.TargetUrn),
					orDash(bl.TargetAnchor),
					orDash(bl.Label),
					fmtTimestamp(bl.CreatedAt),
				)
			}
			fmt.Printf("\ntotal: %d backlink(s)\n", len(resp.Backlinks))
		}
		return nil
	},
}

func init() {
	f := backlinksListCmd.Flags()
	f.StringVar(&backlinksListFlags.targetURN, "target-urn", "", "target note URN (required)")
	f.StringVar(&backlinksListFlags.anchorID, "anchor-id", "", "filter by target anchor ID")
}

// ─────────────────────────────────────────────────────────────────────────────
// links backlinks outbound
// ─────────────────────────────────────────────────────────────────────────────

var backlinksOutboundFlags struct {
	sourceURN string
}

var backlinksOutboundCmd = &cobra.Command{
	Use:   "outbound",
	Short: "List outbound links from a source note",
	Long: `Calls ListOutboundLinks and prints all links that originate from the source note.

Examples:
  notxctl links backlinks outbound --source-urn notx:note:…
  notxctl links backlinks outbound --source-urn notx:note:… -o json`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if backlinksOutboundFlags.sourceURN == "" {
			return fmt.Errorf("--source-urn is required")
		}

		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Links().ListOutboundLinks(ctx, &pb.ListOutboundLinksRequest{
			SourceUrn: backlinksOutboundFlags.sourceURN,
		})
		if err != nil {
			return fmt.Errorf("ListOutboundLinks: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(backlinkRecordsToJSON(resp.Links))
		default:
			tw := newTabWriter()
			defer tw.Flush()
			header(tw, "SOURCE", "TARGET", "ANCHOR", "LABEL", "CREATED")
			for _, bl := range resp.Links {
				row(tw,
					shortURN(bl.SourceUrn),
					shortURN(bl.TargetUrn),
					orDash(bl.TargetAnchor),
					orDash(bl.Label),
					fmtTimestamp(bl.CreatedAt),
				)
			}
			fmt.Printf("\ntotal: %d link(s)\n", len(resp.Links))
		}
		return nil
	},
}

func init() {
	f := backlinksOutboundCmd.Flags()
	f.StringVar(&backlinksOutboundFlags.sourceURN, "source-urn", "", "source note URN (required)")
}

// ─────────────────────────────────────────────────────────────────────────────
// links backlinks referrers
// ─────────────────────────────────────────────────────────────────────────────

var backlinksReferrersFlags struct {
	targetURN string
	anchorID  string
}

var backlinksReferrersCmd = &cobra.Command{
	Use:   "referrers",
	Short: "Get source URNs that reference a target anchor",
	Long: `Calls GetReferrers and lists all source note URNs that link to the given target anchor.

Examples:
  notxctl links backlinks referrers --target-urn notx:note:… --anchor-id my-anchor
  notxctl links backlinks referrers --target-urn notx:note:… --anchor-id my-anchor -o json`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if backlinksReferrersFlags.targetURN == "" {
			return fmt.Errorf("--target-urn is required")
		}
		if backlinksReferrersFlags.anchorID == "" {
			return fmt.Errorf("--anchor-id is required")
		}

		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Links().GetReferrers(ctx, &pb.GetReferrersRequest{
			TargetUrn: backlinksReferrersFlags.targetURN,
			AnchorId:  backlinksReferrersFlags.anchorID,
		})
		if err != nil {
			return fmt.Errorf("GetReferrers: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"target_urn":  backlinksReferrersFlags.targetURN,
				"anchor_id":   backlinksReferrersFlags.anchorID,
				"source_urns": resp.SourceUrns,
			})
		default:
			for _, urn := range resp.SourceUrns {
				fmt.Println(urn)
			}
			fmt.Printf("\ntotal: %d referrer(s)\n", len(resp.SourceUrns))
		}
		return nil
	},
}

func init() {
	f := backlinksReferrersCmd.Flags()
	f.StringVar(&backlinksReferrersFlags.targetURN, "target-urn", "", "target note URN (required)")
	f.StringVar(&backlinksReferrersFlags.anchorID, "anchor-id", "", "target anchor ID (required)")
}

// ─────────────────────────────────────────────────────────────────────────────
// links backlinks upsert
// ─────────────────────────────────────────────────────────────────────────────

var backlinksUpsertFlags struct {
	sourceURN    string
	targetURN    string
	targetAnchor string
	label        string
}

var backlinksUpsertCmd = &cobra.Command{
	Use:   "upsert",
	Short: "Create or update a backlink",
	Long: `Calls UpsertBacklink to create or update a link between two notes.

--source-urn, --target-urn, and --target-anchor are required.

Examples:
  notxctl links backlinks upsert --source-urn notx:note:… --target-urn notx:note:… --target-anchor my-anchor
  notxctl links backlinks upsert --source-urn notx:note:… --target-urn notx:note:… --target-anchor my-anchor --label "related"`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if backlinksUpsertFlags.sourceURN == "" {
			return fmt.Errorf("--source-urn is required")
		}
		if backlinksUpsertFlags.targetURN == "" {
			return fmt.Errorf("--target-urn is required")
		}
		if backlinksUpsertFlags.targetAnchor == "" {
			return fmt.Errorf("--target-anchor is required")
		}

		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Links().UpsertBacklink(ctx, &pb.UpsertBacklinkRequest{
			SourceUrn:    backlinksUpsertFlags.sourceURN,
			TargetUrn:    backlinksUpsertFlags.targetURN,
			TargetAnchor: backlinksUpsertFlags.targetAnchor,
			Label:        backlinksUpsertFlags.label,
		})
		if err != nil {
			return fmt.Errorf("UpsertBacklink: %w", err)
		}

		bl := resp.Backlink

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"source_urn":    bl.SourceUrn,
				"target_urn":    bl.TargetUrn,
				"target_anchor": bl.TargetAnchor,
				"label":         bl.Label,
				"created_at":    fmtTimestamp(bl.CreatedAt),
			})
		default:
			fmt.Printf("upserted  %s → %s\n", bl.SourceUrn, bl.TargetUrn)
			fmt.Printf("anchor    %s\n", bl.TargetAnchor)
			fmt.Printf("label     %s\n", orDash(bl.Label))
			fmt.Printf("created   %s\n", fmtTimestamp(bl.CreatedAt))
		}
		return nil
	},
}

func init() {
	f := backlinksUpsertCmd.Flags()
	f.StringVar(&backlinksUpsertFlags.sourceURN, "source-urn", "", "source note URN (required)")
	f.StringVar(&backlinksUpsertFlags.targetURN, "target-urn", "", "target note URN (required)")
	f.StringVar(&backlinksUpsertFlags.targetAnchor, "target-anchor", "", "target anchor ID (required)")
	f.StringVar(&backlinksUpsertFlags.label, "label", "", "optional human-readable label for the link")
}

// ─────────────────────────────────────────────────────────────────────────────
// links backlinks delete
// ─────────────────────────────────────────────────────────────────────────────

var backlinksDeleteFlags struct {
	sourceURN    string
	targetURN    string
	targetAnchor string
}

var backlinksDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Remove a backlink",
	Long: `Calls DeleteBacklink to remove a link between two notes.

--source-urn, --target-urn, and --target-anchor are required.

Example:
  notxctl links backlinks delete --source-urn notx:note:… --target-urn notx:note:… --target-anchor my-anchor`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if backlinksDeleteFlags.sourceURN == "" {
			return fmt.Errorf("--source-urn is required")
		}
		if backlinksDeleteFlags.targetURN == "" {
			return fmt.Errorf("--target-urn is required")
		}
		if backlinksDeleteFlags.targetAnchor == "" {
			return fmt.Errorf("--target-anchor is required")
		}

		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Links().DeleteBacklink(ctx, &pb.DeleteBacklinkRequest{
			SourceUrn:    backlinksDeleteFlags.sourceURN,
			TargetUrn:    backlinksDeleteFlags.targetURN,
			TargetAnchor: backlinksDeleteFlags.targetAnchor,
		})
		if err != nil {
			return fmt.Errorf("DeleteBacklink: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"source_urn":    backlinksDeleteFlags.sourceURN,
				"target_urn":    backlinksDeleteFlags.targetURN,
				"target_anchor": backlinksDeleteFlags.targetAnchor,
				"deleted":       resp.Deleted,
			})
		default:
			fmt.Printf("deleted  %s → %s / %s\n",
				backlinksDeleteFlags.sourceURN,
				backlinksDeleteFlags.targetURN,
				backlinksDeleteFlags.targetAnchor,
			)
		}
		return nil
	},
}

func init() {
	f := backlinksDeleteCmd.Flags()
	f.StringVar(&backlinksDeleteFlags.sourceURN, "source-urn", "", "source note URN (required)")
	f.StringVar(&backlinksDeleteFlags.targetURN, "target-urn", "", "target note URN (required)")
	f.StringVar(&backlinksDeleteFlags.targetAnchor, "target-anchor", "", "target anchor ID (required)")
}

// ─────────────────────────────────────────────────────────────────────────────
// links external — sub-group
// ─────────────────────────────────────────────────────────────────────────────

var linksExternalCmd = &cobra.Command{
	Use:   "external",
	Short: "Manage external URI links from notes",
	Long: `Commands for external link management within the LinkService.

Sub-commands:
  list    ListExternalLinks    — list external links from a note
  upsert  UpsertExternalLink   — create or update an external link
  delete  DeleteExternalLink   — remove an external link`,
}

func init() {
	linksExternalCmd.AddCommand(externalListCmd)
	linksExternalCmd.AddCommand(externalUpsertCmd)
	linksExternalCmd.AddCommand(externalDeleteCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// links external list
// ─────────────────────────────────────────────────────────────────────────────

var externalListFlags struct {
	sourceURN string
}

var externalListCmd = &cobra.Command{
	Use:   "list",
	Short: "List external links from a note",
	Long: `Calls ListExternalLinks and prints all external URI links from the source note.

Examples:
  notxctl links external list --source-urn notx:note:…
  notxctl links external list --source-urn notx:note:… -o json`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if externalListFlags.sourceURN == "" {
			return fmt.Errorf("--source-urn is required")
		}

		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Links().ListExternalLinks(ctx, &pb.ListExternalLinksRequest{
			SourceUrn: externalListFlags.sourceURN,
		})
		if err != nil {
			return fmt.Errorf("ListExternalLinks: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			type linkOut struct {
				SourceURN string `json:"source_urn"`
				URI       string `json:"uri"`
				Label     string `json:"label"`
				CreatedAt string `json:"created_at"`
			}
			type out struct {
				Links []linkOut `json:"links"`
			}
			o := out{}
			for _, l := range resp.Links {
				o.Links = append(o.Links, linkOut{
					SourceURN: l.SourceUrn,
					URI:       l.Uri,
					Label:     l.Label,
					CreatedAt: fmtTimestamp(l.CreatedAt),
				})
			}
			return printJSON(o)

		default:
			tw := newTabWriter()
			defer tw.Flush()
			header(tw, "SOURCE", "URI", "LABEL", "CREATED")
			for _, l := range resp.Links {
				row(tw,
					shortURN(l.SourceUrn),
					truncate(l.Uri, 40),
					orDash(l.Label),
					fmtTimestamp(l.CreatedAt),
				)
			}
			fmt.Printf("\ntotal: %d link(s)\n", len(resp.Links))
		}
		return nil
	},
}

func init() {
	f := externalListCmd.Flags()
	f.StringVar(&externalListFlags.sourceURN, "source-urn", "", "source note URN (required)")
}

// ─────────────────────────────────────────────────────────────────────────────
// links external upsert
// ─────────────────────────────────────────────────────────────────────────────

var externalUpsertFlags struct {
	sourceURN string
	uri       string
	label     string
}

var externalUpsertCmd = &cobra.Command{
	Use:   "upsert",
	Short: "Create or update an external link",
	Long: `Calls UpsertExternalLink to create or update a link from a note to an external URI.

--source-urn and --uri are required.

Examples:
  notxctl links external upsert --source-urn notx:note:… --uri https://example.com/docs
  notxctl links external upsert --source-urn notx:note:… --uri https://example.com/docs --label "docs"`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if externalUpsertFlags.sourceURN == "" {
			return fmt.Errorf("--source-urn is required")
		}
		if externalUpsertFlags.uri == "" {
			return fmt.Errorf("--uri is required")
		}

		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Links().UpsertExternalLink(ctx, &pb.UpsertExternalLinkRequest{
			SourceUrn: externalUpsertFlags.sourceURN,
			Uri:       externalUpsertFlags.uri,
			Label:     externalUpsertFlags.label,
		})
		if err != nil {
			return fmt.Errorf("UpsertExternalLink: %w", err)
		}

		l := resp.Link

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"source_urn": l.SourceUrn,
				"uri":        l.Uri,
				"label":      l.Label,
				"created_at": fmtTimestamp(l.CreatedAt),
			})
		default:
			fmt.Printf("upserted  %s\n", l.Uri)
			fmt.Printf("source    %s\n", l.SourceUrn)
			fmt.Printf("label     %s\n", orDash(l.Label))
			fmt.Printf("created   %s\n", fmtTimestamp(l.CreatedAt))
		}
		return nil
	},
}

func init() {
	f := externalUpsertCmd.Flags()
	f.StringVar(&externalUpsertFlags.sourceURN, "source-urn", "", "source note URN (required)")
	f.StringVar(&externalUpsertFlags.uri, "uri", "", "external URI (required)")
	f.StringVar(&externalUpsertFlags.label, "label", "", "optional human-readable label for the link")
}

// ─────────────────────────────────────────────────────────────────────────────
// links external delete
// ─────────────────────────────────────────────────────────────────────────────

var externalDeleteFlags struct {
	sourceURN string
	uri       string
}

var externalDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Remove an external link",
	Long: `Calls DeleteExternalLink to remove an external URI link from a note.

--source-urn and --uri are required.

Example:
  notxctl links external delete --source-urn notx:note:… --uri https://example.com/docs`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if externalDeleteFlags.sourceURN == "" {
			return fmt.Errorf("--source-urn is required")
		}
		if externalDeleteFlags.uri == "" {
			return fmt.Errorf("--uri is required")
		}

		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Links().DeleteExternalLink(ctx, &pb.DeleteExternalLinkRequest{
			SourceUrn: externalDeleteFlags.sourceURN,
			Uri:       externalDeleteFlags.uri,
		})
		if err != nil {
			return fmt.Errorf("DeleteExternalLink: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"source_urn": externalDeleteFlags.sourceURN,
				"uri":        externalDeleteFlags.uri,
				"deleted":    resp.Deleted,
			})
		default:
			fmt.Printf("deleted  %s → %s\n", externalDeleteFlags.sourceURN, externalDeleteFlags.uri)
		}
		return nil
	},
}

func init() {
	f := externalDeleteCmd.Flags()
	f.StringVar(&externalDeleteFlags.sourceURN, "source-urn", "", "source note URN (required)")
	f.StringVar(&externalDeleteFlags.uri, "uri", "", "external URI to remove (required)")
}

// ─────────────────────────────────────────────────────────────────────────────
// Shared helpers
// ─────────────────────────────────────────────────────────────────────────────

// backlinkRecordsToJSON converts a slice of *pb.BacklinkRecord to a JSON-safe
// structure used by both list and outbound commands.
func backlinkRecordsToJSON(records []*pb.BacklinkRecord) map[string]any {
	type blOut struct {
		SourceURN    string `json:"source_urn"`
		TargetURN    string `json:"target_urn"`
		TargetAnchor string `json:"target_anchor"`
		Label        string `json:"label"`
		CreatedAt    string `json:"created_at"`
	}
	type out struct {
		Backlinks []blOut `json:"backlinks"`
	}
	o := out{}
	for _, bl := range records {
		o.Backlinks = append(o.Backlinks, blOut{
			SourceURN:    bl.SourceUrn,
			TargetURN:    bl.TargetUrn,
			TargetAnchor: bl.TargetAnchor,
			Label:        bl.Label,
			CreatedAt:    fmtTimestamp(bl.CreatedAt),
		})
	}
	return map[string]any{
		"backlinks": o.Backlinks,
	}
}
