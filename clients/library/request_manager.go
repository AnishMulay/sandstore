package sandlib

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/AnishMulay/sandstore/clients/library/topology"
	"github.com/AnishMulay/sandstore/internal/communication"
)

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

func waitForRetryBackoff(ctx context.Context, attempt int) error {
	select {
	case <-time.After(calculateJitter(attempt)):
		return nil
	case <-ctx.Done():
		return fmt.Errorf("request cancelled during retry backoff: %w", ctx.Err())
	}
}

func (rm *StandardRequestManager) getRoute(ctx context.Context, isMutation bool) (string, error) {
	targetAddr, err := rm.router.GetRoute(isMutation)
	if err == nil {
		return targetAddr, nil
	}
	if refreshErr := rm.router.Refresh(ctx); refreshErr != nil {
		return "", err
	}
	return rm.router.GetRoute(isMutation)
}

func (rm *StandardRequestManager) ExecuteMutation(ctx context.Context, msgType string, payload any) (*communication.Response, error) {
	const maxRetries = 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		targetAddr, err := rm.getRoute(ctx, true)
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
			return rawResp, nil

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
			if err := waitForRetryBackoff(ctx, attempt); err != nil {
				return nil, err
			}
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
		targetAddr, err := rm.getRoute(ctx, false)
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
			return rawResp, nil

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
			if err := waitForRetryBackoff(ctx, attempt); err != nil {
				return nil, err
			}
			continue

		default:
			return nil, sysErr.WrappedErr
		}
	}

	return nil, fmt.Errorf("idempotent op failed after %d attempts. last error: %w", maxRetries, lastErr)
}
