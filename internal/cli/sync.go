package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	pb "github.com/zebaqui/notx-engine/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zebaqui/notx-engine/config"
	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/internal/clientconfig"
	"github.com/zebaqui/notx-engine/internal/cloud"
	"github.com/zebaqui/notx-engine/internal/grpcclient"
)

// syncProgressEvent is written as newline-delimited JSON to ~/.notx/sync.log
// and streamed to subscribers via GET /v1/sync/stream on the local engine.
type syncProgressEvent struct {
	Type      string `json:"type"` // "start"|"note"|"done"|"error"
	Timestamp string `json:"ts"`   // RFC3339

	// type=start
	Total int `json:"total,omitempty"`

	// type=note
	NoteURN   string `json:"note_urn,omitempty"`
	NoteName  string `json:"note_name,omitempty"`
	Action    string `json:"action,omitempty"`    // "skip"|"push"|"pull"|"rebase"|"fast-forward"|"skip-secure"|"error"
	Direction string `json:"direction,omitempty"` // "→ cloud"|"→ local"|"↔ both"
	FromSeq   int    `json:"from_seq,omitempty"`
	ToSeq     int    `json:"to_seq,omitempty"`
	Done      int    `json:"done,omitempty"`

	// type=done
	Pushed  int `json:"pushed,omitempty"`
	Pulled  int `json:"pulled,omitempty"`
	Rebased int `json:"rebased,omitempty"`
	Synced  int `json:"synced,omitempty"`
	Skipped int `json:"skipped,omitempty"`

	// type=error
	Error string `json:"error,omitempty"`
}

type syncLogger struct {
	f *os.File
}

func openSyncLogger() *syncLogger {
	dir, err := clientconfig.Dir()
	if err != nil {
		return &syncLogger{}
	}
	p := filepath.Join(dir, "sync.log")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return &syncLogger{}
	}
	return &syncLogger{f: f}
}

func (l *syncLogger) emit(ev syncProgressEvent) {
	if l.f == nil {
		return
	}
	ev.Timestamp = time.Now().UTC().Format(time.RFC3339)
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	b = append(b, '\n')
	_, _ = l.f.Write(b)
}

func (l *syncLogger) close() {
	if l.f != nil {
		l.f.Close()
	}
}

// -----------------------------------------------------------------------------
// notx sync
// -----------------------------------------------------------------------------

var syncFlags struct {
	push    bool
	pull    bool
	dryRun  bool
	verbose bool
	certDir string
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync notes between local engine and notx cloud",
	Long: `Sync notes between the local notx engine (localhost:4060) and the notx
cloud (notx.zebaqui.com).

When this server has been paired with a cloud authority (via notx pair), sync
uses the mTLS gRPC SyncService.SyncNotes bidirectional stream for the push
direction, and falls back to the cloud HTTP API for pulling cloud-only notes.

Without pairing, sync falls back entirely to the HTTP REST path.

Sync works event-by-event, preserving full history. When both sides have
diverged from a common ancestor, a timestamp-based rebase is performed:
diverging events are sorted by created_at (tie-broken by event URN) and
re-sequenced deterministically so both sides converge to the same history.

Flags:
  --push     push local notes to cloud only (no pull)
  --pull     pull cloud notes to local only (no push)
  --dry-run  compute the full sync plan and print it without making any writes
  --cert-dir directory containing mTLS client certs (default ~/.notx/certs/)
  -v         print per-note detail`,
	RunE: runSync,
}

func init() {
	f := syncCmd.Flags()
	f.BoolVar(&syncFlags.push, "push", false, "local -> cloud only")
	f.BoolVar(&syncFlags.pull, "pull", false, "cloud -> local only")
	f.BoolVar(&syncFlags.dryRun, "dry-run", false, "print what would happen, no writes")
	f.BoolVarP(&syncFlags.verbose, "verbose", "v", false, "print per-note detail")

	// Default cert dir: ~/.notx/certs/
	defaultCertDir := ""
	if dir, err := clientconfig.Dir(); err == nil {
		defaultCertDir = filepath.Join(dir, "certs")
	}
	f.StringVar(&syncFlags.certDir, "cert-dir", defaultCertDir,
		"directory holding mTLS client certs issued after notx pair")

	rootCmd.AddCommand(syncCmd)
}

// Reuse noteHeader, noteEvent, lineEntry, eventsResponse from pull.go.

// syncReceiveRequest is the body for POST /v1/notes/:urn/receive (local) and
// POST /engine/v1/notes/:urn/receive (cloud).
type syncReceiveRequest struct {
	Header noteHeader  `json:"header"`
	Events []noteEvent `json:"events"`
}

type syncReceiveResponse struct {
	NoteURN      string `json:"note_urn"`
	EventsStored int    `json:"events_stored"`
	Error        string `json:"error,omitempty"`
}

// -----------------------------------------------------------------------------
// Sync plan types
// -----------------------------------------------------------------------------

type syncAction int

const (
	syncActionSkip        syncAction = iota // already in sync
	syncActionFastForward                   // one side is ahead
	syncActionRebase                        // both sides diverged
	syncActionPushNew                       // note only exists locally
	syncActionPullNew                       // note only exists on cloud
	syncActionSkipSecure                    // secure note - skip
)

