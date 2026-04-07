package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "notx [file]",
	Short: "notx — note engine CLI",
	Long: `notx is a note engine for creating, managing and inspecting .notx documents.

When called with a file path as the first argument, notx creates a new note
by sending it to the running notx gRPC server (equivalent to "notx add <file>"):

  notx some/file.txt
  notx some/file.txt -d --secure
  notx some/file.txt --addr localhost:9000

The server address is read from ~/.notx/config.json (client.grpc_addr).
Run "notx config" to set it up interactively.

Pass --urn to update an existing note instead of creating a new one:

  notx some/file.txt --urn notx:note:1a9670dd-1a65-481a-ad17-03d77de021e5`,

	// Silence the default "unknown command" error so our custom RunE can
	// intercept bare file arguments and route them to addNoteCmd.
	SilenceErrors: true,
	SilenceUsage:  true,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(infoCmd)
	rootCmd.AddCommand(validateCmd)

	// Mirror the add-note flags on the root command so they work when the user
	// invokes notx directly with a file path:
	//
	//   notx notes.txt -d --secure --addr localhost:9000
	//
	// The actual flag variables live in addnote.go (addNoteFlags); we bind the
	// same pointers here so both "notx add ..." and "notx ..." share one set of
	// values.
	f := rootCmd.Flags()
	f.StringVar(&addNoteFlags.addr, "addr", "",
		"gRPC server address to dial (overrides config client.grpc_addr)")
	f.StringVar(&addNoteFlags.urn, "urn", "",
		"URN of an existing note to update (skips creation, diffs and appends an event)")
	f.BoolVarP(&addNoteFlags.delete, "delete", "d", false,
		"Delete the source file after successfully creating the note")
	f.BoolVar(&addNoteFlags.secure, "secure", false,
		"Mark the note as secure (end-to-end encrypted)")
	f.StringVar(&addNoteFlags.projectURN, "project", "",
		"Project URN to assign the note to (enables candidate detection)")
	f.StringVar(&addNoteFlags.folderURN, "folder", "",
		"Folder URN to assign the note to (optional, requires --project)")
}
