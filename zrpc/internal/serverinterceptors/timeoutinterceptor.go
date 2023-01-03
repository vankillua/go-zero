package serverinterceptors

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var methodTimeout sync.Map

// SetTimeoutForFullMethod set the specified timeout for given method.
func SetTimeoutForFullMethod(fullMethod string, timeout time.Duration) {
	methodTimeout.Store(fullMethod, timeout)
}

// UnaryTimeoutInterceptor returns a func that sets timeout to incoming unary requests.
func UnaryTimeoutInterceptor(timeout time.Duration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		timeout = getTimeoutByUnaryServerInfo(info, timeout)
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		var resp any
		var err error
		var lock sync.Mutex
		done := make(chan struct{})
		// create channel with buffer size 1 to avoid goroutine leak
		panicChan := make(chan any, 1)
		go func() {
			defer func() {
				if p := recover(); p != nil {
					// attach call stack to avoid missing in different goroutine
					panicChan <- fmt.Sprintf("%+v\n\n%s", p, strings.TrimSpace(string(debug.Stack())))
				}
			}()

			lock.Lock()
			defer lock.Unlock()
			resp, err = handler(ctx, req)
			close(done)
		}()

		select {
		case p := <-panicChan:
			panic(p)
		case <-done:
			lock.Lock()
			defer lock.Unlock()
			return resp, err
		case <-ctx.Done():
			err := ctx.Err()
			if errors.Is(err, context.Canceled) {
				err = status.Error(codes.Canceled, err.Error())
			} else if errors.Is(err, context.DeadlineExceeded) {
				err = status.Error(codes.DeadlineExceeded, err.Error())
			}
			return nil, err
		}
	}
}

func getTimeoutByUnaryServerInfo(info *grpc.UnaryServerInfo, defaultTimeout time.Duration) time.Duration {
	if ts, ok := info.Server.(TimeoutStrategy); ok {
		return ts.GetTimeoutByFullMethod(info.FullMethod, defaultTimeout)
	} else if v, ok := methodTimeout.Load(info.FullMethod); ok {
		if t, ok := v.(time.Duration); ok {
			return t
		}
	}

	return defaultTimeout
}

type TimeoutStrategy interface {
	GetTimeoutByFullMethod(fullMethod string, defaultTimeout time.Duration) time.Duration
}
