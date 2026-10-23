package gateway

import (
	"bytes"
	"context"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestPreparedCodexHookOnlyOnSubscription(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","instructions":"unchanged"}`)
	for _, p := range []provider.Provider{
		{ID: "openai"},
		{ID: "plugin", Account: &provider.Account{Agent: "plugin"}},
		{ID: "codex", Account: &provider.Account{Agent: "codex"}},
	} {
		got := preparedCodexHook(context.Background(), p, provider.Chat, body)
		if !bytes.Equal(got, body) {
			t.Fatalf("%s chat request was modified: %s", p.ID, got)
		}
	}
}
