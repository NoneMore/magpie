package cli

import (
	"log"

	"github.com/yetone/magpie/internal/plugin"
)

// The native built-in has its own pluginrt state. Migration is intentionally
// breaking: only remove registrations of the old Bun plugin; preserve every
// other plugin, its options and preferred routing.
func installBundledLMDetector() {
	if err := plugin.RetireLegacyLMDetector(); err != nil {
		log.Printf("LM Detector legacy registration cleanup: %v", err)
	}
}
