// Package detector registers the native LM Detector with Magpie's built-in
// plugin runtime. The scoring engine is upstream lmfpd's Go SDK.
package detector

import (
	"github.com/yetone/magpie/internal/lmdetector"
	"github.com/yetone/magpie/internal/pluginrt"
)

func init() {
	pluginrt.Register(pluginrt.Manifest{
		ID: "lm-detector", API: "^1", Requires: []string{"gateway", "window"}, Essential: true,
		Name: "Model fingerprint", Sub: "Identify a model from its number distribution using the upstream Go SDK",
		Parts:  []pluginrt.Part{{Slot: "nav.page", Key: "lm-detector"}},
		Client: []string{"i18n.js", "client.js", "detector.css"},
		Apply: func(c *pluginrt.Ctx) error {
			lmdetector.Enable()
			c.OnDispose(lmdetector.Disable)
			return nil
		},
	})
}
