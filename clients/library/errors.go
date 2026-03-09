package sandlib

import (
	"context"
	"errors"
	"fmt"

	"github.com/AnishMulay/sandstore/clients/library/topology"
	"github.com/AnishMulay/sandstore/internal/communication"
)

const (
	codeNotLeader communication.SandCode = "NOT_LEADER"
	codeWrongNode communication.SandCode = "WRONG_NODE"
)

type SystemError struct {
	Class       topology.ErrorClass
	WrappedErr  error
	RoutingHint string
}

func (e *SystemError) Error() string { return e.WrappedErr.Error() }

type ErrorTranslator interface {
	Translate(resp *communication.Response, rawErr error) *SystemError
}

type DefaultErrorTranslator struct{}

func (t *DefaultErrorTranslator) Translate(resp *communication.Response, rawErr error) *SystemError {
	if rawErr != nil {
		if errors.Is(rawErr, context.DeadlineExceeded) || errors.Is(rawErr, context.Canceled) {
			return &SystemError{
				Class:      topology.ClassAmbiguousFailure,
				WrappedErr: rawErr,
			}
		}

		return &SystemError{
			Class:      topology.ClassTransportFailure,
			WrappedErr: rawErr,
		}
	}

	if resp == nil {
		return &SystemError{
			Class:      topology.ClassAmbiguousFailure,
			WrappedErr: fmt.Errorf("nil response with no error"),
		}
	}

	code := resp.Code
	body := string(resp.Body)

	switch code {
	case communication.CodeOK:
		return nil
	case communication.CodeNotFound, communication.CodeAlreadyExists:
		return &SystemError{
			Class:      topology.ClassSemanticError,
			WrappedErr: fmt.Errorf("semantic error: %s", body),
		}
	case codeNotLeader, codeWrongNode:
		return &SystemError{
			Class:       topology.ClassExplicitRejection,
			WrappedErr:  fmt.Errorf("explicit rejection: %s", body),
			RoutingHint: body,
		}
	default:
		return &SystemError{
			Class:      topology.ClassAmbiguousFailure,
			WrappedErr: fmt.Errorf("unknown server error (%s): %s", code, body),
		}
	}
}