type noteplan struct {
	action   syncAction
	noteURN  string
	noteName string
	noteType string

	// populated depending on action
	forkSeq       int
	localNewCount int
	cloudNewCount int
	localMaxSeq   int
	cloudMaxSeq   int
	eventCount    int

	// merged event stream to send
	mergedEvents []noteEvent
	mergedHeader noteHeader
	targetLocal  bool
	targetCloud  bool
}

// -----------------------------------------------------------------------------
// runSync
// -----------------------------------------------------------------------------

func runSync(cmd *cobra.Command, _ []string) error {
	if syncFlags.push && syncFlags.pull {
		return fmt.Errorf("--push and --pull are mutually exclusive")
	}

	out := cmd.OutOrStdout()

	syncLog := openSyncLogger()
	defer syncLog.close()

	// Load configs
	fileCfg, _ := clientconfig.Load()
	clientJSON, err := clientconfig.LoadClientJSON()
	if err != nil {
		return fmt.Errorf("load client.json: %w", err)
	}

	// Auth
	token, err := cloud.EnsureToken(clientJSON)
	if err != nil {
		return fmt.Errorf("cloud auth: %w", err)
	}

	// Decode email for display.
	accountLabel := "(unknown)"
	if claims, claimsErr := cloud.ParseAllClaims(token); claimsErr == nil {
		if claims.Email != "" {
			accountLabel = claims.Email
		} else if claims.Username != "" {
			accountLabel = claims.Username
		}
	}

	// Discover the local server's HTTP port from the ports file written at
	// startup. Fall back to config value and then hardcoded default.
	apiBase := fileCfg.Admin.APIAddr
	if apiBase == "" {
		apiBase = "http://localhost:4060"
	}

	// Read peer cert dir and peer authority from the ports file (written at
	// daemon startup) when available.
	peerCertDir := syncFlags.certDir
	peerAuthority := ""
	if p, portsErr := ReadServerPorts(); portsErr == nil {
		if p.HTTPPort > 0 {
			apiBase = fmt.Sprintf("http://localhost:%d", p.HTTPPort)
		}
		if p.PeerCertDir != "" {
			peerCertDir = p.PeerCertDir
		}
		if p.PeerAuthority != "" {
			peerAuthority = p.PeerAuthority
		}
	}
	apiBase = strings.TrimRight(apiBase, "/")

	deviceURN := fileCfg.Admin.AdminDeviceURN
	if deviceURN == "" {
		deviceURN = config.DefaultAdminDeviceURN
	}

	// Determine if we can use gRPC mTLS path.
	useGRPC := false
	certFile := filepath.Join(peerCertDir, "server.crt")
	keyFile := filepath.Join(peerCertDir, "server.key")
	caFile := filepath.Join(peerCertDir, "ca.crt")

	if peerAuthority != "" && peerCertDir != "" {
		if _, e1 := os.Stat(certFile); e1 == nil {
			if _, e2 := os.Stat(keyFile); e2 == nil {
				if _, e3 := os.Stat(caFile); e3 == nil {
					useGRPC = true
				}
			}
		}
	}

	cloudBase := cloud.CloudBaseURL()
	fmt.Fprintf(out, "\n  \033[1;34m▶\033[0m  Syncing with notx cloud\n")
	fmt.Fprintf(out, "     local   → \033[36m%s\033[0m\n", apiBase)
	fmt.Fprintf(out, "     cloud   → \033[36m%s\033[0m\n", cloudBase)
	fmt.Fprintf(out, "     account : %s\n", accountLabel)
	if useGRPC {
		fmt.Fprintf(out, "     transport: \033[32mgRPC mTLS\033[0m → \033[36m%s\033[0m\n\n", peerAuthority)
	} else {
		fmt.Fprintf(out, "     transport: HTTP REST\n")
		if peerAuthority == "" {
			fmt.Fprintf(out, "     \033[33m⚠\033[0m  No peer authority configured — run \033[1mnotx pair\033[0m to enable gRPC sync\n")
		} else {
			fmt.Fprintf(out, "     \033[33m⚠\033[0m  mTLS certs not found in %s — falling back to HTTP\n", peerCertDir)
		}
		fmt.Fprintln(out)
	}

	noteClient := cloud.NewNoteClient(token)

	// Scan notes
	fmt.Fprintf(out, "  Scanning notes...\n")

	localHeaders, err := syncListLocalNotes(apiBase, deviceURN)
	if err != nil {
		syncLog.emit(syncProgressEvent{Type: "error", Error: err.Error()})
		return fmt.Errorf("list local notes: %w", err)
	}

	ctx30 := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 30*time.Second)
	}

	cctx, ccancel := ctx30()
	cloudHeaders, err := noteClient.ListNotes(cctx)
	ccancel()
	if err != nil {
		syncLog.emit(syncProgressEvent{Type: "error", Error: err.Error()})
		return fmt.Errorf("list cloud notes: %w", err)
	}

	fmt.Fprintf(out, "     local : %d notes\n", len(localHeaders))
	fmt.Fprintf(out, "     cloud : %d notes\n\n", len(cloudHeaders))

	// Build URN lookup maps
	localByURN := make(map[string]noteHeader, len(localHeaders))
	for _, h := range localHeaders {
		localByURN[h.URN] = h
	}
	cloudByURN := make(map[string]cloud.NoteHeader, len(cloudHeaders))
	for _, h := range cloudHeaders {
		cloudByURN[h.URN] = h
	}

	// Union of all URNs, stable order: local first then cloud-only.
	var allURNs []string
	seen := make(map[string]bool)
	for _, h := range localHeaders {
		if !seen[h.URN] {
			allURNs = append(allURNs, h.URN)
			seen[h.URN] = true
		}
	}
	for _, h := range cloudHeaders {
		if !seen[h.URN] {
			allURNs = append(allURNs, h.URN)
			seen[h.URN] = true
		}
	}

	// Build sync plans
	plans := make([]noteplan, 0, len(allURNs))
	for _, urn := range allURNs {
		lh, hasLocal := localByURN[urn]
		ch, hasCloud := cloudByURN[urn]

		// Determine note name/type from whichever side has it.
		noteName := ""
		nType := "normal"
		var hdr noteHeader
		if hasLocal {
			noteName = lh.Name
			nType = lh.NoteType
			hdr = lh
		} else {
			noteName = ch.Name
			nType = ch.NoteType
			hdr = noteHeader{
				URN:       ch.URN,
				Name:      ch.Name,
				NoteType:  ch.NoteType,
				Deleted:   ch.Deleted,
				CreatedAt: ch.CreatedAt,
				UpdatedAt: ch.UpdatedAt,
			}
		}

		// Skip secure notes.
		if nType == "secure" {
			plans = append(plans, noteplan{
				action:   syncActionSkipSecure,
				noteURN:  urn,
				noteName: noteName,
				noteType: nType,
			})
			continue
		}

		switch {
		case hasLocal && !hasCloud:
			if syncFlags.pull {
				plans = append(plans, noteplan{action: syncActionSkip, noteURN: urn, noteName: noteName})
				continue
			}
			lEv, err := syncGetLocalEvents(apiBase, deviceURN, urn)
			if err != nil {
				return fmt.Errorf("get local events for %s: %w", urn, err)
			}
			plans = append(plans, noteplan{
				action:       syncActionPushNew,
				noteURN:      urn,
				noteName:     noteName,
				noteType:     nType,
				eventCount:   len(lEv),
				mergedEvents: lEv,
				mergedHeader: hdr,
				targetCloud:  true,
			})

		case !hasLocal && hasCloud:
			if syncFlags.push {
				plans = append(plans, noteplan{action: syncActionSkip, noteURN: urn, noteName: noteName})
				continue
			}
			cctx2, ccancel2 := ctx30()
			cEvResp, err := noteClient.GetEvents(cctx2, urn)
			ccancel2()
			if err != nil {
				return fmt.Errorf("get cloud events for %s: %w", urn, err)
			}
			cEv := syncCloudEventsToLocal(cEvResp.Events)
			plans = append(plans, noteplan{
				action:       syncActionPullNew,
				noteURN:      urn,
				noteName:     noteName,
				noteType:     nType,
				eventCount:   len(cEv),
				mergedEvents: cEv,
				mergedHeader: hdr,
				targetLocal:  true,
			})

		case hasLocal && hasCloud:
			lEv, err := syncGetLocalEvents(apiBase, deviceURN, urn)
			if err != nil {
				return fmt.Errorf("get local events for %s: %w", urn, err)
			}
			cctx3, ccancel3 := ctx30()
			cEvResp, err := noteClient.GetEvents(cctx3, urn)
			ccancel3()
			if err != nil {
				return fmt.Errorf("get cloud events for %s: %w", urn, err)
			}
			cEv := syncCloudEventsToLocal(cEvResp.Events)

			plan := buildMergePlan(urn, noteName, nType, hdr, lEv, cEv)
			if syncFlags.push {
				plan.targetLocal = false
			}
			if syncFlags.pull {
				plan.targetCloud = false
			}
			plans = append(plans, plan)
		}
	}

	// Emit start event
	syncLog.emit(syncProgressEvent{Type: "start", Total: len(plans)})

	// Print plan
	total := len(plans)
	for i, p := range plans {
		label := fmt.Sprintf("  [%d/%d] %-30s", i+1, total, truncateName(p.noteName, 30))
		switch p.action {
		case syncActionSkip:
			if syncFlags.verbose {
				fmt.Fprintf(out, "%s skipped (already in sync)\n", label)
			}
		case syncActionSkipSecure:
			fmt.Fprintf(out, "%s \033[33mskipped (secure note)\033[0m\n", label)
		case syncActionFastForward:
			dir := "→ cloud"
			if p.targetLocal {
				dir = "→ local"
			}
			fmt.Fprintf(out, "%s fast-forward %s  (seq %d → %d)\n", label, dir, p.forkSeq, len(p.mergedEvents))
		case syncActionRebase:
			fmt.Fprintf(out, "%s rebase  (fork@%d, %d local + %d cloud → merged seq %s)\n", label, p.forkSeq, p.localNewCount, p.cloudNewCount, rebaseSeqRange(p.forkSeq, p.localNewCount+p.cloudNewCount))
		case syncActionPushNew:
			fmt.Fprintf(out, "%s push → cloud  (new, %d events)\n", label, p.eventCount)
		case syncActionPullNew:
			fmt.Fprintf(out, "%s pull → local  (new, %d events)\n", label, p.eventCount)
		}
	}
	fmt.Fprintln(out)

	if syncFlags.dryRun {
		fmt.Fprintf(out, "  \033[33m(dry-run — no writes performed)\033[0m\n\n")
		return nil
	}

	// -------------------------------------------------------------------------
	// Execute plans
	// -------------------------------------------------------------------------

	if useGRPC {
		return runSyncGRPC(cmd, plans, peerCertDir, peerAuthority, certFile, keyFile, caFile, apiBase, deviceURN, noteClient, ctx30, syncLog, out)
	}
	return runSyncHTTP(cmd, plans, apiBase, deviceURN, noteClient, ctx30, syncLog, out)
}

