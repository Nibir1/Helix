// internal/providers/health.go
// Purpose: Provider health helper.
package providers

import (
	"context"
	"time"
)

// CheckHealth runs a provider health check with a bounded timeout.
func CheckHealth(ctx context.Context, p AIProvider) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	return p.HealthCheck(ctx)
}
