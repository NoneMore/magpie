// Package fingerprint registers the built-in model fingerprint detector.
// All UI lives in assets/plugins/fingerprint and disappears when switched off.
package fingerprint

import (
	"github.com/yetone/magpie/internal/fingerprint"
	"github.com/yetone/magpie/internal/pluginrt"
)

func init() {
	pluginrt.Register(pluginrt.Manifest{
		ID: "fingerprint", API: "^1",
		Requires: []string{"window", "gateway"},
		Name:     "Model fingerprint",
		Sub:      "Identify the closest reference models using three Magpie Gateway challenges",
		Parts:    []pluginrt.Part{{Slot: "nav.page", Key: "fingerprint"}},
		Client:   []string{"i18n.js", "fingerprint.css", "client.js"},
		Apply: func(ctx *pluginrt.Ctx) error {
			ctx.OnDispose(fingerprint.StopAll)
			return nil
		},
	})
}
