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
    "io"
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
    root := filepath.Join(settings.Dir(), "bundled", "lm-detector")
    marker := filepath.Join(settings.Dir(), "bundled-lm-detector.registered")
    // Carry forward the legacy marker, including a previous explicit uninstall.
    if _, err := os.Stat(filepath.Join(root, ".registered")); err == nil {
        if err := markBundled(marker); err != nil { return err }
    } else if !errors.Is(err, os.ErrNotExist) { return err }
    needed, err := bundledLMDetectorNeeded(root, marker)
    if err != nil || !needed { return err }

    for _, name := range []string{"data/unified_bank.json", "data/shared_detector.bin"} {
        b, err := lmDetectorFiles.ReadFile("bundled-lm-detector/runtime/" + name)
        if err != nil || len(b) < 1<<20 {
            return fmt.Errorf("LM Detector was built without %s; run node .github/scripts/prepare-lm-detector.mjs before go build", name)
        }
    }
    payload, err := fs.Sub(lmDetectorFiles, "bundled-lm-detector/runtime")
    if err != nil { return err }
    target, err := materializeLMDetector(payload, root)
    if err != nil { return err }
    return EnsureBundled(target, "magpie-lm-detector", marker)
}

// A persistent registration marker with no matching entry means the user
// uninstalled the bundle. Do not extract or even inspect its embedded assets
// on subsequent launches. A still-listed bundle must continue to migrate,
// including when the previous version's extraction has been deleted.
func bundledLMDetectorNeeded(root, marker string) (bool, error) {
    if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
        return true, nil
    } else if err != nil {
        return false, err
    }
    for _, e := range Load().Plugins {
        if bundledPackage(e, "magpie-lm-detector") || oldBundledSpec(e.Spec, root, "lm-detector") {
            return true, nil
        }
    }
    return false, nil
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
    if materializedBundleMatches(files, target, version) {
        return target, nil
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
    // Replace a damaged copy atomically, without mutating its files in place.
    versionDir := filepath.Dir(target)
    if materializedBundleMatches(files, target, version) { return target, nil }
    if _, err := os.Lstat(versionDir); err == nil {
        broken, err := os.MkdirTemp(root, ".broken-")
        if err != nil { return "", err }
        if err := os.Remove(broken); err != nil { return "", err }
        if err := os.Rename(versionDir, broken); err != nil {
            if materializedBundleMatches(files, target, version) { return target, nil }
            return "", fmt.Errorf("move damaged LM Detector bundle: %w", err)
        }
        defer os.RemoveAll(broken)
    } else if !errors.Is(err, os.ErrNotExist) { return "", err }
    if err := os.Rename(stage, versionDir); err != nil {
        if materializedBundleMatches(files, target, version) { return target, nil }
        return "", fmt.Errorf("publish bundled LM Detector: %w", err)
    }
    return target, nil
}

// Check the bytes, not just the presence of .complete: a damaged extraction
// must not be reused forever on later launches.
func materializedBundleMatches(files fs.FS, target, version string) bool {
    b, err := os.ReadFile(filepath.Join(target, ".complete"))
    if err != nil || string(b) != version+"\n" { return false }
    h := sha256.New()
    err = fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
        if err != nil { return err }
        if entry.IsDir() { return nil }
        file := filepath.Join(target, filepath.FromSlash(name))
        st, err := os.Lstat(file)
        if err != nil { return err }
        if !st.Mode().IsRegular() { return fmt.Errorf("not a regular bundle file: %s", name) }
        f, err := os.Open(file)
        if err != nil { return err }
        defer f.Close()
        h.Write([]byte(name))
        h.Write([]byte{0})
        _, err = io.Copy(h, f)
        return err
    })
    return err == nil && hex.EncodeToString(h.Sum(nil))[:20] == version
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
        if e.Spec == spec || bundledPackage(e, packageName) || oldBundledSpec(e.Spec, filepath.Dir(filepath.Dir(spec)), filepath.Base(spec)) {
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

// Match a prior content-addressed extraction even if its package.json was
// deleted with the cache. Do not treat arbitrary user plugin paths as ours.
func oldBundledSpec(old, root, name string) bool {
    if !filepath.IsAbs(old) || filepath.Base(old) != name { return false }
    rel, err := filepath.Rel(root, old)
    if err != nil { return false }
    parts := strings.Split(filepath.ToSlash(rel), "/")
    if len(parts) != 2 || len(parts[0]) != 20 || parts[1] != name { return false }
    _, err = hex.DecodeString(parts[0])
    return err == nil
}

func markBundled(marker string) error {
    if _, err := os.Stat(marker); err == nil { return nil } else if !errors.Is(err, os.ErrNotExist) { return err }
    if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil { return err }
    // The marker belongs to the user config, not the versioned bundle. It
    // survives app upgrades and explicit removal from the plugin list.
    return os.WriteFile(marker, []byte("registered\n"), 0o600)
}
