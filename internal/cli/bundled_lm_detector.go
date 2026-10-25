package cli

import (
	"errors"
	"log"
	"os"
	"path/filepath"

	"github.com/yetone/magpie/internal/plugin"
	"github.com/yetone/magpie/internal/pluginrt"
	"github.com/yetone/magpie/internal/settings"
)

// Called at startup after pluginrt is initialized. An old explicit Off or
// uninstall stays Off in the new built-in; an old On stays On even when
// the user selected the Essentials preset. Never erase an unreadable list.
func installBundledLMDetector() {
	marker := filepath.Join(settings.Dir(), "bundled-lm-detector.native-migrated")
	if _, err := os.Stat(marker); err == nil {
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Printf("LM Detector migration marker: %v", err)
		return
	}
	found, off, removed, err := plugin.LegacyLMDetectorPreference()
	if err != nil {
		log.Printf("LM Detector legacy preference: %v", err)
		return
	}
	if found || removed {
		if err := pluginrt.Set("lm-detector", found && !off); err != nil {
			log.Printf("LM Detector native preference: %v", err)
			return
		}
		if _, err := pluginrt.Refresh(); err != nil {
			log.Printf("LM Detector activation: %v", err)
			return
		}
	}
	if err := plugin.RetireLegacyLMDetector(); err != nil {
		log.Printf("LM Detector retirement: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
		log.Printf("LM Detector migration: %v", err)
		return
	}
	if err := os.WriteFile(marker, []byte("native-go\n"), 0600); err != nil {
		log.Printf("LM Detector migration: %v", err)
	}
}