// -----------------------------------------------------------------------------
// gRPC mTLS sync execution
// -----------------------------------------------------------------------------

func runSyncGRPC(
	cmd *cobra.Command,
	plans []noteplan,
	peerCertDir, peerAuthority string,
	certFile, keyFile, caFile string,
	apiBase, deviceURN string,
	noteClient *cloud.NoteClient,
	ctx30 func() (context.Context, context.CancelFunc),
	syncLog *syncLogger,
	out io.Writer,
) error {
	// Load mTLS credentials.
	clientCert, err := grpcclient.LoadClientCert(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("load mTLS client cert: %w", err)
	}
	caPool, err := grpcclient.LoadCAPool(caFile)
	if err != nil {
		return fmt.Errorf("load mTLS CA pool: %w", err)
	}

	conn, err := grpcclient.DialMTLS(peerAuthority, clientCert, caPool)
	if err != nil {
		return fmt.Errorf("dial mTLS %s: %w", peerAuthority, err)
	}
	defer conn.Close()

	syncClient := conn.Sync()

	// Open a bidirectional stream with a generous timeout.
	streamCtx, streamCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer streamCancel()

	stream, err := syncClient.SyncNotes(streamCtx)
	if err != nil {
		return fmt.Errorf("open SyncNotes stream: %w", err)
	}

	// Collect notes that need to be pushed to cloud (via gRPC) so we can send
	// them in one pass, then receive results.
	type pushEntry struct {
		planIdx int
	}
	var pushPlans []pushEntry

	for i, p := range plans {
		switch p.action {
		case syncActionPushNew, syncActionFastForward, syncActionRebase:
			if p.targetCloud {
				pushPlans = append(pushPlans, pushEntry{planIdx: i})
				msg := &pb.SyncNoteMessage{
					Header: syncToProtoHeader(p.mergedHeader, p.noteType),
					Events: syncToProtoEvents(p.mergedEvents),
				}
				if sendErr := stream.Send(msg); sendErr != nil {
					return fmt.Errorf("send SyncNoteMessage for %s: %w", p.noteURN, sendErr)
				}
			}
		}
	}

	if err := stream.CloseSend(); err != nil {
		return fmt.Errorf("close send on SyncNotes stream: %w", err)
	}

	// Receive results.
	// EventsStored == -1 means the cloud has this note but we don't — pull it.
	pushResultByURN := make(map[string]*pb.SyncNoteResult)
	cloudOnlyURNs := []string{}

	for {
		result, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			return fmt.Errorf("receive SyncNoteResult: %w", recvErr)
		}
		if result.EventsStored == -1 {
			cloudOnlyURNs = append(cloudOnlyURNs, result.NoteUrn)
		} else {
			pushResultByURN[result.NoteUrn] = result
		}
	}

	// Now execute local writes for everything that targeted local, and handle
	// cloud-only pull, and count outcomes.
	var (
		synced    int
		rebased   int
		pushed    int
		pulled    int
		skipped   int
		doneCount int
	)

	for _, p := range plans {
		switch p.action {
		case syncActionSkip, syncActionSkipSecure:
			skipped++
			doneCount++
			action := "skip"
			if p.action == syncActionSkipSecure {
				action = "skip-secure"
			}
			syncLog.emit(syncProgressEvent{
				Type:     "note",
				NoteURN:  p.noteURN,
				NoteName: p.noteName,
				Action:   action,
				Done:     doneCount,
			})

		case syncActionFastForward:
			synced++
			// gRPC push to cloud already done above (if targetCloud).
			// Handle local write.
			if p.targetLocal {
				lctx, lcancel := ctx30()
				if writeErr := syncLocalReceive(lctx, apiBase, deviceURN, p.mergedHeader, p.mergedEvents); writeErr != nil {
					lcancel()
					syncLog.emit(syncProgressEvent{Type: "error", Error: writeErr.Error()})
					return fmt.Errorf("fast-forward to local for %s: %w", p.noteURN, writeErr)
				}
				lcancel()
			}
			doneCount++
			ffDir := "→ cloud"
			if p.targetLocal {
				ffDir = "→ local"
			}
			syncLog.emit(syncProgressEvent{
				Type:      "note",
				NoteURN:   p.noteURN,
				NoteName:  p.noteName,
				Action:    "fast-forward",
				Direction: ffDir,
				FromSeq:   p.forkSeq,
				ToSeq:     len(p.mergedEvents),
				Done:      doneCount,
			})

		case syncActionRebase:
			rebased++
			synced++
			// gRPC push already done; write merged events to local.
			lctx2, lcancel2 := ctx30()
			if writeErr := syncLocalReceive(lctx2, apiBase, deviceURN, p.mergedHeader, p.mergedEvents); writeErr != nil {
				lcancel2()
				syncLog.emit(syncProgressEvent{Type: "error", Error: writeErr.Error()})
				return fmt.Errorf("rebase to local for %s: %w", p.noteURN, writeErr)
			}
			lcancel2()
			doneCount++
			syncLog.emit(syncProgressEvent{
				Type:      "note",
				NoteURN:   p.noteURN,
				NoteName:  p.noteName,
				Action:    "rebase",
				Direction: "↔ both",
				FromSeq:   p.forkSeq,
				ToSeq:     p.forkSeq + p.localNewCount + p.cloudNewCount,
				Done:      doneCount,
			})

		case syncActionPushNew:
			pushed++
			synced++
			// gRPC push already sent above; check for errors in the result.
			if res, ok := pushResultByURN[p.noteURN]; ok && res.Error != "" {
				syncLog.emit(syncProgressEvent{Type: "error", Error: res.Error})
				return fmt.Errorf("push new note %s to cloud: %s", p.noteURN, res.Error)
			}
			doneCount++
			syncLog.emit(syncProgressEvent{
				Type:      "note",
				NoteURN:   p.noteURN,
				NoteName:  p.noteName,
				Action:    "push",
				Direction: "→ cloud",
				Done:      doneCount,
			})

		case syncActionPullNew:
			pulled++
			synced++
			lctx3, lcancel3 := ctx30()
			if writeErr := syncLocalReceive(lctx3, apiBase, deviceURN, p.mergedHeader, p.mergedEvents); writeErr != nil {
				lcancel3()
				syncLog.emit(syncProgressEvent{Type: "error", Error: writeErr.Error()})
				return fmt.Errorf("pull new note %s to local: %w", p.noteURN, writeErr)
			}
			lcancel3()
			doneCount++
			syncLog.emit(syncProgressEvent{
				Type:      "note",
				NoteURN:   p.noteURN,
				NoteName:  p.noteName,
				Action:    "pull",
				Direction: "→ local",
				Done:      doneCount,
			})
		}
	}

	// Pull any cloud-only notes the server told us about (EventsStored == -1).
	if !syncFlags.push && len(cloudOnlyURNs) > 0 {
		fmt.Fprintf(out, "\n  Pulling %d cloud-only note(s) reported by gRPC server...\n", len(cloudOnlyURNs))
		for _, urn := range cloudOnlyURNs {
			cctx, ccancel := ctx30()
			cEvResp, cErr := noteClient.GetEvents(cctx, urn)
			ccancel()
			if cErr != nil {
				syncLog.emit(syncProgressEvent{Type: "error", Error: cErr.Error()})
				return fmt.Errorf("get cloud events for cloud-only note %s: %w", urn, cErr)
			}

			cEv := syncCloudEventsToLocal(cEvResp.Events)
			hdr := noteHeader{URN: urn}
			if len(cEv) > 0 {
				hdr.URN = urn
			}

			lctx, lcancel := ctx30()
			if writeErr := syncLocalReceive(lctx, apiBase, deviceURN, hdr, cEv); writeErr != nil {
				lcancel()
				syncLog.emit(syncProgressEvent{Type: "error", Error: writeErr.Error()})
				return fmt.Errorf("pull cloud-only note %s to local: %w", urn, writeErr)
			}
			lcancel()

			pulled++
			synced++
			doneCount++
			syncLog.emit(syncProgressEvent{
				Type:      "note",
				NoteURN:   urn,
				Action:    "pull",
				Direction: "→ local",
				Done:      doneCount,
			})
		}
	}

	printSyncSummary(out, synced, rebased, pushed, pulled, skipped)
	syncLog.emit(syncProgressEvent{
		Type:    "done",
		Pushed:  pushed,
		Pulled:  pulled,
		Rebased: rebased,
		Synced:  synced,
		Skipped: skipped,
	})
	return nil
}

