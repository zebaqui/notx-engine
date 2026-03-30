package notxctl

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	pb "github.com/zebaqui/notx-engine/internal/server/proto"
)

// ─────────────────────────────────────────────────────────────────────────────
// projects — top-level group
// ─────────────────────────────────────────────────────────────────────────────

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "Manage projects (ProjectService)",
	Long: `Commands for the ProjectService gRPC endpoints.

Sub-commands:
  list    ListProjects    — list all projects
  get     GetProject      — fetch a single project
  create  CreateProject   — create a new project
  update  UpdateProject   — update name, description, or deleted flag
  delete  DeleteProject   — delete a project`,
}

func init() {
	projectsCmd.AddCommand(projectsListCmd)
	projectsCmd.AddCommand(projectsGetCmd)
	projectsCmd.AddCommand(projectsCreateCmd)
	projectsCmd.AddCommand(projectsUpdateCmd)
	projectsCmd.AddCommand(projectsDeleteCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// projects list
// ─────────────────────────────────────────────────────────────────────────────

var projectsListFlags struct {
	includeDeleted bool
	pageSize       int32
	pageToken      string
}

var projectsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all projects",
	Long: `Calls ListProjects and prints a table of projects.

Examples:
  notxctl projects list
  notxctl projects list --deleted
  notxctl projects list --page-size 20 -o json`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Projects().ListProjects(ctx, &pb.ListProjectsRequest{
			IncludeDeleted: projectsListFlags.includeDeleted,
			PageSize:       projectsListFlags.pageSize,
			PageToken:      projectsListFlags.pageToken,
		})
		if err != nil {
			return fmt.Errorf("ListProjects: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			type projectOut struct {
				URN         string    `json:"urn"`
				Name        string    `json:"name"`
				Description string    `json:"description,omitempty"`
				Deleted     bool      `json:"deleted"`
				CreatedAt   time.Time `json:"created_at"`
				UpdatedAt   time.Time `json:"updated_at"`
			}
			type out struct {
				Projects      []projectOut `json:"projects"`
				NextPageToken string       `json:"next_page_token,omitempty"`
			}
			o := out{NextPageToken: resp.NextPageToken}
			for _, p := range resp.Projects {
				o.Projects = append(o.Projects, projectOut{
					URN:         p.Urn,
					Name:        p.Name,
					Description: p.Description,
					Deleted:     p.Deleted,
					CreatedAt:   p.CreatedAt.AsTime(),
					UpdatedAt:   p.UpdatedAt.AsTime(),
				})
			}
			return printJSON(o)

		default:
			tw := newTabWriter()
			defer tw.Flush()
			header(tw, "URN", "NAME", "DESCRIPTION", "DELETED", "UPDATED")
			for _, p := range resp.Projects {
				desc := p.Description
				if len(desc) > 40 {
					desc = desc[:37] + "..."
				}
				row(tw,
					shortURN(p.Urn),
					p.Name,
					orDash(desc),
					fmtBool(p.Deleted, "yes", "—"),
					fmtTime(p.UpdatedAt.AsTime()),
				)
			}
			if resp.NextPageToken != "" {
				fmt.Printf("\nnext-page-token: %s\n", resp.NextPageToken)
			}
			fmt.Printf("\ntotal: %d project(s)\n", len(resp.Projects))
		}
		return nil
	},
}

func init() {
	f := projectsListCmd.Flags()
	f.BoolVar(&projectsListFlags.includeDeleted, "deleted", false, "include soft-deleted projects")
	f.Int32Var(&projectsListFlags.pageSize, "page-size", 0, "max results per page (0 = server default)")
	f.StringVar(&projectsListFlags.pageToken, "page-token", "", "pagination token from previous response")
}

// ─────────────────────────────────────────────────────────────────────────────
// projects get <urn>
// ─────────────────────────────────────────────────────────────────────────────

var projectsGetCmd = &cobra.Command{
	Use:   "get <urn>",
	Short: "Fetch a single project",
	Long: `Calls GetProject and prints the project details.

Example:
  notxctl projects get notx:proj:…`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Projects().GetProject(ctx, &pb.GetProjectRequest{
			Urn: args[0],
		})
		if err != nil {
			return fmt.Errorf("GetProject: %w", err)
		}

		p := resp.Project

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"urn":         p.Urn,
				"name":        p.Name,
				"description": p.Description,
				"deleted":     p.Deleted,
				"created_at":  p.CreatedAt.AsTime(),
				"updated_at":  p.UpdatedAt.AsTime(),
			})
		default:
			tw := newTabWriter()
			defer tw.Flush()
			fmt.Fprintf(tw, "URN\t%s\n", p.Urn)
			fmt.Fprintf(tw, "Name\t%s\n", p.Name)
			fmt.Fprintf(tw, "Description\t%s\n", orDash(p.Description))
			fmt.Fprintf(tw, "Deleted\t%s\n", fmtBool(p.Deleted, "yes", "no"))
			fmt.Fprintf(tw, "Created\t%s\n", fmtTime(p.CreatedAt.AsTime()))
			fmt.Fprintf(tw, "Updated\t%s\n", fmtTime(p.UpdatedAt.AsTime()))
		}
		return nil
	},
}

