package cli

import (
    "log"

    "github.com/yetone/magpie/internal/plugin"
)

// The fork-specific bootstrap stays out of upstream's plugin host and CLI
// implementation. Startup remains usable when the optional bundle fails.
func installBundledLMDetector() {
    if err := plugin.EnsureBundledLMDetector(); err != nil {
        log.Printf("bundled LM Detector: %v", err)
    }
}
