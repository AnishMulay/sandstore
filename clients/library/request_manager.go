package sandlib

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/AnishMulay/sandstore/clients/library/topology"
	"github.com/AnishMulay/sandstore/internal/communication"
)

type RequestManager interface {
	ExecuteIdempotent(ctx context.Context, msgType string, payload any) (*communication.Response, error)
	ExecuteMutation(ctx context.Context, msgType string, payload any) (*communication.Response, error)
}

type StandardRequestManager struct {
	router     topology.Router
	comm       communication.Communicator
	translator ErrorTranslator
}

func NewStandardRequestManager(r topology.Router, c communication.Communicator, t ErrorTranslator) *StandardRequestManager {
	return &StandardRequestManager{
		router:     r,
		comm:       c,
		translator: t,
	}
}

func calculateJitter(attempt int) time.Duration {
	base := 100 * time.Millisecond
	maxWait := 1000 * time.Millisecond

	wait := base * time.Duration(1<<attempt)
	if wait > maxWait {
		wait = maxWait
	}

	jitter := time.Duration(rand.Intn(50)) * time.Millisecond
	return wait + jitter
}

func (rm *StandardRequestManager) ExecuteMutation(ctx context.Context, msgType string, payload any) (*communication.Response, error) {
	const maxRetries = 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		targetAddr, err := rm.router.GetRoute(true)
		if err != nil {
			return nil, err
		}

		reqMsg := communication.Message{
			From:    "sandlib",
			Type:    msgType,
			Payload: payload,
		}
		rawResp, rawErr := rm.comm.Send(ctx, targetAddr, reqMsg)

		sysErr := rm.translator.Translate(rawResp, rawErr)
		if sysErr == nil {
			return rawResp, nil
		}

		switch sysErr.Class {
		case topology.ClassSemanticError:
			return nil, sysErr.WrappedErr

		case topology.ClassAmbiguousFailure, topology.ClassTransportFailure:
			rm.router.Invalidate(targetAddr)
			return nil, sysErr.WrappedErr

		case topology.ClassExplicitRejection:
			if sysErr.RoutingHint != "" {
				rm.router.SetRouteHint(sysErr.RoutingHint)
			} else {
				_ = rm.router.Refresh(ctx)
			}

			lastErr = sysErr.WrappedErr
			time.Sleep(calculateJitter(attempt))
			continue

		default:
			return nil, sysErr.WrappedErr
		}
	}

	return nil, fmt.Errorf("mutation failed after %d attempts. last error: %w", maxRetries, lastErr)
}

func (rm *StandardRequestManager) ExecuteIdempotent(ctx context.Context, msgType string, payload any) (*communication.Response, error) {
	const maxRetries = 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		targetAddr, err := rm.router.GetRoute(false)
		if err != nil {
			return nil, err
		}

		reqMsg := communication.Message{
			From:    "sandlib",
			Type:    msgType,
			Payload: payload,
		}
		rawResp, rawErr := rm.comm.Send(ctx, targetAddr, reqMsg)

		sysErr := rm.translator.Translate(rawResp, rawErr)
		if sysErr == nil {
			return rawResp, nil
		}

		switch sysErr.Class {
		case topology.ClassSemanticError:
			return nil, sysErr.WrappedErr

		case topology.ClassAmbiguousFailure:
			return nil, sysErr.WrappedErr

		case topology.ClassExplicitRejection, topology.ClassTransportFailure:
			rm.router.Invalidate(targetAddr)

			if sysErr.RoutingHint != "" {
				rm.router.SetRouteHint(sysErr.RoutingHint)
			} else {
				_ = rm.router.Refresh(ctx)
			}

			lastErr = sysErr.WrappedErr
			time.Sleep(calculateJitter(attempt))
			continue

		default:
			return nil, sysErr.WrappedErr
		}
	}

	return nil, fmt.Errorf("idempotent op failed after %d attempts. last error: %w", maxRetries, lastErr)
}
