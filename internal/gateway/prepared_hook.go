package gateway

import (
	"context"

	"github.com/tidwall/gjson"
	"github.com/yetone/magpie/internal/plugin"
	"github.com/yetone/magpie/internal/provider"
)

// preparedCodexHook runs only for the built-in Codex subscription, not the
// separate native Codex backend passthrough or other subscription plugins.
func preparedCodexHook(ctx context.Context, p provider.Provider, to provider.Protocol, body []byte) []byte {
	if p.Account == nil || p.Account.Agent != "codex" || to != provider.Responses {
		return body
	}
	return plugin.GatewayPrepared(ctx, body, plugin.PreparedContext{
		Provider: p.ID,
		Protocol: string(to),
		Model:    gjson.GetBytes(body, "model").String(),
	})
}
