package notxctl

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	pb "github.com/zebaqui/notx-engine/proto"
)

// ─────────────────────────────────────────────────────────────────────────────
// context — top-level group
// ─────────────────────────────────────────────────────────────────────────────

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Manage context graph (ContextService)",
	Long: `Commands for the ContextService gRPC endpoints.

Sub-commands:
  stats       GetStats           — queue-health statistics
  candidates  ListCandidates     — list candidate relations
  candidate   GetCandidate       — fetch a single candidate with burst previews
  promote     PromoteCandidate   — promote a candidate into a stable link
  dismiss     DismissCandidate   — dismiss a candidate
  bursts      ListBursts         — list bursts for a note
  burst       GetBurst           — fetch a single burst
  config      get/set            — per-project rate-limit configuration`,
}

func init() {
	contextCmd.AddCommand(contextStatsCmd)
	contextCmd.AddCommand(contextCandidatesCmd)
	contextCmd.AddCommand(contextCandidateCmd)
	contextCmd.AddCommand(contextPromoteCmd)
	contextCmd.AddCommand(contextDismissCmd)
	contextCmd.AddCommand(contextBurstsCmd)
	contextCmd.AddCommand(contextBurstCmd)
	contextCmd.AddCommand(contextConfigCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// context stats
// ─────────────────────────────────────────────────────────────────────────────

var contextStatsFlags struct {
	projectURN string
}

var contextStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Get queue-health statistics",
	Long: `Calls GetStats and prints counters for bursts and candidates.

When --project-urn is supplied the counters are scoped to that project.
Without it the numbers cover the entire server instance.

Examples:
  notxctl context stats
  notxctl context stats --project-urn notx:proj:…
  notxctl context stats -o json`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Context().GetStats(ctx, &pb.GetStatsRequest{
			ProjectUrn: contextStatsFlags.projectURN,
		})
		if err != nil {
			return fmt.Errorf("GetStats: %w", err)
		}

		s := resp.Stats

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"bursts_total":                  s.BurstsTotal,
				"bursts_today":                  s.BurstsToday,
				"candidates_pending":            s.CandidatesPending,
				"candidates_pending_unenriched": s.CandidatesPendingUnenriched,
				"candidates_promoted":           s.CandidatesPromoted,
				"candidates_dismissed":          s.CandidatesDismissed,
				"oldest_pending_age_days":       s.OldestPendingAgeDays,
			})
		default:
			tw := newTabWriter()
			fmt.Fprintf(tw, "Bursts total\t%d\n", s.BurstsTotal)
			fmt.Fprintf(tw, "Bursts today\t%d\n", s.BurstsToday)
			fmt.Fprintf(tw, "Candidates pending\t%d\n", s.CandidatesPending)
			fmt.Fprintf(tw, "  (unenriched)\t%d\n", s.CandidatesPendingUnenriched)
			fmt.Fprintf(tw, "Candidates promoted\t%d\n", s.CandidatesPromoted)
			fmt.Fprintf(tw, "Candidates dismissed\t%d\n", s.CandidatesDismissed)
			fmt.Fprintf(tw, "Oldest pending (days)\t%.1f\n", s.OldestPendingAgeDays)
			tw.Flush()

			fmt.Println()
			switch {
			case s.CandidatesPending == 0:
				fmt.Println("Queue is empty.")
			case s.CandidatesPendingUnenriched == 0:
				fmt.Println("Queue is ready.")
			default:
				fmt.Printf("Queue has %d unenriched candidates — scorer still running.\n",
					s.CandidatesPendingUnenriched)
			}
		}
		return nil
	},
}

func init() {
	f := contextStatsCmd.Flags()
	f.StringVar(&contextStatsFlags.projectURN, "project-urn", "",
		"scope stats to this project URN (empty = server-wide)")
}

// ─────────────────────────────────────────────────────────────────────────────
// context candidates
// ─────────────────────────────────────────────────────────────────────────────

