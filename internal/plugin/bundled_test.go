package plugin

import (
    "context"
    "errors"
    "os"
    "path/filepath"
    "testing"
    "testing/fstest"
    "io/fs"

    "github.com/yetone/magpie/internal/settings"
)

func fixtureBundled(t *testing.T, parent, version string) string {
    t.Helper()
    p := filepath.Join(parent, version)
    if err := os.MkdirAll(p, 0o700); err != nil { t.Fatal(err) }
    if err := os.WriteFile(filepath.Join(p, "package.json"), []byte("{\"name\":\"magpie-lm-detector\",\"main\":\"index.js\"}"), 0o600); err != nil { t.Fatal(err) }
    if err := os.WriteFile(filepath.Join(p, "index.js"), []byte("export const Fingerprint = () => ({});"), 0o600); err != nil { t.Fatal(err) }
    return p
}

func TestBundledFirstRunUpgradeOffAndExplicitRemoval(t *testing.T) {
    storeSandbox(t)
    base := filepath.Join(settings.Dir(), "bundled", "lm-detector")
    marker := filepath.Join(settings.Dir(), "bundled-lm-detector.registered")
    a := fixtureBundled(t, base, "v1")
    b := fixtureBundled(t, base, "v2")
    if err := EnsureBundled(a, "magpie-lm-detector", marker); err != nil { t.Fatal(err) }
    if got := Load().Plugins; len(got) != 1 || got[0].Spec != a { t.Fatalf("first install: %+v", got) }
    if err := SetOff(a, true); err != nil { t.Fatal(err) }
    if err := EnsureBundled(b, "magpie-lm-detector", marker); err != nil { t.Fatal(err) }
    if got := Load().Plugins; len(got) != 1 || got[0].Spec != b || !got[0].Off { t.Fatalf("upgrade lost off choice: %+v", got) }
    if err := Remove(context.Background(), b); err != nil { t.Fatal(err) }
    if err := os.RemoveAll(base); err != nil { t.Fatal(err) } // removing generated cache must not erase uninstall intent
    if err := EnsureBundled(b, "magpie-lm-detector", marker); err != nil { t.Fatal(err) }
    if got := Load().Plugins; len(got) != 0 { t.Fatalf("resurrected removed plugin: %+v", got) }
}

func TestBundledMigratesManualCopyWithoutLosingOptions(t *testing.T) {
    storeSandbox(t)
    manual := fixtureBundled(t, t.TempDir(), "manual")
    base := filepath.Join(settings.Dir(), "bundled", "lm-detector")
    fresh := fixtureBundled(t, base, "version")
    marker := filepath.Join(settings.Dir(), "bundled-lm-detector.registered")
    l := List{Plugins: []Entry{{Spec: manual, Off: true, Options: map[string]any{"custom": true}}},
        Prefer: map[string]string{"provider": manual}}
    if err := save(l); err != nil { t.Fatal(err) }
    if err := EnsureBundled(fresh, "magpie-lm-detector", marker); err != nil { t.Fatal(err) }
    got := Load()
    if len(got.Plugins) != 1 || got.Plugins[0].Spec != fresh || !got.Plugins[0].Off ||
        got.Plugins[0].Options["custom"] != true || got.Prefer["provider"] != fresh {
        t.Fatalf("manual migration lost state: %+v", got)
    }
}

func TestBundledFailsClosedOnInvalidPluginList(t *testing.T) {
    storeSandbox(t)
    base := filepath.Join(settings.Dir(), "bundled", "lm-detector")
    pluginPath := fixtureBundled(t, base, "v1")
    writeList(t, "{bad json")
    if err := EnsureBundled(pluginPath, "magpie-lm-detector", filepath.Join(settings.Dir(), "bundled-lm-detector.registered")); err == nil {
        t.Fatal("invalid config should not be overwritten")
    }
    b, err := os.ReadFile(listPath())
    if err != nil || string(b) != "{bad json" { t.Fatalf("invalid config overwritten: %q / %v", b, err) }
}

