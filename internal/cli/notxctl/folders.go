package notxctl

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	pb "github.com/zebaqui/notx-engine/internal/server/proto"
)

// ─────────────────────────────────────────────────────────────────────────────
// folders — top-level group
// ─────────────────────────────────────────────────────────────────────────────

var foldersCmd = &cobra.Command{
	Use:   "folders",
	Short: "Manage folders (ProjectService)",
	Long: `Commands for the ProjectService folder gRPC endpoints.

Sub-commands:
  list    ListFolders    — list folders, optionally filtered by project
  get     GetFolder      — fetch a single folder
  create  CreateFolder   — create a new folder inside a project
  update  UpdateFolder   — update name, description, or deleted flag
  delete  DeleteFolder   — delete a folder`,
}

func init() {
	foldersCmd.AddCommand(foldersListCmd)
	foldersCmd.AddCommand(foldersGetCmd)
	foldersCmd.AddCommand(foldersCreateCmd)
	foldersCmd.AddCommand(foldersUpdateCmd)
	foldersCmd.AddCommand(foldersDeleteCmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// folders list
// ─────────────────────────────────────────────────────────────────────────────

var foldersListFlags struct {
	projectURN     string
	includeDeleted bool
	pageSize       int32
	pageToken      string
}

var foldersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List folders",
	Long: `Calls ListFolders and prints a table of folders.

Examples:
  notxctl folders list
  notxctl folders list --project notx:proj:…
  notxctl folders list --deleted -o json`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Projects().ListFolders(ctx, &pb.ListFoldersRequest{
			ProjectUrn:     foldersListFlags.projectURN,
			IncludeDeleted: foldersListFlags.includeDeleted,
			PageSize:       foldersListFlags.pageSize,
			PageToken:      foldersListFlags.pageToken,
		})
		if err != nil {
			return fmt.Errorf("ListFolders: %w", err)
		}

		switch outputFromCtx(cmd) {
		case "json":
			type folderOut struct {
				URN         string    `json:"urn"`
				ProjectURN  string    `json:"project_urn"`
				Name        string    `json:"name"`
				Description string    `json:"description,omitempty"`
				Deleted     bool      `json:"deleted"`
				CreatedAt   time.Time `json:"created_at"`
				UpdatedAt   time.Time `json:"updated_at"`
			}
			type out struct {
				Folders       []folderOut `json:"folders"`
				NextPageToken string      `json:"next_page_token,omitempty"`
			}
			o := out{NextPageToken: resp.NextPageToken}
			for _, f := range resp.Folders {
				o.Folders = append(o.Folders, folderOut{
					URN:         f.Urn,
					ProjectURN:  f.ProjectUrn,
					Name:        f.Name,
					Description: f.Description,
					Deleted:     f.Deleted,
					CreatedAt:   f.CreatedAt.AsTime(),
					UpdatedAt:   f.UpdatedAt.AsTime(),
				})
			}
			return printJSON(o)

		default:
			tw := newTabWriter()
			defer tw.Flush()
			header(tw, "URN", "PROJECT", "NAME", "DESCRIPTION", "DELETED", "UPDATED")
			for _, f := range resp.Folders {
				desc := f.Description
				if len(desc) > 36 {
					desc = desc[:33] + "..."
				}
				row(tw,
					shortURN(f.Urn),
					shortURN(f.ProjectUrn),
					f.Name,
					orDash(desc),
					fmtBool(f.Deleted, "yes", "—"),
					fmtTime(f.UpdatedAt.AsTime()),
				)
			}
			if resp.NextPageToken != "" {
				fmt.Printf("\nnext-page-token: %s\n", resp.NextPageToken)
			}
			fmt.Printf("\ntotal: %d folder(s)\n", len(resp.Folders))
		}
		return nil
	},
}