var contextCandidatesFlags struct {
	projectURN    string
	noteURN       string
	status        string
	minScore      float64
	includeBursts bool
	pageSize      int32
	pageToken     string
}

var contextCandidatesCmd = &cobra.Command{
	Use:   "candidates",
	Short: "List candidate relations",
	Long: `Calls ListCandidates and prints a table of candidate records.

Examples:
  notxctl context candidates --project-urn notx:proj:…
  notxctl context candidates --status pending --min-score 0.3
  notxctl context candidates --include-bursts -o json`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Context().ListCandidates(ctx, &pb.ListCandidatesRequest{
			ProjectUrn:    contextCandidatesFlags.projectURN,
			NoteUrn:       contextCandidatesFlags.noteURN,
			Status:        contextCandidatesFlags.status,
			MinScore:      contextCandidatesFlags.minScore,
			IncludeBursts: contextCandidatesFlags.includeBursts,
			PageSize:      contextCandidatesFlags.pageSize,
			PageToken:     contextCandidatesFlags.pageToken,
		})
		if err != nil {
			return fmt.Errorf("ListCandidates: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			type burstOut struct {
				ID        string    `json:"id"`
				NoteURN   string    `json:"note_urn"`
				Sequence  int32     `json:"sequence"`
				LineStart int32     `json:"line_start"`
				LineEnd   int32     `json:"line_end"`
				Text      string    `json:"text"`
				Tokens    string    `json:"tokens"`
				Truncated bool      `json:"truncated"`
				CreatedAt time.Time `json:"created_at"`
			}
			type candidateOut struct {
				ID           string    `json:"id"`
				BurstAID     string    `json:"burst_a_id"`
				BurstBID     string    `json:"burst_b_id"`
				NoteURNA     string    `json:"note_urn_a"`
				NoteURNB     string    `json:"note_urn_b"`
				ProjectURN   string    `json:"project_urn"`
				OverlapScore float64   `json:"overlap_score"`
				BM25Score    float64   `json:"bm25_score"`
				Status       string    `json:"status"`
				CreatedAt    time.Time `json:"created_at"`
				ReviewedAt   time.Time `json:"reviewed_at,omitempty"`
				ReviewedBy   string    `json:"reviewed_by,omitempty"`
				PromotedLink string    `json:"promoted_link,omitempty"`
				BurstA       *burstOut `json:"burst_a,omitempty"`
				BurstB       *burstOut `json:"burst_b,omitempty"`
			}
			type out struct {
				Candidates    []candidateOut `json:"candidates"`
				NextPageToken string         `json:"next_page_token,omitempty"`
			}
			o := out{NextPageToken: resp.NextPageToken}
			for _, c := range resp.Candidates {
				co := candidateOut{
					ID:           c.Id,
					BurstAID:     c.BurstAId,
					BurstBID:     c.BurstBId,
					NoteURNA:     c.NoteUrnA,
					NoteURNB:     c.NoteUrnB,
					ProjectURN:   c.ProjectUrn,
					OverlapScore: c.OverlapScore,
					BM25Score:    c.Bm25Score,
					Status:       c.Status,
					CreatedAt:    c.CreatedAt.AsTime(),
					ReviewedBy:   c.ReviewedBy,
					PromotedLink: c.PromotedLink,
				}
				if c.ReviewedAt != nil {
					co.ReviewedAt = c.ReviewedAt.AsTime()
				}
				if c.BurstA != nil {
					co.BurstA = &burstOut{
						ID:        c.BurstA.Id,
						NoteURN:   c.BurstA.NoteUrn,
						Sequence:  c.BurstA.Sequence,
						LineStart: c.BurstA.LineStart,
						LineEnd:   c.BurstA.LineEnd,
						Text:      c.BurstA.Text,
						Tokens:    c.BurstA.Tokens,
						Truncated: c.BurstA.Truncated,
						CreatedAt: c.BurstA.CreatedAt.AsTime(),
					}
				}
				if c.BurstB != nil {
					co.BurstB = &burstOut{
						ID:        c.BurstB.Id,
						NoteURN:   c.BurstB.NoteUrn,
						Sequence:  c.BurstB.Sequence,
						LineStart: c.BurstB.LineStart,
						LineEnd:   c.BurstB.LineEnd,
						Text:      c.BurstB.Text,
						Tokens:    c.BurstB.Tokens,
						Truncated: c.BurstB.Truncated,
						CreatedAt: c.BurstB.CreatedAt.AsTime(),
					}
				}
				o.Candidates = append(o.Candidates, co)
			}
			return printJSON(o)

		default:
			tw := newTabWriter()
			header(tw, "ID", "NOTE-A", "NOTE-B", "OVERLAP", "BM25", "STATUS", "AGE")
			for _, c := range resp.Candidates {
				ageDays := time.Since(c.CreatedAt.AsTime()).Hours() / 24
				ageStr := fmt.Sprintf("%.0fd", ageDays)
				row(tw,
					shortURN(c.Id),
					shortURN(c.NoteUrnA),
					shortURN(c.NoteUrnB),
					fmt.Sprintf("%.3f", c.OverlapScore),
					fmt.Sprintf("%.3f", c.Bm25Score),
					c.Status,
					ageStr,
				)
				if contextCandidatesFlags.includeBursts {
					tw.Flush()
					textA := burstPreview(c.BurstA)
					textB := burstPreview(c.BurstB)
					fmt.Printf("    A: %s\n", textA)
					fmt.Printf("    B: %s\n", textB)
					fmt.Println()
				}
			}
			tw.Flush()
			if resp.NextPageToken != "" {
				fmt.Printf("\nnext-page-token: %s\n", resp.NextPageToken)
			}
			fmt.Printf("\ntotal: %d candidate(s)\n", len(resp.Candidates))
		}
		return nil
	},
}

