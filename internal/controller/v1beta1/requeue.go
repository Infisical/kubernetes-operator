package v1beta1

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/Infisical/infisical/k8-operator/internal/api"
)

// maxRateLimitJitter spreads out retries of resources that were rate limited together.
const maxRateLimitJitter = 1500 * time.Millisecond

// requeueOnRateLimit reports whether err is an Infisical rate-limit error and, if so,
// returns a result that retries after the server's Retry-After delay plus jitter.
func requeueOnRateLimit(logger logr.Logger, err error) (ctrl.Result, bool) {
	retryAfter, rateLimited := rateLimitDelay(err)
	if !rateLimited {
		return ctrl.Result{}, false
	}

	logger.Info(fmt.Sprintf("Rate limited, retrying after %v", retryAfter))
	return ctrl.Result{RequeueAfter: withRateLimitJitter(retryAfter)}, true
}

// rateLimitDelay reports whether err is an Infisical rate-limit error and, if so,
// returns the server's Retry-After delay.
func rateLimitDelay(err error) (time.Duration, bool) {
	var rateLimitErr *api.TooManyRequestsError
	if !errors.As(err, &rateLimitErr) {
		return 0, false
	}
	return time.Duration(rateLimitErr.RetryAfter) * time.Second, true
}

func withRateLimitJitter(delay time.Duration) time.Duration {
	return delay + time.Duration(rand.Int64N(int64(maxRateLimitJitter)))
}
