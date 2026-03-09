package topology

import "context"

type ErrorClass string

const (
	ClassExplicitRejection ErrorClass = "EXPLICIT_REJECTION"
	ClassAmbiguousFailure  ErrorClass = "AMBIGUOUS_FAILURE"
	ClassTransportFailure  ErrorClass = "TRANSPORT_FAILURE"
	ClassSemanticError     ErrorClass = "SEMANTIC_ERROR"
)

// MsgTopologyRequest is JSON encoded into communication.MessageRequest.Payload.
type MsgTopologyRequest struct {
	TopologyType string `json:"topology_type"`
	TopologyData []byte `json:"topology_data,omitempty"`
}

// MsgTopologyResponse is JSON encoded into communication.MessageResponse.Body.
type MsgTopologyResponse struct {
	TopologyData []byte `json:"topology_data"`
}

type Router interface {
	GetRoute(isMutation bool) (address string, err error)
	Invalidate(failedAddr string)
	SetRouteHint(hint string)
	Refresh(ctx context.Context) error
}