func init() {
	f := contextCandidatesCmd.Flags()
	f.StringVar(&contextCandidatesFlags.projectURN, "project-urn", "",
		"filter by project URN")
	f.StringVar(&contextCandidatesFlags.noteURN, "note-urn", "",
		"filter to candidates involving this note URN")
	f.StringVar(&contextCandidatesFlags.status, "status", "",
		"filter by status: pending | promoted | dismissed | expired (empty = all)")
	f.Float64Var(&contextCandidatesFlags.minScore, "min-score", 0.0,
		"minimum overlap_score floor (0.0 = no floor)")
	f.BoolVar(&contextCandidatesFlags.includeBursts, "include-bursts", false,
		"embed burst previews in each candidate row")
	f.Int32Var(&contextCandidatesFlags.pageSize, "page-size", 0,
		"max results per page (0 = server default)")
	f.StringVar(&contextCandidatesFlags.pageToken, "page-token", "",
		"pagination token from previous response")
}

// ─────────────────────────────────────────────────────────────────────────────
// context candidate <id>
// ─────────────────────────────────────────────────────────────────────────────

var contextCandidateFlags struct {
	includeBursts bool
}

var contextCandidateCmd = &cobra.Command{
	Use:   "candidate <id>",
	Short: "Fetch a single candidate with burst previews",
	Long: `Calls GetCandidate and prints full candidate details.

Burst previews are included by default (--include-bursts=true).

Examples:
  notxctl context candidate <uuid>
  notxctl context candidate <uuid> --include-bursts=false
  notxctl context candidate <uuid> -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Context().GetCandidate(ctx, &pb.GetCandidateRequest{
			Id:            args[0],
			IncludeBursts: contextCandidateFlags.includeBursts,
		})
		if err != nil {
			return fmt.Errorf("GetCandidate: %w", err)
		}

		c := resp.Candidate

		switch outputFromCtx(cmd) {
		case "json":
			m := map[string]any{
				"id":            c.Id,
				"burst_a_id":    c.BurstAId,
				"burst_b_id":    c.BurstBId,
				"note_urn_a":    c.NoteUrnA,
				"note_urn_b":    c.NoteUrnB,
				"project_urn":   c.ProjectUrn,
				"overlap_score": c.OverlapScore,
				"bm25_score":    c.Bm25Score,
				"status":        c.Status,
				"created_at":    c.CreatedAt.AsTime(),
				"reviewed_by":   orDash(c.ReviewedBy),
				"promoted_link": orDash(c.PromotedLink),
			}
			if c.ReviewedAt != nil {
				m["reviewed_at"] = c.ReviewedAt.AsTime()
			} else {
				m["reviewed_at"] = nil
			}
			if c.BurstA != nil {
				m["burst_a"] = burstRecordMap(c.BurstA)
			}
			if c.BurstB != nil {
				m["burst_b"] = burstRecordMap(c.BurstB)
			}
			return printJSON(m)

		default:
			tw := newTabWriter()
			fmt.Fprintf(tw, "ID\t%s\n", c.Id)
			fmt.Fprintf(tw, "Status\t%s\n", c.Status)
			fmt.Fprintf(tw, "Project\t%s\n", c.ProjectUrn)
			fmt.Fprintf(tw, "Note A\t%s\n", c.NoteUrnA)
			fmt.Fprintf(tw, "Note B\t%s\n", c.NoteUrnB)
			fmt.Fprintf(tw, "Overlap\t%.3f\n", c.OverlapScore)
			fmt.Fprintf(tw, "BM25\t%.3f\n", c.Bm25Score)
			fmt.Fprintf(tw, "Created\t%s\n", fmtTime(c.CreatedAt.AsTime()))
			if c.ReviewedAt != nil {
				fmt.Fprintf(tw, "Reviewed\t%s\n", fmtTime(c.ReviewedAt.AsTime()))
			} else {
				fmt.Fprintf(tw, "Reviewed\t—\n")
			}
			fmt.Fprintf(tw, "Reviewed by\t%s\n", orDash(c.ReviewedBy))
			fmt.Fprintf(tw, "Promoted link\t%s\n", orDash(c.PromotedLink))
			tw.Flush()

			if c.BurstA != nil {
				fmt.Printf("\nBurst A  (lines %d–%d, seq %d)\n",
					c.BurstA.LineStart, c.BurstA.LineEnd, c.BurstA.Sequence)
				printIndented(c.BurstA.Text, "  ")
			}
			if c.BurstB != nil {
				fmt.Printf("\nBurst B  (lines %d–%d, seq %d)\n",
					c.BurstB.LineStart, c.BurstB.LineEnd, c.BurstB.Sequence)
				printIndented(c.BurstB.Text, "  ")
			}
		}
		return nil
	},
}

func init() {
	f := contextCandidateCmd.Flags()
	f.BoolVar(&contextCandidateFlags.includeBursts, "include-bursts", true,
		"embed burst previews in the response (default true)")
}

// ─────────────────────────────────────────────────────────────────────────────
// context promote <id>
// ─────────────────────────────────────────────────────────────────────────────

var contextPromoteFlags struct {
	label       string
	direction   string
	reviewerURN string
}

var contextPromoteCmd = &cobra.Command{
	Use:   "promote <id>",
	Short: "Promote a candidate into a stable link",
	Long: `Calls PromoteCandidate to convert a pending candidate into a notx:lnk link.

The engine generates anchor slugs for both notes, writes anchor entries, and
records the link in the backlink index.

Examples:
  notxctl context promote <uuid>
  notxctl context promote <uuid> --label "related" --direction a_to_b
  notxctl context promote <uuid> --reviewer-urn urn:notx:usr:alice -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Context().PromoteCandidate(ctx, &pb.PromoteCandidateRequest{
			Id:          args[0],
			Label:       contextPromoteFlags.label,
			Direction:   contextPromoteFlags.direction,
			ReviewerUrn: contextPromoteFlags.reviewerURN,
		})
		if err != nil {
			return fmt.Errorf("PromoteCandidate: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"anchor_a_id":  resp.AnchorAId,
				"anchor_b_id":  resp.AnchorBId,
				"link_a_to_b":  orDash(resp.LinkAToB),
				"link_b_to_a":  orDash(resp.LinkBToA),
				"candidate_id": resp.Candidate.Id,
				"status":       resp.Candidate.Status,
			})
		default:
			tw := newTabWriter()
			fmt.Fprintf(tw, "Promoted\t%s\n", resp.Candidate.Id)
			fmt.Fprintf(tw, "Anchor A\t%s\n", resp.AnchorAId)
			fmt.Fprintf(tw, "Anchor B\t%s\n", resp.AnchorBId)
			fmt.Fprintf(tw, "Link A→B\t%s\n", orDash(resp.LinkAToB))
			fmt.Fprintf(tw, "Link B→A\t%s\n", orDash(resp.LinkBToA))
			tw.Flush()
		}
		return nil
	},
}

