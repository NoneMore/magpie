package cli

import (
    "log"

    detector "github.com/yetone/magpie/plugins/lm-detector"
)

// The fork-specific bootstrap stays out of upstream's plugin host and CLI
// implementation. Startup remains usable when the optional bundle fails.
func installBundledLMDetector() {
    if err := detector.Ensure(); err != nil {
        log.Printf("bundled LM Detector: %v", err)
    }
}