// -----------------------------------------------------------------------------
// HTTP REST sync execution (fallback / pre-pairing)
// -----------------------------------------------------------------------------

func runSyncHTTP(
	_ *cobra.Command,
	plans []noteplan,
	apiBase, deviceURN string,
	noteClient *cloud.NoteClient,
	ctx30 func() (context.Context, context.CancelFunc),
	syncLog *syncLogger,
	out io.Writer,
) error {
	var (
		synced    int
		rebased   int
		pushed    int
		pulled    int
		skipped   int
		doneCount int
	)

	for _, p := range plans {
		switch p.action {
		case syncActionSkip, syncActionSkipSecure:
			skipped++
			doneCount++
			action := "skip"
			if p.action == syncActionSkipSecure {
				action = "skip-secure"
			}
			syncLog.emit(syncProgressEvent{
				Type:     "note",
				NoteURN:  p.noteURN,
				NoteName: p.noteName,
				Action:   action,
				Done:     doneCount,
			})
			continue

		case syncActionFastForward:
			synced++
			if p.targetCloud {
				cctx4, ccancel4 := ctx30()
				_, err := noteClient.ReceiveNote(cctx4, syncToCloudHeader(p.mergedHeader), syncToCloudEvents(p.mergedEvents))
				ccancel4()
				if err != nil {
					syncLog.emit(syncProgressEvent{Type: "error", Error: err.Error()})
					return fmt.Errorf("fast-forward to cloud for %s: %w", p.noteURN, err)
				}
			}
			if p.targetLocal {
				lctx, lcancel := ctx30()
				err := syncLocalReceive(lctx, apiBase, deviceURN, p.mergedHeader, p.mergedEvents)
				lcancel()
				if err != nil {
					syncLog.emit(syncProgressEvent{Type: "error", Error: err.Error()})
					return fmt.Errorf("fast-forward to local for %s: %w", p.noteURN, err)
				}
			}
			doneCount++
			ffDir := "→ cloud"
			if p.targetLocal {
				ffDir = "→ local"
			}
			syncLog.emit(syncProgressEvent{
				Type:      "note",
				NoteURN:   p.noteURN,
				NoteName:  p.noteName,
				Action:    "fast-forward",
				Direction: ffDir,
				FromSeq:   p.forkSeq,
				ToSeq:     len(p.mergedEvents),
				Done:      doneCount,
			})

		case syncActionRebase:
			rebased++
			synced++
			cctx5, ccancel5 := ctx30()
			_, err := noteClient.ReceiveNote(cctx5, syncToCloudHeader(p.mergedHeader), syncToCloudEvents(p.mergedEvents))
			ccancel5()
			if err != nil {
				syncLog.emit(syncProgressEvent{Type: "error", Error: err.Error()})
				return fmt.Errorf("rebase to cloud for %s: %w", p.noteURN, err)
			}
			lctx2, lcancel2 := ctx30()
			err = syncLocalReceive(lctx2, apiBase, deviceURN, p.mergedHeader, p.mergedEvents)
			lcancel2()
			if err != nil {
				syncLog.emit(syncProgressEvent{Type: "error", Error: err.Error()})
				return fmt.Errorf("rebase to local for %s: %w", p.noteURN, err)
			}
			doneCount++
			syncLog.emit(syncProgressEvent{
				Type:      "note",
				NoteURN:   p.noteURN,
				NoteName:  p.noteName,
				Action:    "rebase",
				Direction: "↔ both",
				FromSeq:   p.forkSeq,
				ToSeq:     p.forkSeq + p.localNewCount + p.cloudNewCount,
				Done:      doneCount,
			})

		case syncActionPushNew:
			pushed++
			synced++
			cctx6, ccancel6 := ctx30()
			_, err := noteClient.ReceiveNote(cctx6, syncToCloudHeader(p.mergedHeader), syncToCloudEvents(p.mergedEvents))
			ccancel6()
			if err != nil {
				syncLog.emit(syncProgressEvent{Type: "error", Error: err.Error()})
				return fmt.Errorf("push new note %s to cloud: %w", p.noteURN, err)
			}
			doneCount++
			syncLog.emit(syncProgressEvent{
				Type:      "note",
				NoteURN:   p.noteURN,
				NoteName:  p.noteName,
				Action:    "push",
				Direction: "→ cloud",
				Done:      doneCount,
			})

		case syncActionPullNew:
			pulled++
			synced++
			lctx3, lcancel3 := ctx30()
			err := syncLocalReceive(lctx3, apiBase, deviceURN, p.mergedHeader, p.mergedEvents)
			lcancel3()
			if err != nil {
				syncLog.emit(syncProgressEvent{Type: "error", Error: err.Error()})
				return fmt.Errorf("pull new note %s to local: %w", p.noteURN, err)
			}
			doneCount++
			syncLog.emit(syncProgressEvent{
				Type:      "note",
				NoteURN:   p.noteURN,
				NoteName:  p.noteName,
				Action:    "pull",
				Direction: "→ local",
				Done:      doneCount,
			})
		}
	}

	printSyncSummary(out, synced, rebased, pushed, pulled, skipped)
	syncLog.emit(syncProgressEvent{
		Type:    "done",
		Pushed:  pushed,
		Pulled:  pulled,
		Rebased: rebased,
		Synced:  synced,
		Skipped: skipped,
	})
	return nil
}