func TestMaterializeBundledCopyKeepsStableName(t *testing.T) {
    files := fstest.MapFS{
        "index.js": {Data: []byte("export const Fingerprint = () => ({})")},
        "page/index.html": {Data: []byte("<html></html>")},
        "data/unified_bank.json": {Data: []byte("{}")},
    }
    root := t.TempDir()
    a, err := materializeLMDetector(files, root)
    if err != nil { t.Fatal(err) }
    b, err := materializeLMDetector(files, root)
    if err != nil || a != b { t.Fatalf("not idempotent: %s %s %v", a,b,err) }
    if filepath.Base(a) != "lm-detector" { t.Fatalf("unstable plugin short name: %s", a) }
    if _, err := os.Stat(filepath.Join(a, ".complete")); err != nil { t.Fatal(err) }
    // A complete marker does not justify reusing deleted or modified files.
    if err := os.WriteFile(filepath.Join(a, "index.js"), []byte("damaged"), 0o600); err != nil { t.Fatal(err) }
    repaired, err := materializeLMDetector(files, root)
    if err != nil || repaired != a { t.Fatalf("did not repair damaged bundle: %s / %v", repaired, err) }
    original, err := os.ReadFile(filepath.Join(a, "index.js"))
    if err != nil || string(original) != "export const Fingerprint = () => ({})" { t.Fatalf("bad repair: %q / %v", original, err) }
    if err := os.Remove(filepath.Join(a, "page", "index.html")); err != nil { t.Fatal(err) }
    if _, err := materializeLMDetector(files, root); err != nil { t.Fatalf("did not restore missing bundle file: %v", err) }
    files["index.js"] = &fstest.MapFile{Data: []byte("changed")}
    next, err := materializeLMDetector(files, root)
    if err != nil || next == a { t.Fatalf("changed bundle must get a new directory: %s / %v", next,err) }
    old, err := fs.ReadFile(os.DirFS(a), "index.js")
    if err != nil || string(old) != "export const Fingerprint = () => ({})" { t.Fatalf("old bundle mutated: %q / %v", old,err) }
}

func TestBundledMissingPreviousExtractionMigrates(t *testing.T) {
    storeSandbox(t)
    base := filepath.Join(settings.Dir(), "bundled", "lm-detector")
    marker := filepath.Join(settings.Dir(), "bundled-lm-detector.registered")
    old := fixtureBundled(t, filepath.Join(base, "aaaaaaaaaaaaaaaaaaaa"), "lm-detector")
    next := fixtureBundled(t, filepath.Join(base, "bbbbbbbbbbbbbbbbbbbb"), "lm-detector")
    if err := EnsureBundled(old, "magpie-lm-detector", marker); err != nil { t.Fatal(err) }
    if err := SetOff(old, true); err != nil { t.Fatal(err) }
    if err := os.RemoveAll(old); err != nil { t.Fatal(err) }
    if err := EnsureBundled(next, "magpie-lm-detector", marker); err != nil { t.Fatal(err) }
    got := Load().Plugins
    if len(got) != 1 || got[0].Spec != next || !got[0].Off {
        t.Fatalf("stale path created duplicate or lost off choice: %+v", got)
    }
}

func TestPreparedBundlePreservesLegacyRemoval(t *testing.T) {
    if _, err := lmDetectorFiles.ReadFile("bundled-lm-detector/runtime/data/unified_bank.json"); err != nil {
        if os.Getenv("LM_DETECTOR_REQUIRED") == "1" { t.Fatal(err) }
        t.Skip("reference assets are not prepared")
    }
    storeSandbox(t)
    root := filepath.Join(settings.Dir(), "bundled", "lm-detector")
    if err := os.MkdirAll(root, 0o700); err != nil { t.Fatal(err) }
    if err := os.WriteFile(filepath.Join(root, ".registered"), []byte("registered\n"), 0o600); err != nil { t.Fatal(err) }
    if err := EnsureBundledLMDetector(); err != nil { t.Fatal(err) }
    if err := os.RemoveAll(root); err != nil { t.Fatal(err) }
    if err := EnsureBundledLMDetector(); err != nil { t.Fatal(err) }
    if got := Load().Plugins; len(got) != 0 { t.Fatalf("resurrected previously removed plugin: %+v", got) }
    if _, err := os.Stat(filepath.Join(settings.Dir(), "bundled-lm-detector.registered")); err != nil { t.Fatal(err) }
}

