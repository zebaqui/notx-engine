package grpc

import (
	"io"
	"log/slog"

	pb "github.com/zebaqui/notx-engine/proto"
)

// SyncServer implements pb.SyncServiceServer.
// It is registered on the primary mTLS gRPC listener so that paired local
// engines can sync notes bidirectionally over a certificate-authenticated channel.
type SyncServer struct {
	pb.UnimplementedSyncServiceServer
	noteS *NoteServer
	log   *slog.Logger
}

// NewSyncServer returns a ready-to-register SyncServer backed by the given NoteServer.
func NewSyncServer(noteS *NoteServer, log *slog.Logger) *SyncServer {
	return &SyncServer{noteS: noteS, log: log}
}

// SyncNotes is a bidirectional streaming RPC.
//
// Phase 1 — inbound push: the client streams SyncNoteMessage frames, one per
// local note.  For each frame the server calls ReceiveSharedNote and sends
// back a SyncNoteResult (EventsStored >= 0).  Per-note errors are reported
// inside the result frame (non-fatal); stream-level errors abort the RPC.
//
// Phase 2 — cloud catalogue: after the client closes its send side (EOF), the
// server lists all notes it holds and sends a SyncNoteResult per note with
// EventsStored = -1.  The -1 sentinel tells the client "this note exists on
// the cloud; pull it if you don't have it."
func (s *SyncServer) SyncNotes(stream pb.SyncService_SyncNotesServer) error {
	ctx := stream.Context()

	// ── Phase 1: receive notes pushed by the client ──────────────────────────
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.log.Error("sync: recv error", "err", err)
			return err
		}

		resp, recvErr := s.noteS.ReceiveSharedNote(ctx, &pb.ReceiveSharedNoteRequest{
			Header: msg.Header,
			Events: msg.Events,
		})

		var result *pb.SyncNoteResult
		if recvErr != nil {
			s.log.Warn("sync: receive_shared_note failed",
				"urn", msg.GetHeader().GetUrn(),
				"err", recvErr,
			)
			result = &pb.SyncNoteResult{
				NoteUrn: msg.GetHeader().GetUrn(),
				Error:   recvErr.Error(),
			}
		} else {
			result = &pb.SyncNoteResult{
				NoteUrn:      resp.NoteUrn,
				EventsStored: resp.EventsStored,
			}
		}

		if sendErr := stream.Send(result); sendErr != nil {
			s.log.Error("sync: send result error", "err", sendErr)
			return sendErr
		}
	}

	// ── Phase 2: stream cloud note catalogue back to the client ──────────────
	pageToken := ""
	for {
		listResp, err := s.noteS.ListNotes(ctx, &pb.ListNotesRequest{
			PageSize:       500,
			PageToken:      pageToken,
			IncludeDeleted: true,
		})
		if err != nil {
			s.log.Error("sync: list notes error", "err", err)
			return err
		}

		for _, header := range listResp.Notes {
			sentinel := &pb.SyncNoteResult{
				NoteUrn:      header.Urn,
				EventsStored: -1,
			}
			if sendErr := stream.Send(sentinel); sendErr != nil {
				s.log.Error("sync: send catalogue sentinel error", "err", sendErr)
				return sendErr
			}
		}

		if listResp.NextPageToken == "" {
			break
		}
		pageToken = listResp.NextPageToken
	}

	return nil
}