func printSyncSummary(out io.Writer, synced, rebased, pushed, pulled, skipped int) {
	fmt.Fprintf(out, "  \033[1;32m✓\033[0m  Sync complete\n")
	fmt.Fprintf(out, "     synced    : %d notes\n", synced)
	fmt.Fprintf(out, "     rebased   : %d notes\n", rebased)
	fmt.Fprintf(out, "     pushed    : %d notes (new on cloud)\n", pushed)
	fmt.Fprintf(out, "     pulled    : %d notes (new on local)\n", pulled)
	fmt.Fprintf(out, "     skipped   : %d notes (already in sync)\n\n", skipped)
}

// -----------------------------------------------------------------------------
// Rebase / merge logic
// -----------------------------------------------------------------------------

// buildMergePlan computes the sync plan for a note that exists on both sides.
func buildMergePlan(
	urn, name, noteType string,
	hdr noteHeader,
	localEvents, cloudEvents []noteEvent,
) noteplan {
	// Find fork point: last event whose URN appears on both sides.
	localURNToSeq := make(map[string]int, len(localEvents))
	for _, e := range localEvents {
		localURNToSeq[e.URN] = e.Sequence
	}

	forkSeq := 0
	for _, ce := range cloudEvents {
		if seq, ok := localURNToSeq[ce.URN]; ok && seq > forkSeq {
			forkSeq = seq
		}
	}

	// Collect diverging events.
	var localNew, cloudNew []noteEvent
	for _, e := range localEvents {
		if e.Sequence > forkSeq {
			localNew = append(localNew, e)
		}
	}
	for _, e := range cloudEvents {
		if e.Sequence > forkSeq {
			cloudNew = append(cloudNew, e)
		}
	}

	// Shared prefix (identical on both sides).
	var shared []noteEvent
	for _, e := range localEvents {
		if e.Sequence <= forkSeq {
			shared = append(shared, e)
		}
	}

	// Already in sync.
	if len(localNew) == 0 && len(cloudNew) == 0 {
		return noteplan{action: syncActionSkip, noteURN: urn, noteName: name}
	}

	// Fast-forward: only local has new events.
	if len(localNew) > 0 && len(cloudNew) == 0 {
		full := append(shared, localNew...)
		return noteplan{
			action:       syncActionFastForward,
			noteURN:      urn,
			noteName:     name,
			noteType:     noteType,
			forkSeq:      forkSeq,
			localMaxSeq:  len(localEvents),
			cloudMaxSeq:  len(cloudEvents),
			mergedEvents: full,
			mergedHeader: hdr,
			targetCloud:  true,
		}
	}

	// Fast-forward: only cloud has new events.
	if len(cloudNew) > 0 && len(localNew) == 0 {
		full := append(shared, cloudNew...)
		return noteplan{
			action:       syncActionFastForward,
			noteURN:      urn,
			noteName:     name,
			noteType:     noteType,
			forkSeq:      forkSeq,
			localMaxSeq:  len(localEvents),
			cloudMaxSeq:  len(cloudEvents),
			mergedEvents: full,
			mergedHeader: hdr,
			targetLocal:  true,
		}
	}

	// Both sides diverged: timestamp rebase.
	all := append(localNew, cloudNew...)
	sort.SliceStable(all, func(i, j int) bool {
		ti := syncParseTime(all[i].CreatedAt)
		tj := syncParseTime(all[j].CreatedAt)
		if ti.Equal(tj) {
			return all[i].URN < all[j].URN
		}
		return ti.Before(tj)
	})

	rebased := make([]noteEvent, len(all))
	for i, e := range all {
		newURN := core.NewURN(core.ObjectTypeEvent).String()
		rebased[i] = noteEvent{
			URN:       newURN,
			Sequence:  forkSeq + 1 + i,
			AuthorURN: e.AuthorURN,
			CreatedAt: e.CreatedAt,
			Entries:   e.Entries,
		}
	}

	full := append(shared, rebased...)
	return noteplan{
		action:        syncActionRebase,
		noteURN:       urn,
		noteName:      name,
		noteType:      noteType,
		forkSeq:       forkSeq,
		localNewCount: len(localNew),
		cloudNewCount: len(cloudNew),
		mergedEvents:  full,
		mergedHeader:  hdr,
		targetLocal:   true,
		targetCloud:   true,
	}
}

