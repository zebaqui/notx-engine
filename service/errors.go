package service

import "errors"

// ErrInvalidInput is returned by service methods when the supplied arguments
// fail validation (e.g. a required field is missing or a URN is malformed).
// Transport layers should map this to their "bad request" response type
// (HTTP 400 / gRPC codes.InvalidArgument).
var ErrInvalidInput = errors.New("invalid input")
