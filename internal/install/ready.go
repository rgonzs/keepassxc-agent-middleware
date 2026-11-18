package install

import (
	"context"
	"fmt"
	"time"

	"keepassxc-agent-middleware/internal/transport"
)

func cleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}
func (m SystemManager) Start(ctx context.Context, p *Plan) error {
	// A reachable endpoint before activation belongs to another process. Do not
	// mistake its liveness for a successful start of this managed service.
	probeCtx, probeCancel := context.WithTimeout(ctx, 250*time.Millisecond)
	existing, probeErr := transport.Dial(probeCtx, p.Config.Listen)
	probeCancel()
	if probeErr == nil {
		existing.Close()
		return fmt.Errorf("proxy endpoint is already active; stop the other proxy before installing: %s", p.Config.Listen)
	}
	if err := m.start(ctx, p); err != nil {
		return err
	}
	if err := waitEndpoint(ctx, p.Config.Listen, true); err != nil {
		return err
	}
	return verifyStartedProxy(ctx, p)
}
