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
	var rateLimitErr *api.TooManyRequestsError
	if !errors.As(err, &rateLimitErr) {
		return ctrl.Result{}, false
	}

	retryAfter := time.Duration(rateLimitErr.RetryAfter) * time.Second
	jitter := time.Duration(rand.Int64N(int64(maxRateLimitJitter)))

	logger.Info(fmt.Sprintf("Rate limited, retrying after %v", retryAfter))
	return ctrl.Result{RequeueAfter: retryAfter + jitter}, true
}
