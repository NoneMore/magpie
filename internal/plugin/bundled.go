package plugin

import (
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "path/filepath"

    "github.com/yetone/magpie/internal/settings"
)

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
    if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil { return err }
    // The marker belongs to the user config, not the versioned bundle. It
    // survives app upgrades and explicit removal from the plugin list.
    return os.WriteFile(marker, []byte("registered\n"), 0o600)
}