func init() {
	f := foldersListCmd.Flags()
	f.StringVar(&foldersListFlags.projectURN, "project", "",
		"filter by project URN (notx:proj:…)")
	f.BoolVar(&foldersListFlags.includeDeleted, "deleted", false,
		"include soft-deleted folders")
	f.Int32Var(&foldersListFlags.pageSize, "page-size", 0,
		"max results per page (0 = server default)")
	f.StringVar(&foldersListFlags.pageToken, "page-token", "",
		"pagination token from previous response")
}

// ─────────────────────────────────────────────────────────────────────────────
// folders get <urn>
// ─────────────────────────────────────────────────────────────────────────────

var foldersGetCmd = &cobra.Command{
	Use:   "get <urn>",
	Short: "Fetch a single folder",
	Long: `Calls GetFolder and prints the folder details.

Example:
  notxctl folders get notx:folder:…`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Projects().GetFolder(ctx, &pb.GetFolderRequest{
			Urn: args[0],
		})
		if err != nil {
			return fmt.Errorf("GetFolder: %w", err)
		}

		f := resp.Folder

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"urn":         f.Urn,
				"project_urn": f.ProjectUrn,
				"name":        f.Name,
				"description": f.Description,
				"deleted":     f.Deleted,
				"created_at":  f.CreatedAt.AsTime(),
				"updated_at":  f.UpdatedAt.AsTime(),
			})
		default:
			tw := newTabWriter()
			defer tw.Flush()
			fmt.Fprintf(tw, "URN\t%s\n", f.Urn)
			fmt.Fprintf(tw, "Project\t%s\n", f.ProjectUrn)
			fmt.Fprintf(tw, "Name\t%s\n", f.Name)
			fmt.Fprintf(tw, "Description\t%s\n", orDash(f.Description))
			fmt.Fprintf(tw, "Deleted\t%s\n", fmtBool(f.Deleted, "yes", "no"))
			fmt.Fprintf(tw, "Created\t%s\n", fmtTime(f.CreatedAt.AsTime()))
			fmt.Fprintf(tw, "Updated\t%s\n", fmtTime(f.UpdatedAt.AsTime()))
		}
		return nil
	},
}

// ─────────────────────────────────────────────────────────────────────────────
// folders create
// ─────────────────────────────────────────────────────────────────────────────

var foldersCreateFlags struct {
	urn         string
	projectURN  string
	name        string
	description string
}

var foldersCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new folder inside a project",
	Long: `Calls CreateFolder to register a new folder.

--project and --name are required.
If --urn is omitted a random notx:folder:<uuid> is generated.

Examples:
  notxctl folders create --project notx:proj:… --name "Q3 Notes"
  notxctl folders create --project notx:proj:… --name "Archive" --desc "Old work" -o json`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if foldersCreateFlags.projectURN == "" {
			return fmt.Errorf("--project is required")
		}
		if foldersCreateFlags.name == "" {
			return fmt.Errorf("--name is required")
		}

		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		urn := foldersCreateFlags.urn
		if urn == "" {
			urn = "notx:folder:" + uuid.New().String()
		}

		resp, err := conn.Projects().CreateFolder(ctx, &pb.CreateFolderRequest{
			Urn:         urn,
			ProjectUrn:  foldersCreateFlags.projectURN,
			Name:        foldersCreateFlags.name,
			Description: foldersCreateFlags.description,
		})
		if err != nil {
			return fmt.Errorf("CreateFolder: %w", err)
		}

		fo := resp.Folder

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"urn":         fo.Urn,
				"project_urn": fo.ProjectUrn,
				"name":        fo.Name,
				"description": fo.Description,
				"created_at":  fo.CreatedAt.AsTime(),
			})
		default:
			fmt.Printf("created  %s\n", fo.Urn)
			fmt.Printf("project  %s\n", fo.ProjectUrn)
			fmt.Printf("name     %s\n", fo.Name)
			if fo.Description != "" {
				fmt.Printf("desc     %s\n", fo.Description)
			}
		}
		return nil
	},
}

