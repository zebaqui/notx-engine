package grpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zebaqui/notx-engine/internal/relay"
	"github.com/zebaqui/notx-engine/internal/repo"
	pb "github.com/zebaqui/notx-engine/internal/server/proto"
)

// ─────────────────────────────────────────────────────────────────────────────
// RelayServiceServer — gRPC implementation of RelayService
// ─────────────────────────────────────────────────────────────────────────────

// RelayServiceServer is the concrete gRPC implementation of RelayService.
// ALL execution logic lives here; the HTTP adapter is a thin translator.
type RelayServiceServer struct {
	pb.UnimplementedRelayServiceServer

	dev      repo.DeviceRepository
	executor *relay.Executor
	policy   relay.Policy
	log      *slog.Logger

	// eventEmitter is called after each successful execution to integrate with
	// the notx event system.  May be nil (no-op).
	eventEmitter func(evt RelayEvent)
}

// RelayEvent is the payload emitted to the notx event system after a relay
// execution completes.
type RelayEvent struct {
	EventType  string // "relay.executed"
	DeviceURN  string
	Method     string
	URL        string
	StatusCode int
	DurationMS int64
	StepID     string // non-empty for flow steps
	FlowLen    int    // total steps in flow, 0 for single Execute
}

// NewRelayServiceServer constructs a RelayServiceServer with the given
// dependencies.  eventEmitter may be nil.
func NewRelayServiceServer(
	devRepo repo.DeviceRepository,
	policy relay.Policy,
	log *slog.Logger,
	eventEmitter func(RelayEvent),
) *RelayServiceServer {
	return &RelayServiceServer{
		dev:          devRepo,
		executor:     relay.NewExecutor(policy),
		policy:       policy,
		log:          log,
		eventEmitter: eventEmitter,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Execute — single HTTP request
// ─────────────────────────────────────────────────────────────────────────────

func (s *RelayServiceServer) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	start := time.Now()

	// ── Validate device ───────────────────────────────────────────────────────
	if err := s.validateDevice(ctx, req.GetDeviceUrn()); err != nil {
		return nil, err
	}

	// ── Validate request presence ─────────────────────────────────────────────
	httpReq := req.GetRequest()
	if httpReq == nil {
		return nil, status.Error(codes.InvalidArgument, "request field is required")
	}

	// ── Build execution context and resolve variables ──────────────────────────
	ec := relay.NewExecutionContext(req.GetDeviceUrn(), req.GetVariables())

	spec, err := buildRequestSpec(httpReq, ec.Snapshot())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "build request: %v", err)
	}

	// ── Execute ───────────────────────────────────────────────────────────────
	result, execErr := s.executor.Execute(ctx, spec)
	elapsed := time.Since(start).Milliseconds()

	if execErr != nil {
		return nil, s.mapExecError(execErr, "", elapsed)
	}

	// ── Emit event ────────────────────────────────────────────────────────────
	s.emit(RelayEvent{
		EventType:  "relay.executed",
		DeviceURN:  req.GetDeviceUrn(),
		Method:     spec.Method,
		URL:        spec.URL,
		StatusCode: result.Status,
		DurationMS: result.DurationMS,
	})

	s.log.Info("relay.executed",
		"device_urn", req.GetDeviceUrn(),
		"method", spec.Method,
		"url", spec.URL,
		"status", result.Status,
		"duration_ms", result.DurationMS,
	)

	return &pb.ExecuteResponse{
		Response: resultToProto(result),
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// ExecuteFlow — multi-step pipeline
// ─────────────────────────────────────────────────────────────────────────────

func (s *RelayServiceServer) ExecuteFlow(ctx context.Context, req *pb.ExecuteFlowRequest) (*pb.ExecuteFlowResponse, error) {
	// ── Validate device ───────────────────────────────────────────────────────
	if err := s.validateDevice(ctx, req.GetDeviceUrn()); err != nil {
		return nil, err
	}

	steps := req.GetSteps()

	// ── Validate step count ───────────────────────────────────────────────────
	if len(steps) == 0 {
		return nil, status.Error(codes.InvalidArgument, "flow must contain at least one step")
	}
	if len(steps) > s.policy.MaxSteps {
		return nil, status.Errorf(codes.InvalidArgument,
			"flow contains %d steps, exceeding the limit of %d", len(steps), s.policy.MaxSteps)
	}

	// Validate that all step IDs are unique and non-empty.
	seen := make(map[string]bool, len(steps))
	for i, step := range steps {
		if step.GetId() == "" {
			return nil, status.Errorf(codes.InvalidArgument, "step %d has an empty id", i)
		}
		if seen[step.GetId()] {
			return nil, status.Errorf(codes.InvalidArgument, "duplicate step id %q", step.GetId())
		}
		seen[step.GetId()] = true
	}

	// ── Build shared execution context ────────────────────────────────────────
	ec := relay.NewExecutionContext(req.GetDeviceUrn(), req.GetVariables())

	results := make([]*pb.StepResult, 0, len(steps))

	// ── Execute steps sequentially ────────────────────────────────────────────
	for _, step := range steps {
		stepStart := time.Now()
		stepID := step.GetId()

		httpReq := step.GetRequest()
		if httpReq == nil {
			return nil, status.Errorf(codes.InvalidArgument, "step %q has no request", stepID)
		}

		// Interpolate variables for this step.
		spec, err := buildRequestSpec(httpReq, ec.Snapshot())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "step %q: build request: %v", stepID, err)
		}

		// Execute step.
		result, execErr := s.executor.Execute(ctx, spec)
		stepElapsed := time.Since(stepStart).Milliseconds()

		if execErr != nil {
			return nil, s.mapExecErrorFlow(execErr, stepID, stepElapsed)
		}

		// Apply extraction rules to update variables.
		if extractRules := step.GetExtract(); len(extractRules) > 0 {
			vars := ec.Snapshot()
			respData := &relay.ResponseData{
				Status:  result.Status,
				Headers: result.Headers,
				Body:    result.Body,
			}
			if extractErr := relay.ApplyExtractions(extractRules, respData, vars); extractErr != nil {
				s.log.Warn("relay flow: extraction error",
					"step_id", stepID,
					"error", extractErr,
				)
				// Non-fatal: log but continue; partial extraction is still applied.
			}
			ec.SetAll(vars)
		}

		results = append(results, &pb.StepResult{
			Id:       stepID,
			Response: resultToProto(result),
		})

		// Emit per-step event.
		s.emit(RelayEvent{
			EventType:  "relay.executed",
			DeviceURN:  req.GetDeviceUrn(),
			Method:     spec.Method,
			URL:        spec.URL,
			StatusCode: result.Status,
			DurationMS: result.DurationMS,
			StepID:     stepID,
			FlowLen:    len(steps),
		})

		s.log.Info("relay.flow.step",
			"device_urn", req.GetDeviceUrn(),
			"step_id", stepID,
			"method", spec.Method,
			"url", spec.URL,
			"status", result.Status,
			"duration_ms", stepElapsed,
		)
	}

	return &pb.ExecuteFlowResponse{
		Results:   results,
		Variables: ec.Snapshot(),
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Device validation
// ─────────────────────────────────────────────────────────────────────────────

// validateDevice checks that the device_urn refers to a registered,
// non-revoked device.  All validation runs inside the gRPC layer.
func (s *RelayServiceServer) validateDevice(ctx context.Context, deviceURN string) error {
	if strings.TrimSpace(deviceURN) == "" {
		return status.Error(codes.InvalidArgument, "device_urn is required")
	}

	dev, err := s.dev.GetDevice(ctx, deviceURN)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return status.Errorf(codes.PermissionDenied, "device %q is not registered", deviceURN)
		}
		s.log.Error("relay: device lookup failed", "device_urn", deviceURN, "err", err)
		return status.Errorf(codes.Internal, "device validation failed: %v", err)
	}

	if dev.Revoked {
		return status.Errorf(codes.PermissionDenied, "device %q has been revoked", deviceURN)
	}

	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Error mapping