func init() {
	f := contextPromoteCmd.Flags()
	f.StringVar(&contextPromoteFlags.label, "label", "",
		"optional label key used in the node_links map of both notes")
	f.StringVar(&contextPromoteFlags.direction, "direction", "both",
		`direction of link creation: "both" | "a_to_b" | "b_to_a" (default "both")`)
	f.StringVar(&contextPromoteFlags.reviewerURN, "reviewer-urn", "urn:notx:usr:anon",
		`URN of the reviewing user or agent (default "urn:notx:usr:anon")`)
}

// ─────────────────────────────────────────────────────────────────────────────
// context dismiss <id>
// ─────────────────────────────────────────────────────────────────────────────

var contextDismissFlags struct {
	reviewerURN string
}

var contextDismissCmd = &cobra.Command{
	Use:   "dismiss <id>",
	Short: "Dismiss a candidate",
	Long: `Calls DismissCandidate to mark a candidate as dismissed.

Dismissed candidates are never re-surfaced for the same burst pair; new edits
that produce fresh overlapping bursts will generate new candidates independently.

Examples:
  notxctl context dismiss <uuid>
  notxctl context dismiss <uuid> --reviewer-urn urn:notx:usr:alice
  notxctl context dismiss <uuid> -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Context().DismissCandidate(ctx, &pb.DismissCandidateRequest{
			Id:          args[0],
			ReviewerUrn: contextDismissFlags.reviewerURN,
		})
		if err != nil {
			return fmt.Errorf("DismissCandidate: %w", err)
		}

		c := resp.Candidate

		switch outputFromCtx(cmd) {
		case "json":
			m := map[string]any{
				"id":          c.Id,
				"status":      c.Status,
				"reviewed_by": c.ReviewedBy,
			}
			if c.ReviewedAt != nil {
				m["reviewed_at"] = c.ReviewedAt.AsTime()
			}
			return printJSON(m)
		default:
			tw := newTabWriter()
			fmt.Fprintf(tw, "Dismissed\t%s\n", c.Id)
			fmt.Fprintf(tw, "Status\t%s\n", c.Status)
			reviewedAt := "—"
			if c.ReviewedAt != nil {
				reviewedAt = fmtTime(c.ReviewedAt.AsTime())
			}
			fmt.Fprintf(tw, "Reviewed\t%s\n", reviewedAt)
			tw.Flush()
		}
		return nil
	},
}

func init() {
	f := contextDismissCmd.Flags()
	f.StringVar(&contextDismissFlags.reviewerURN, "reviewer-urn", "urn:notx:usr:anon",
		`URN of the reviewing user or agent (default "urn:notx:usr:anon")`)
}

// ─────────────────────────────────────────────────────────────────────────────
// context bursts
// ─────────────────────────────────────────────────────────────────────────────

var contextBurstsFlags struct {
	noteURN       string
	sinceSequence int32
	pageSize      int32
	pageToken     string
}

var contextBurstsCmd = &cobra.Command{
	Use:   "bursts",
	Short: "List bursts for a note",
	Long: `Calls ListBursts and prints a table of burst records.

--note-urn is required.

Examples:
  notxctl context bursts --note-urn notx:note:…
  notxctl context bursts --note-urn notx:note:… --since-sequence 5
  notxctl context bursts --note-urn notx:note:… -o json`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if contextBurstsFlags.noteURN == "" {
			return fmt.Errorf("--note-urn is required")
		}

		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Context().ListBursts(ctx, &pb.ListBurstsRequest{
			NoteUrn:       contextBurstsFlags.noteURN,
			SinceSequence: contextBurstsFlags.sinceSequence,
			PageSize:      contextBurstsFlags.pageSize,
			PageToken:     contextBurstsFlags.pageToken,
		})
		if err != nil {
			return fmt.Errorf("ListBursts: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			type burstOut struct {
				ID         string    `json:"id"`
				NoteURN    string    `json:"note_urn"`
				ProjectURN string    `json:"project_urn"`
				FolderURN  string    `json:"folder_urn,omitempty"`
				AuthorURN  string    `json:"author_urn,omitempty"`
				Sequence   int32     `json:"sequence"`
				LineStart  int32     `json:"line_start"`
				LineEnd    int32     `json:"line_end"`
				Text       string    `json:"text"`
				Tokens     string    `json:"tokens"`
				Truncated  bool      `json:"truncated"`
				CreatedAt  time.Time `json:"created_at"`
			}
			type out struct {
				Bursts        []burstOut `json:"bursts"`
				NextPageToken string     `json:"next_page_token,omitempty"`
			}
			o := out{NextPageToken: resp.NextPageToken}
			for _, b := range resp.Bursts {
				o.Bursts = append(o.Bursts, burstOut{
					ID:         b.Id,
					NoteURN:    b.NoteUrn,
					ProjectURN: b.ProjectUrn,
					FolderURN:  b.FolderUrn,
					AuthorURN:  b.AuthorUrn,
					Sequence:   b.Sequence,
					LineStart:  b.LineStart,
					LineEnd:    b.LineEnd,
					Text:       b.Text,
					Tokens:     b.Tokens,
					Truncated:  b.Truncated,
					CreatedAt:  b.CreatedAt.AsTime(),
				})
			}
			return printJSON(o)

		default:
			tw := newTabWriter()
			header(tw, "ID", "NOTE", "SEQ", "LINES", "TOKENS", "CREATED")
			for _, b := range resp.Bursts {
				row(tw,
					shortURN(b.Id),
					shortURN(b.NoteUrn),
					fmt.Sprintf("%d", b.Sequence),
					fmt.Sprintf("%d–%d", b.LineStart, b.LineEnd),
					fmt.Sprintf("%d", tokenCount(b.Tokens)),
					fmtTime(b.CreatedAt.AsTime()),
				)
			}
			tw.Flush()
			if resp.NextPageToken != "" {
				fmt.Printf("\nnext-page-token: %s\n", resp.NextPageToken)
			}
			fmt.Printf("\ntotal: %d burst(s)\n", len(resp.Bursts))
		}
		return nil
	},
}

func init() {
	f := contextBurstsCmd.Flags()
	f.StringVar(&contextBurstsFlags.noteURN, "note-urn", "",
		"URN of the note whose bursts are returned (required)")
	f.Int32Var(&contextBurstsFlags.sinceSequence, "since-sequence", 0,
		"only return bursts from events at or after this sequence (0 = all)")
	f.Int32Var(&contextBurstsFlags.pageSize, "page-size", 0,
		"max results per page (0 = server default)")
	f.StringVar(&contextBurstsFlags.pageToken, "page-token", "",
		"pagination token from previous response")
}

// ─────────────────────────────────────────────────────────────────────────────
// context burst <id>
// ─────────────────────────────────────────────────────────────────────────────

var contextBurstCmd = &cobra.Command{
	Use:   "burst <id>",
	Short: "Fetch a single burst",
	Long: `Calls GetBurst and prints full burst details including text and tokens.

Examples:
  notxctl context burst <uuid>
  notxctl context burst <uuid> -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Context().GetBurst(ctx, &pb.GetBurstRequest{
			Id: args[0],
		})
		if err != nil {
			return fmt.Errorf("GetBurst: %w", err)
		}

		b := resp.Burst

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(burstRecordMap(b))
		default:
			tw := newTabWriter()
			fmt.Fprintf(tw, "ID\t%s\n", b.Id)
			fmt.Fprintf(tw, "Note\t%s\n", b.NoteUrn)
			fmt.Fprintf(tw, "Sequence\t%d\n", b.Sequence)
			fmt.Fprintf(tw, "Lines\t%d–%d\n", b.LineStart, b.LineEnd)
			fmt.Fprintf(tw, "Truncated\t%s\n", fmtBool(b.Truncated, "yes", "no"))
			fmt.Fprintf(tw, "Created\t%s\n", fmtTime(b.CreatedAt.AsTime()))
			fmt.Fprintf(tw, "Tokens\t%d\n", tokenCount(b.Tokens))
			tw.Flush()

			fmt.Println("\nText:")
			printIndented(b.Text, "  ")

			fmt.Println("\nTokens:")
			printIndented(b.Tokens, "  ")
		}
		return nil
	},
}