// -----------------------------------------------------------------------------
// Local engine HTTP helpers
// -----------------------------------------------------------------------------

// syncListLocalNotes fetches all notes from the local engine, paginating as needed.
func syncListLocalNotes(apiBase, deviceURN string) ([]noteHeader, error) {
	var all []noteHeader
	pageToken := ""
	httpClient := &http.Client{Timeout: 30 * time.Second}

	for {
		url := apiBase + "/v1/notes?page_size=500"
		if pageToken != "" {
			url += "&page_token=" + pageToken
		}

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("build list notes request: %w", err)
		}
		req.Header.Set("X-Device-ID", deviceURN)

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("GET %s: %w", url, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read list notes response: %w", err)
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("list notes: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		var result noteListResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("decode list notes response: %w", err)
		}
		all = append(all, result.Notes...)
		if result.NextPageToken == "" {
			break
		}
		pageToken = result.NextPageToken
	}
	return all, nil
}

// syncGetLocalEvents fetches the full event list for a note from the local engine.
func syncGetLocalEvents(apiBase, deviceURN, noteURN string) ([]noteEvent, error) {
	url := apiBase + "/v1/notes/" + percentEncodeURN(noteURN) + "/events"
	httpClient := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build get events request: %w", err)
	}
	req.Header.Set("X-Device-ID", deviceURN)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read events response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("get events: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result eventsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode events response: %w", err)
	}
	return result.Events, nil
}

