package plugin

import (
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "crypto/sha256"
    "embed"
    "encoding/hex"
    "io/fs"
    "strings"

    "github.com/yetone/magpie/internal/settings"

)

// The build step populates bundled-lm-detector/runtime from the checked-in
// JavaScript plugin and the pinned lmfpd reference package. End-user
// executables carry both and do not run npm or access the npm registry.
//go:embed bundled-lm-detector
var lmDetectorFiles embed.FS

// EnsureBundledLMDetector is the fork's startup hook, called by CLI after
// settings migration. It does not register a Go plugin or fork the Bun host.
func EnsureBundledLMDetector() error {
    for _, name := range []string{"data/unified_bank.json", "data/shared_detector.bin"} {
        b, err := lmDetectorFiles.ReadFile("bundled-lm-detector/runtime/" + name)
        if err != nil || len(b) < 1<<20 {
            return fmt.Errorf("LM Detector was built without %s; run node .github/scripts/prepare-lm-detector.mjs before go build", name)
        }
    }
    payload, err := fs.Sub(lmDetectorFiles, "bundled-lm-detector/runtime")
    if err != nil { return err }
    root := filepath.Join(settings.Dir(), "bundled", "lm-detector")
    target, err := materializeLMDetector(payload, root)
    if err != nil { return err }
    return EnsureBundled(target, "magpie-lm-detector", filepath.Join(root, ".registered"))
}

// Immutable, content-addressed copies keep old Bun hosts running safely during
// upgrades. The final directory is always named lm-detector so Magpie keeps
// its stable plugin short name, Page identity and tool names.
func materializeLMDetector(files fs.FS, root string) (string, error) {
    h := sha256.New()
    if err := fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
        if err != nil { return err }
        if entry.IsDir() { return nil }
        b, err := fs.ReadFile(files, name)
        if err != nil { return err }
        h.Write([]byte(name))
        h.Write([]byte{0})
        h.Write(b)
        return nil
    }); err != nil { return "", err }
    version := hex.EncodeToString(h.Sum(nil))[:20]
    target := filepath.Join(root, version, "lm-detector")
    if _, err := os.Stat(filepath.Join(target, ".complete")); err == nil {
        return target, nil
    } else if !os.IsNotExist(err) {
        return "", err
    }
    if err := os.MkdirAll(root, 0o700); err != nil { return "", err }
    stage, err := os.MkdirTemp(root, ".staging-")
    if err != nil { return "", err }
    defer os.RemoveAll(stage)
    stagePlugin := filepath.Join(stage, "lm-detector")
    if err := os.MkdirAll(stagePlugin, 0o700); err != nil { return "", err }
    if err := fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
        if err != nil { return err }
        if name == "." { return nil }
        if entry.Type() & os.ModeSymlink != 0 || strings.Contains(name, "..") {
            return fmt.Errorf("invalid embedded LM Detector path: %s", name)
        }
        dest := filepath.Join(stagePlugin, filepath.FromSlash(name))
        if entry.IsDir() { return os.MkdirAll(dest, 0o700) }
        b, err := fs.ReadFile(files, name)
        if err != nil { return err }
        return os.WriteFile(dest, b, 0o600)
    }); err != nil { return "", err }
    if err := os.WriteFile(filepath.Join(stagePlugin, ".complete"), []byte(version+"\n"), 0o600); err != nil {
        return "", err
    }
    if err := os.Rename(stage, filepath.Dir(target)); err != nil {
        if _, statErr := os.Stat(filepath.Join(target, ".complete")); statErr == nil {
            return target, nil
        }
        return "", fmt.Errorf("publish bundled LM Detector: %w", err)
    }
    return target, nil
}

// EnsureBundled installs a fork-shipped local plugin exactly once, then follows
// binary upgrades to new immutable paths. Existing Off/Options/Prefer survive
// migration from a manually installed copy. If the user removes the plugin,
// the persistent marker prevents it from reappearing on the next startup.
func EnsureBundled(spec, packageName, marker string) error {
    if !filepath.IsAbs(spec) { return fmt.Errorf("bundled plugin path is not absolute: %q", spec) }
    if err := notPlugin(spec); err != nil { return fmt.Errorf("bundled plugin: %w", err) }
    listMu.Lock()
    defer listMu.Unlock()

    // Fail closed instead of discarding an unreadable plugins.json. Load
    // deliberately tolerates malformed files for legacy callers.
    if raw, err := os.ReadFile(listPath()); err == nil {
        var test List
        if err := json.Unmarshal(raw, &test); err != nil {
            return fmt.Errorf("cannot register bundled plugin with invalid plugins.json: %w", err)
        }
    } else if !errors.Is(err, os.ErrNotExist) {
        return err
    }

    l := Load()
    found := -1
    for i, e := range l.Plugins {
        if e.Spec == spec || bundledPackage(e, packageName) {
            found = i
            break
        }
    }
    if found < 0 {
        if _, err := os.Stat(marker); err == nil {
            return nil // removed by user: don't resurrect it
        } else if !errors.Is(err, os.ErrNotExist) {
            return err
        }
        l.Plugins = append(l.Plugins, Entry{Spec: spec})
    } else if l.Plugins[found].Spec != spec {
        old := l.Plugins[found].Spec
        l.Plugins[found].Spec = spec
        for k, v := range l.Prefer {
            if v == old { l.Prefer[k] = spec }
        }
    } else {
        return markBundled(marker) // already registered, preserve user state
    }

    if err := save(l); err != nil { return err }
    if err := markBundled(marker); err != nil { return err }
    Restart()
    return nil
}

// Only migrate a manually added plugin with the exact expected package name;
// never appropriate an unrelated user's local JavaScript project.
func bundledPackage(e Entry, name string) bool {
    if Name(e.Spec) == name { return true }
    b, err := os.ReadFile(filepath.Join(Target(e.Spec), "package.json"))
    if err != nil { return false }
    var pkg struct { Name string }
    return json.Unmarshal(b, &pkg) == nil && pkg.Name == name
}

func markBundled(marker string) error {
    if _, err := os.Stat(marker); err == nil { return nil } else if !errors.Is(err, os.ErrNotExist) { return err }
    if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil { return err }
    // The marker belongs to the user config, not the versioned bundle. It
    // survives app upgrades and explicit removal from the plugin list.
    return os.WriteFile(marker, []byte("registered\n"), 0o600)
}