// ─────────────────────────────────────────────────────────────────────────────
// context config — sub-group
// ─────────────────────────────────────────────────────────────────────────────

var contextConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage per-project context configuration",
	Long: `Commands for per-project rate-limit configuration.

Sub-commands:
  get  <project-urn>  GetProjectConfig  — fetch per-project config
  set  <project-urn>  SetProjectConfig  — set per-project burst caps`,
}

func init() {
	contextConfigCmd.AddCommand(contextConfigGetCmd)
	contextConfigCmd.AddCommand(contextConfigSetCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// context config get <project-urn>
// ─────────────────────────────────────────────────────────────────────────────

var contextConfigGetCmd = &cobra.Command{
	Use:   "get <project-urn>",
	Short: "Fetch per-project context config",
	Long: `Calls GetProjectConfig and prints per-project rate-limit overrides.

A cap value of 0 means "use the global server default".

Examples:
  notxctl context config get notx:proj:…
  notxctl context config get notx:proj:… -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Context().GetProjectConfig(ctx, &pb.GetProjectConfigRequest{
			ProjectUrn: args[0],
		})
		if err != nil {
			return fmt.Errorf("GetProjectConfig: %w", err)
		}

		return printProjectConfig(cmd, resp.Config, "")
	},
}

// ─────────────────────────────────────────────────────────────────────────────
// context config set <project-urn>
// ─────────────────────────────────────────────────────────────────────────────

var contextConfigSetFlags struct {
	noteCap    int32
	projectCap int32
}

var contextConfigSetCmd = &cobra.Command{
	Use:   "set <project-urn>",
	Short: "Set per-project burst caps",
	Long: `Calls SetProjectConfig to set or replace per-project rate-limit overrides.

Pass 0 for a cap field to reset it to the global server default.

Examples:
  notxctl context config set notx:proj:… --note-cap 50 --project-cap 500
  notxctl context config set notx:proj:… --note-cap 0   # reset note cap to global default
  notxctl context config set notx:proj:… -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Context().SetProjectConfig(ctx, &pb.SetProjectConfigRequest{
			ProjectUrn:               args[0],
			BurstMaxPerNotePerDay:    contextConfigSetFlags.noteCap,
			BurstMaxPerProjectPerDay: contextConfigSetFlags.projectCap,
		})
		if err != nil {
			return fmt.Errorf("SetProjectConfig: %w", err)
		}

		return printProjectConfig(cmd, resp.Config,
			fmt.Sprintf("Updated config for %s\n\n", args[0]))
	},
}

func init() {
	f := contextConfigSetCmd.Flags()
	f.Int32Var(&contextConfigSetFlags.noteCap, "note-cap", 0,
		"max bursts per note per day (0 = global default)")
	f.Int32Var(&contextConfigSetFlags.projectCap, "project-cap", 0,
		"max bursts per project per day (0 = global default)")
}

// ─────────────────────────────────────────────────────────────────────────────
// Shared output helpers
// ─────────────────────────────────────────────────────────────────────────────

// printProjectConfig prints a ProjectContextConfig in table or JSON form.
// prefix is printed before the table (e.g. "Updated config for …\n\n").
func printProjectConfig(cmd *cobra.Command, cfg *pb.ProjectContextConfig, prefix string) error {
	switch outputFromCtx(cmd) {
	case "json":
		m := map[string]any{
			"project_urn":                   cfg.ProjectUrn,
			"burst_max_per_note_per_day":    cfg.BurstMaxPerNotePerDay,
			"burst_max_per_project_per_day": cfg.BurstMaxPerProjectPerDay,
		}
		if cfg.UpdatedAt != nil {
			m["updated_at"] = cfg.UpdatedAt.AsTime()
		} else {
			m["updated_at"] = nil
		}
		return printJSON(m)
	default:
		if prefix != "" {
			fmt.Print(prefix)
		}
		tw := newTabWriter()
		fmt.Fprintf(tw, "Project\t%s\n", cfg.ProjectUrn)
		fmt.Fprintf(tw, "Note cap/day\t%s\n", fmtCap(cfg.BurstMaxPerNotePerDay))
		fmt.Fprintf(tw, "Project cap/day\t%s\n", fmtCap(cfg.BurstMaxPerProjectPerDay))
		updatedAt := "—"
		if cfg.UpdatedAt != nil {
			updatedAt = fmtTime(cfg.UpdatedAt.AsTime())
		}
		fmt.Fprintf(tw, "Updated\t%s\n", updatedAt)
		tw.Flush()
	}
	return nil
}

// fmtCap formats a burst cap value: 0 means "use the global default".
func fmtCap(n int32) string {
	if n == 0 {
		return "— (global default)"
	}
	return fmt.Sprintf("%d", n)
}

// tokenCount returns the number of space-separated tokens in s.
// Returns 0 for empty strings.
func tokenCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, " ") + 1
}

// burstPreview returns up to 80 chars of burst text with newlines replaced by
// spaces. Returns "—" when the burst record is nil or has no text.
func burstPreview(b *pb.BurstRecord) string {
	if b == nil || b.Text == "" {
		return "—"
	}
	text := strings.ReplaceAll(b.Text, "\n", " ")
	if len(text) > 80 {
		text = text[:80]
	}
	return text
}

// printIndented prints each line of text with the given prefix indent.
func printIndented(text, indent string) {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		fmt.Printf("%s%s\n", indent, line)
	}
}

// burstRecordMap converts a BurstRecord to a map[string]any for JSON output.
func burstRecordMap(b *pb.BurstRecord) map[string]any {
	if b == nil {
		return nil
	}
	return map[string]any{
		"id":          b.Id,
		"note_urn":    b.NoteUrn,
		"project_urn": b.ProjectUrn,
		"folder_urn":  b.FolderUrn,
		"author_urn":  b.AuthorUrn,
		"sequence":    b.Sequence,
		"line_start":  b.LineStart,
		"line_end":    b.LineEnd,
		"text":        b.Text,
		"tokens":      b.Tokens,
		"truncated":   b.Truncated,
		"created_at":  b.CreatedAt.AsTime(),
	}
}