// syncLocalReceive pushes a full event stream to the local engine.
func syncLocalReceive(ctx context.Context, apiBase, deviceURN string, hdr noteHeader, events []noteEvent) error {
	reqBody := syncReceiveRequest{Header: hdr, Events: events}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal receive request: %w", err)
	}

	url := apiBase + "/v1/notes/" + percentEncodeURN(hdr.URN) + "/receive"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("build receive request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-ID", deviceURN)

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return fmt.Errorf("read receive response: %w", err)
	}
	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if jsonErr := json.Unmarshal(body, &errResp); jsonErr == nil && errResp.Error != "" {
			return fmt.Errorf("local receive: %s (HTTP %d)", errResp.Error, resp.StatusCode)
		}
		return fmt.Errorf("local receive: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result syncReceiveResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decode receive response: %w", err)
	}
	if result.Error != "" {
		return fmt.Errorf("local receive: %s", result.Error)
	}
	return nil
}

// -----------------------------------------------------------------------------
// Proto conversion helpers
// -----------------------------------------------------------------------------

// lineOpFromString converts op string to int32 as used by the proto LineEntry.
// "set" → 0, "set_empty" → 1, "delete" → 2. Unknown → 0.
func lineOpFromString(op string) int32 {
	switch op {
	case "set":
		return 0
	case "set_empty":
		return 1
	case "delete":
		return 2
	default:
		return 0
	}
}

