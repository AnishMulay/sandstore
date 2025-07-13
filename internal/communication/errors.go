package communication

import "errors"

var (
	// Server startup/shutdown errors
	ErrServerStartFailed = errors.New("failed to start server")
	ErrServerStopFailed  = errors.New("failed to stop server")

	// Client connection errors
	ErrClientCreateFailed = errors.New("failed to create client")
	ErrConnectionFailed   = errors.New("failed to connect to server")

	// Message handling errors
	ErrHandlerNotSet        = errors.New("message handler not set")
	ErrMessageSendFailed    = errors.New("failed to send message")
	ErrMessageHandlerFailed = errors.New("message handler failed")

	// Serialization/deserialization errors
	ErrPayloadMarshalFailed   = errors.New("failed to marshal payload")
	ErrPayloadUnmarshalFailed = errors.New("failed to unmarshal payload")
	ErrMessageMarshalFailed   = errors.New("failed to marshal message")

	// HTTP specific errors
	ErrHTTPRequestCreateFailed = errors.New("failed to create HTTP request")
	ErrHTTPRequestSendFailed   = errors.New("failed to send HTTP request")
	ErrHTTPResponseReadFailed  = errors.New("failed to read HTTP response")
	ErrHTTPBodyReadFailed      = errors.New("failed to read HTTP request body")
	ErrInvalidJSON             = errors.New("invalid JSON in request")
	ErrMissingRequiredFields   = errors.New("missing required fields in request")

	// GRPC specific errors
	ErrGRPCListenFailed = errors.New("failed to listen on address")
)