package http

import (
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	grpcsvc "github.com/zebaqui/notx-engine/internal/server/grpc"
	pb "github.com/zebaqui/notx-engine/proto"
)

// ─────────────────────────────────────────────────────────────────────────────
// Relay HTTP adapter
//
// Responsibilities of this layer (ONLY):
//  1. Decode the JSON request body.
//  2. Extract X-Device-ID from the request header.
//  3. Map the JSON payload to the appropriate gRPC request message.
//  4. Call the RelayService gRPC method.
//  5. Translate the gRPC response (or error) back to JSON.
//
// NO business logic, validation, or execution lives here.
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// JSON request/response shapes for POST /v1/relay/execute
// ─────────────────────────────────────────────────────────────────────────────

type relayExecuteRequest struct {
	// Request is the outbound HTTP request to execute.
	Request relayHTTPRequestJSON `json:"request"`
	// Variables are the initial {{var}} bindings for interpolation.
	Variables map[string]string `json:"variables,omitempty"`
}

type relayHTTPRequestJSON struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"` // base64 or plain text
}

type relayExecuteResponse struct {
	Response relayHTTPResponseJSON `json:"response"`
}

type relayHTTPResponseJSON struct {
	Status     int32             `json:"status"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	DurationMS int64             `json:"duration_ms"`
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON request/response shapes for POST /v1/relay/execute-flow
// ─────────────────────────────────────────────────────────────────────────────

type relayExecuteFlowRequest struct {
	Steps     []relayStepJSON   `json:"steps"`
	Variables map[string]string `json:"variables,omitempty"`
}

type relayStepJSON struct {
	ID      string               `json:"id"`
	Request relayHTTPRequestJSON `json:"request"`
	Extract map[string]string    `json:"extract,omitempty"`
}

type relayExecuteFlowResponse struct {
	Results   []relayStepResultJSON `json:"results"`
	Variables map[string]string     `json:"variables,omitempty"`
}

type relayStepResultJSON struct {
	ID       string                `json:"id"`
	Response relayHTTPResponseJSON `json:"response"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Route registration (called from Handler.routes)
// ─────────────────────────────────────────────────────────────────────────────

// routeRelay registers the relay adapter endpoints onto the mux.
// It is called from Handler.routes() and requires a non-nil relay service.
func (h *Handler) routeRelay(relaySvc *grpcsvc.RelayServer) {
	h.mux.HandleFunc("/v1/relay/execute",
		h.withDeviceAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
			h.handleRelayExecute(w, r, relaySvc)
		}))

	h.mux.HandleFunc("/v1/relay/execute-flow",
		h.withDeviceAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
			h.handleRelayExecuteFlow(w, r, relaySvc)
		}))
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /v1/relay/execute
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleRelayExecute(w http.ResponseWriter, r *http.Request, svc *grpcsvc.RelayServer) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// 1. Decode JSON body.
	var body relayExecuteRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// 2. Extract device identity from middleware-injected context.
	dev := DeviceFromCtx(r.Context())
	if dev == nil {
		writeError(w, http.StatusUnauthorized, "device not identified")
		return
	}

	// 3. Map to gRPC request.
	grpcReq := &pb.ExecuteRequest{
		DeviceUrn: dev.URN.String(),
		Request:   jsonToProtoHTTPRequest(body.Request),
		Variables: body.Variables,
	}

	// 4. Call gRPC service.
	resp, err := svc.Execute(r.Context(), grpcReq)
	if err != nil {
		writeError(w, grpcStatusToHTTP(err), grpcErrMessage(err))
		return
	}

	// 5. Translate gRPC response to JSON.
	writeJSON(w, http.StatusOK, relayExecuteResponse{
		Response: protoToJSONHTTPResponse(resp.GetResponse()),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /v1/relay/execute-flow
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleRelayExecuteFlow(w http.ResponseWriter, r *http.Request, svc *grpcsvc.RelayServer) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// 1. Decode JSON body.
	var body relayExecuteFlowRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// 2. Extract device identity.
	dev := DeviceFromCtx(r.Context())
	if dev == nil {
		writeError(w, http.StatusUnauthorized, "device not identified")
		return
	}

	// 3. Map to gRPC request.
	steps := make([]*pb.Step, 0, len(body.Steps))
	for _, s := range body.Steps {
		steps = append(steps, &pb.Step{
			Id:      s.ID,
			Request: jsonToProtoHTTPRequest(s.Request),
			Extract: s.Extract,
		})
	}

	grpcReq := &pb.ExecuteFlowRequest{
		DeviceUrn: dev.URN.String(),
		Steps:     steps,
		Variables: body.Variables,
	}

	// 4. Call gRPC service.
	resp, err := svc.ExecuteFlow(r.Context(), grpcReq)
	if err != nil {
		writeError(w, grpcStatusToHTTP(err), grpcErrMessage(err))
		return
	}

	// 5. Translate gRPC response to JSON.
	results := make([]relayStepResultJSON, 0, len(resp.GetResults()))
	for _, sr := range resp.GetResults() {
		results = append(results, relayStepResultJSON{
			ID:       sr.GetId(),
			Response: protoToJSONHTTPResponse(sr.GetResponse()),
		})
	}

	writeJSON(w, http.StatusOK, relayExecuteFlowResponse{
		Results:   results,
		Variables: resp.GetVariables(),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Proto <-> JSON conversion helpers
// ─────────────────────────────────────────────────────────────────────────────

func jsonToProtoHTTPRequest(j relayHTTPRequestJSON) *pb.HttpRequest {
	return &pb.HttpRequest{
		Method:  j.Method,
		Url:     j.URL,
		Headers: j.Headers,
		Body:    []byte(j.Body),
	}
}

func protoToJSONHTTPResponse(r *pb.HttpResponse) relayHTTPResponseJSON {
	if r == nil {
		return relayHTTPResponseJSON{}
	}
	return relayHTTPResponseJSON{
		Status:     r.GetStatus(),
		Headers:    r.GetHeaders(),
		Body:       string(r.GetBody()),
		DurationMS: r.GetDurationMs(),
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// gRPC error -> HTTP status helpers
// ─────────────────────────────────────────────────────────────────────────────

// grpcStatusToHTTP maps a gRPC error's status code to the appropriate HTTP
// status code using the google.golang.org/grpc/status and codes packages.
func grpcStatusToHTTP(err error) int {
	if err == nil {
		return http.StatusOK
	}
	st, ok := status.FromError(err)
	if !ok {
		return http.StatusInternalServerError
	}
	switch st.Code() {
	case codes.InvalidArgument:
		return http.StatusBadRequest
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.NotFound:
		return http.StatusNotFound
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	case codes.Aborted:
		return http.StatusBadGateway
	case codes.Unimplemented:
		return http.StatusNotImplemented
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.AlreadyExists:
		return http.StatusConflict
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// grpcErrMessage extracts the human-readable description from a gRPC status
// error. Falls back to err.Error() for non-status errors.
func grpcErrMessage(err error) string {
	if err == nil {
		return ""
	}
	if st, ok := status.FromError(err); ok {
		return st.Message()
	}
	return err.Error()
}