// ─────────────────────────────────────────────────────────────────────────────

// mapExecError maps a relay executor error to a gRPC status error for single
// Execute calls.
func (s *RelayServiceServer) mapExecError(err error, stepID string, durationMS int64) error {
	return s.buildGRPCError(err, stepID, durationMS)
}

// mapExecErrorFlow maps a relay executor error to a gRPC status error for flow
// steps, attaching the step_id to the detail.
func (s *RelayServiceServer) mapExecErrorFlow(err error, stepID string, durationMS int64) error {
	return s.buildGRPCError(err, stepID, durationMS)
}

func (s *RelayServiceServer) buildGRPCError(err error, stepID string, durationMS int64) error {
	var policyErr *relay.PolicyViolationError
	var timeoutErr *relay.TimeoutError

	switch {
	case errors.As(err, &policyErr):
		msg := fmt.Sprintf("outbound request blocked: %v", policyErr.Cause)
		if stepID != "" {
			msg = fmt.Sprintf("step %q: %s", stepID, msg)
		}
		return status.Errorf(codes.PermissionDenied, "%s", msg)

	case errors.As(err, &timeoutErr):
		msg := fmt.Sprintf("request timed out after %dms", durationMS)
		if stepID != "" {
			msg = fmt.Sprintf("step %q: %s", stepID, msg)
		}
		return status.Errorf(codes.DeadlineExceeded, "%s", msg)

	default:
		msg := fmt.Sprintf("execution failed: %v", err)
		if stepID != "" {
			msg = fmt.Sprintf("step %q: %s", stepID, msg)
		}
		if stepID != "" {
			return status.Errorf(codes.Aborted, "%s", msg)
		}
		return status.Errorf(codes.Internal, "%s", msg)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// buildRequestSpec applies variable interpolation to an HttpRequest and
// returns a relay.RequestSpec ready for the executor.
func buildRequestSpec(r *pb.HttpRequest, vars map[string]string) (relay.RequestSpec, error) {
	if r.GetMethod() == "" {
		return relay.RequestSpec{}, fmt.Errorf("method is required")
	}
	if r.GetUrl() == "" {
		return relay.RequestSpec{}, fmt.Errorf("url is required")
	}

	// Interpolate URL and headers.
	resolvedURL := relay.Interpolate(r.GetUrl(), vars)
	resolvedHeaders := make(map[string]string, len(r.GetHeaders()))
	for k, v := range r.GetHeaders() {
		resolvedHeaders[k] = relay.Interpolate(v, vars)
	}

	// Interpolate body (treated as UTF-8 text).
	resolvedBody := relay.InterpolateBytes(r.GetBody(), vars)

	return relay.RequestSpec{
		Method:  r.GetMethod(),
		URL:     resolvedURL,
		Headers: resolvedHeaders,
		Body:    resolvedBody,
	}, nil
}

// resultToProto converts a relay.ResponseResult to the proto HttpResponse.
func resultToProto(r *relay.ResponseResult) *pb.HttpResponse {
	return &pb.HttpResponse{
		Status:     int32(r.Status),
		Headers:    r.Headers,
		Body:       r.Body,
		DurationMs: r.DurationMS,
	}
}

// emit calls the eventEmitter if set, swallowing any panic to avoid breaking
// the execution path on event system errors.
func (s *RelayServiceServer) emit(evt RelayEvent) {
	if s.eventEmitter == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("relay: event emitter panicked", "panic", r)
		}
	}()
	s.eventEmitter(evt)
}