func TestPreparedBundleBootstrap(t *testing.T) {
    if _, err := lmDetectorFiles.ReadFile("bundled-lm-detector/runtime/data/unified_bank.json"); err != nil {
        if os.Getenv("LM_DETECTOR_REQUIRED") == "1" { t.Fatalf("build preparation missing: %v", err) }
        t.Skip("build-time reference assets have not been prepared in this source checkout")
    }
    storeSandbox(t)
    if err := EnsureBundledLMDetector(); err != nil { t.Fatal(err) }
    l := Load()
    if len(l.Plugins) != 1 { t.Fatalf("want one auto-registered plugin: %+v", l.Plugins) }
    if filepath.Base(l.Plugins[0].Spec) != "lm-detector" { t.Fatalf("unstable plugin spec: %+v", l.Plugins[0]) }
    w, _ := Window(Target(l.Plugins[0].Spec))
    if w == nil || w.Page == nil || len(w.Actions) != 1 { t.Fatalf("missing Page/Provider action in bundled plugin: %+v", w) }
}

func TestBundledRemovedSkipsExtraction(t *testing.T) {
    for _, otherPlugin := range []bool{false, true} {
        name := "empty-list"
        if otherPlugin { name = "other-plugin-present" }
        t.Run(name, func(t *testing.T) {
            storeSandbox(t)
            root := filepath.Join(settings.Dir(), "bundled", "lm-detector")
            marker := filepath.Join(settings.Dir(), "bundled-lm-detector.registered")
            if err := markBundled(marker); err != nil { t.Fatal(err) }
            if otherPlugin {
                if err := save(List{Plugins: []Entry{{Spec: "unrelated-plugin@latest"}}}); err != nil { t.Fatal(err) }
            }
            // Works even in an unprepared source checkout; a removed plugin
            // must not check the embedded data bank or create an extraction.
            if err := EnsureBundledLMDetector(); err != nil { t.Fatal(err) }
            if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
                t.Fatalf("removed plugin extracted into %s: %v", root, err)
            }
        })
    }
}

func TestBundledLegacyRemovalSkipsExtraction(t *testing.T) {
    storeSandbox(t)
    root := filepath.Join(settings.Dir(), "bundled", "lm-detector")
    if err := os.MkdirAll(root, 0o700); err != nil { t.Fatal(err) }
    if err := os.WriteFile(filepath.Join(root, ".registered"), []byte("registered\n"), 0o600); err != nil { t.Fatal(err) }
    if err := EnsureBundledLMDetector(); err != nil { t.Fatal(err) }
    entries, err := os.ReadDir(root)
    if err != nil { t.Fatal(err) }
    if len(entries) != 1 || entries[0].Name() != ".registered" {
        t.Fatalf("legacy removal left new extraction entries: %+v", entries)
    }
    if _, err := os.Stat(filepath.Join(settings.Dir(), "bundled-lm-detector.registered")); err != nil {
        t.Fatalf("legacy marker was not migrated: %v", err)
    }
}

func TestBundledRemovedCheckAllowsExistingVersionMigration(t *testing.T) {
    storeSandbox(t)
    root := filepath.Join(settings.Dir(), "bundled", "lm-detector")
    marker := filepath.Join(settings.Dir(), "bundled-lm-detector.registered")
    if err := markBundled(marker); err != nil { t.Fatal(err) }
    // Simulate an installed old version whose extraction disappeared.
    old := filepath.Join(root, "aaaaaaaaaaaaaaaaaaaa", "lm-detector")
    if err := save(List{Plugins: []Entry{{Spec: old, Off: true}}}); err != nil { t.Fatal(err) }
    needed, err := bundledLMDetectorNeeded(root, marker)
    if err != nil || !needed { t.Fatalf("lost upgrade eligibility for missing old extraction: %v, needed=%v", err, needed) }
}
