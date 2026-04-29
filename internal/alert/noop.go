package alert

import "context"

// NoopService is a no-op alert service for when alerts are disabled.
type NoopService struct{}

// NewNoopService creates a new no-op alert service.
func NewNoopService() *NoopService {
	return &NoopService{}
}

// Send does nothing and returns nil.
func (s *NoopService) Send(_ context.Context, _ AlertEvent) error {
	return nil
}