// ─────────────────────────────────────────────────────────────────────────────
// projects create
// ─────────────────────────────────────────────────────────────────────────────

var projectsCreateFlags struct {
	urn         string
	name        string
	description string
}

var projectsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new project",
	Long: `Calls CreateProject to register a new project.

If --urn is omitted a random notx:proj:<uuid> is generated.

Examples:
  notxctl projects create --name "Alpha"
  notxctl projects create --name "Beta" --desc "Q3 work" -o json`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if projectsCreateFlags.name == "" {
			return fmt.Errorf("--name is required")
		}

		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		urn := projectsCreateFlags.urn
		if urn == "" {
			urn = "notx:proj:" + uuid.New().String()
		}

		resp, err := conn.Projects().CreateProject(ctx, &pb.CreateProjectRequest{
			Urn:         urn,
			Name:        projectsCreateFlags.name,
			Description: projectsCreateFlags.description,
		})
		if err != nil {
			return fmt.Errorf("CreateProject: %w", err)
		}

		p := resp.Project

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"urn":         p.Urn,
				"name":        p.Name,
				"description": p.Description,
				"created_at":  p.CreatedAt.AsTime(),
			})
		default:
			fmt.Printf("created  %s\n", p.Urn)
			fmt.Printf("name     %s\n", p.Name)
			if p.Description != "" {
				fmt.Printf("desc     %s\n", p.Description)
			}
		}
		return nil
	},
}

func init() {
	f := projectsCreateCmd.Flags()
	f.StringVar(&projectsCreateFlags.urn, "urn", "",
		"URN for the new project (auto-generated if omitted)")
	f.StringVar(&projectsCreateFlags.name, "name", "",
		"project name (required)")
	f.StringVar(&projectsCreateFlags.description, "desc", "",
		"optional project description")
}

// ─────────────────────────────────────────────────────────────────────────────
// projects update <urn>
// ─────────────────────────────────────────────────────────────────────────────

var projectsUpdateFlags struct {
	name        string
	description string
	deleted     bool
}

var projectsUpdateCmd = &cobra.Command{
	Use:   "update <urn>",
	Short: "Update a project's name, description, or deleted flag",
	Long: `Calls UpdateProject to modify an existing project.

Only the flags you supply are applied. Any omitted flag keeps its current
value on the server.

Examples:
  notxctl projects update notx:proj:… --name "New Name"
  notxctl projects update notx:proj:… --desc "Updated description"
  notxctl projects update notx:proj:… --deleted`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		// First fetch the current state so we can carry forward any fields the
		// caller didn't explicitly set.
		getResp, err := conn.Projects().GetProject(ctx, &pb.GetProjectRequest{
			Urn: args[0],
		})
		if err != nil {
			return fmt.Errorf("GetProject (pre-update fetch): %w", err)
		}
		current := getResp.Project

		name := current.Name
		if cmd.Flags().Changed("name") {
			name = projectsUpdateFlags.name
		}

		description := current.Description
		if cmd.Flags().Changed("desc") {
			description = projectsUpdateFlags.description
		}

		deleted := current.Deleted
		if cmd.Flags().Changed("deleted") {
			deleted = projectsUpdateFlags.deleted
		}

		resp, err := conn.Projects().UpdateProject(ctx, &pb.UpdateProjectRequest{
			Urn:         args[0],
			Name:        name,
			Description: description,
			Deleted:     deleted,
		})
		if err != nil {
			return fmt.Errorf("UpdateProject: %w", err)
		}

		p := resp.Project

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"urn":         p.Urn,
				"name":        p.Name,
				"description": p.Description,
				"deleted":     p.Deleted,
				"updated_at":  p.UpdatedAt.AsTime(),
			})
		default:
			fmt.Printf("updated  %s\n", p.Urn)
			fmt.Printf("name     %s\n", p.Name)
			fmt.Printf("deleted  %s\n", fmtBool(p.Deleted, "yes", "no"))
		}
		return nil
	},
}

func init() {
	f := projectsUpdateCmd.Flags()
	f.StringVar(&projectsUpdateFlags.name, "name", "",
		"new project name")
	f.StringVar(&projectsUpdateFlags.description, "desc", "",
		"new project description")
	f.BoolVar(&projectsUpdateFlags.deleted, "deleted", false,
		"mark the project as deleted")
}

// ─────────────────────────────────────────────────────────────────────────────
// projects delete <urn>
// ─────────────────────────────────────────────────────────────────────────────

var projectsDeleteCmd = &cobra.Command{
	Use:   "delete <urn>",
	Short: "Delete a project",
	Long: `Calls DeleteProject to remove a project by URN.

Example:
  notxctl projects delete notx:proj:…`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Projects().DeleteProject(ctx, &pb.DeleteProjectRequest{
			Urn: args[0],
		})
		if err != nil {
			return fmt.Errorf("DeleteProject: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"urn":     args[0],
				"deleted": resp.Deleted,
			})
		default:
			fmt.Printf("deleted  %s\n", args[0])
		}
		return nil
	},
}