// noteTypeFromString maps the string note type to a pb.NoteType enum value.
func noteTypeFromString(nType string) pb.NoteType {
	switch nType {
	case "normal":
		return pb.NoteType_NOTE_TYPE_NORMAL
	case "secure":
		return pb.NoteType_NOTE_TYPE_SECURE
	default:
		return pb.NoteType_NOTE_TYPE_UNSPECIFIED
	}
}

// syncToProtoHeader converts a local noteHeader to a *pb.NoteHeader.
func syncToProtoHeader(h noteHeader, noteType string) *pb.NoteHeader {
	hdr := &pb.NoteHeader{
		Urn:        h.URN,
		Name:       h.Name,
		NoteType:   noteTypeFromString(noteType),
		ProjectUrn: h.ProjectURN,
		FolderUrn:  h.FolderURN,
		Deleted:    h.Deleted,
	}
	if h.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, h.CreatedAt); err == nil {
			hdr.CreatedAt = timestamppb.New(t)
		} else if t, err := time.Parse(time.RFC3339, h.CreatedAt); err == nil {
			hdr.CreatedAt = timestamppb.New(t)
		}
	}
	if h.UpdatedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, h.UpdatedAt); err == nil {
			hdr.UpdatedAt = timestamppb.New(t)
		} else if t, err := time.Parse(time.RFC3339, h.UpdatedAt); err == nil {
			hdr.UpdatedAt = timestamppb.New(t)
		}
	}
	return hdr
}

// syncToProtoEvents converts []noteEvent to []*pb.Event.
func syncToProtoEvents(events []noteEvent) []*pb.Event {
	out := make([]*pb.Event, len(events))
	for i, e := range events {
		entries := make([]*pb.LineEntry, len(e.Entries))
		for j, ent := range e.Entries {
			entries[j] = &pb.LineEntry{
				Op:         lineOpFromString(ent.Op),
				LineNumber: int32(ent.LineNumber),
				Content:    ent.Content,
			}
		}
		var createdAt *timestamppb.Timestamp
		if e.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, e.CreatedAt); err == nil {
				createdAt = timestamppb.New(t)
			} else if t, err := time.Parse(time.RFC3339, e.CreatedAt); err == nil {
				createdAt = timestamppb.New(t)
			}
		}
		out[i] = &pb.Event{
			Urn:       e.URN,
			Sequence:  int32(e.Sequence),
			AuthorUrn: e.AuthorURN,
			CreatedAt: createdAt,
			Entries:   entries,
		}
	}
	return out
}

// -----------------------------------------------------------------------------
// Type conversion helpers (HTTP path)
// -----------------------------------------------------------------------------

// syncCloudEventsToLocal converts []cloud.NoteEvent to []noteEvent.
func syncCloudEventsToLocal(events []cloud.NoteEvent) []noteEvent {
	out := make([]noteEvent, len(events))
	for i, e := range events {
		entries := make([]lineEntry, len(e.Entries))
		for j, ent := range e.Entries {
			entries[j] = lineEntry{
				Op:         ent.Op,
				LineNumber: ent.LineNumber,
				Content:    ent.Content,
			}
		}
		out[i] = noteEvent{
			URN:       e.URN,
			Sequence:  e.Sequence,
			AuthorURN: e.AuthorURN,
			CreatedAt: e.CreatedAt,
			Entries:   entries,
		}
	}
	return out
}

// syncToCloudHeader converts a noteHeader to cloud.NoteHeader.
func syncToCloudHeader(h noteHeader) cloud.NoteHeader {
	return cloud.NoteHeader{
		URN:        h.URN,
		Name:       h.Name,
		NoteType:   h.NoteType,
		ProjectURN: h.ProjectURN,
		FolderURN:  h.FolderURN,
		Deleted:    h.Deleted,
		CreatedAt:  h.CreatedAt,
		UpdatedAt:  h.UpdatedAt,
	}
}

// syncToCloudEvents converts []noteEvent to []cloud.NoteEvent.
func syncToCloudEvents(events []noteEvent) []cloud.NoteEvent {
	out := make([]cloud.NoteEvent, len(events))
	for i, e := range events {
		entries := make([]cloud.LineEntry, len(e.Entries))
		for j, ent := range e.Entries {
			entries[j] = cloud.LineEntry{
				Op:         ent.Op,
				LineNumber: ent.LineNumber,
				Content:    ent.Content,
			}
		}
		out[i] = cloud.NoteEvent{
			URN:       e.URN,
			Sequence:  e.Sequence,
			AuthorURN: e.AuthorURN,
			CreatedAt: e.CreatedAt,
			Entries:   entries,
		}
	}
	return out
}

// -----------------------------------------------------------------------------
// Small utilities
// -----------------------------------------------------------------------------

// syncParseTime parses an RFC3339 (or RFC3339Nano) timestamp; returns zero on error.
func syncParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, _ = time.Parse(time.RFC3339, s)
	}
	return t
}

// truncateName shortens s to at most n runes, appending "..." if trimmed.
func truncateName(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-3]) + "..."
}

// rebaseSeqRange returns a compact string like "3,4,5" for forkSeq+1..forkSeq+count.
func rebaseSeqRange(forkSeq, count int) string {
	if count == 0 {
		return "(none)"
	}
	parts := make([]string, count)
	for i := 0; i < count; i++ {
		parts[i] = fmt.Sprintf("%d", forkSeq+1+i)
	}
	return strings.Join(parts, ",")
}