func init() {
	f := foldersCreateCmd.Flags()
	f.StringVar(&foldersCreateFlags.urn, "urn", "",
		"URN for the new folder (auto-generated if omitted)")
	f.StringVar(&foldersCreateFlags.projectURN, "project", "",
		"parent project URN notx:proj:… (required)")
	f.StringVar(&foldersCreateFlags.name, "name", "",
		"folder name (required)")
	f.StringVar(&foldersCreateFlags.description, "desc", "",
		"optional folder description")
}

// ─────────────────────────────────────────────────────────────────────────────
// folders update <urn>
// ─────────────────────────────────────────────────────────────────────────────

var foldersUpdateFlags struct {
	name        string
	description string
	deleted     bool
}

var foldersUpdateCmd = &cobra.Command{
	Use:   "update <urn>",
	Short: "Update a folder's name, description, or deleted flag",
	Long: `Calls UpdateFolder to modify an existing folder.

Only the flags you supply are applied. Any omitted flag carries the current
value forward from a pre-update GetFolder fetch.

Examples:
  notxctl folders update notx:folder:… --name "New Name"
  notxctl folders update notx:folder:… --desc "Updated description"
  notxctl folders update notx:folder:… --deleted`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		// Pre-fetch current state so unchanged fields are carried forward.
		getResp, err := conn.Projects().GetFolder(ctx, &pb.GetFolderRequest{
			Urn: args[0],
		})
		if err != nil {
			return fmt.Errorf("GetFolder (pre-update fetch): %w", err)
		}
		current := getResp.Folder

		name := current.Name
		if cmd.Flags().Changed("name") {
			name = foldersUpdateFlags.name
		}

		description := current.Description
		if cmd.Flags().Changed("desc") {
			description = foldersUpdateFlags.description
		}

		deleted := current.Deleted
		if cmd.Flags().Changed("deleted") {
			deleted = foldersUpdateFlags.deleted
		}

		resp, err := conn.Projects().UpdateFolder(ctx, &pb.UpdateFolderRequest{
			Urn:         args[0],
			Name:        name,
			Description: description,
			Deleted:     deleted,
		})
		if err != nil {
			return fmt.Errorf("UpdateFolder: %w", err)
		}

		fo := resp.Folder

		switch outputFromCtx(cmd) {
		case "json":
			return printJSON(map[string]any{
				"urn":         fo.Urn,
				"project_urn": fo.ProjectUrn,
				"name":        fo.Name,
				"description": fo.Description,
				"deleted":     fo.Deleted,
				"updated_at":  fo.UpdatedAt.AsTime(),
			})
		default:
			fmt.Printf("updated  %s\n", fo.Urn)
			fmt.Printf("name     %s\n", fo.Name)
			fmt.Printf("deleted  %s\n", fmtBool(fo.Deleted, "yes", "no"))
		}
		return nil
	},
}

func init() {
	f := foldersUpdateCmd.Flags()
	f.StringVar(&foldersUpdateFlags.name, "name", "",
		"new folder name")
	f.StringVar(&foldersUpdateFlags.description, "desc", "",
		"new folder description")
	f.BoolVar(&foldersUpdateFlags.deleted, "deleted", false,
		"mark the folder as deleted")
}

// ─────────────────────────────────────────────────────────────────────────────
// folders delete <urn>
// ─────────────────────────────────────────────────────────────────────────────

var foldersDeleteCmd = &cobra.Command{
	Use:   "delete <urn>",
	Short: "Delete a folder",
	Long: `Calls DeleteFolder to remove a folder by URN.

Example:
  notxctl folders delete notx:folder:…`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn := connFromCtx(cmd)
		ctx, cancel := rpcCtx(cmd)
		defer cancel()

		resp, err := conn.Projects().DeleteFolder(ctx, &pb.DeleteFolderRequest{
			Urn: args[0],
		})
		if err != nil {
			return fmt.Errorf("DeleteFolder: %w", err)
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
