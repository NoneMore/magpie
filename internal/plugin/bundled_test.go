package plugin

import (
    "context"
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
    marker := filepath.Join(base, ".registered")
    a := fixtureBundled(t, base, "v1")
    b := fixtureBundled(t, base, "v2")
    if err := EnsureBundled(a, "magpie-lm-detector", marker); err != nil { t.Fatal(err) }
    if got := Load().Plugins; len(got) != 1 || got[0].Spec != a { t.Fatalf("first install: %+v", got) }
    if err := SetOff(a, true); err != nil { t.Fatal(err) }
    if err := EnsureBundled(b, "magpie-lm-detector", marker); err != nil { t.Fatal(err) }
    if got := Load().Plugins; len(got) != 1 || got[0].Spec != b || !got[0].Off { t.Fatalf("upgrade lost off choice: %+v", got) }
    if err := Remove(context.Background(), b); err != nil { t.Fatal(err) }
    if err := EnsureBundled(b, "magpie-lm-detector", marker); err != nil { t.Fatal(err) }
    if got := Load().Plugins; len(got) != 0 { t.Fatalf("resurrected removed plugin: %+v", got) }
}

func TestBundledMigratesManualCopyWithoutLosingOptions(t *testing.T) {
    storeSandbox(t)
    manual := fixtureBundled(t, t.TempDir(), "manual")
    base := filepath.Join(settings.Dir(), "bundled", "lm-detector")
    fresh := fixtureBundled(t, base, "version")
    marker := filepath.Join(base, ".registered")
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
    if err := EnsureBundled(pluginPath, "magpie-lm-detector", filepath.Join(base, ".registered")); err == nil {
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
    files["index.js"] = &fstest.MapFile{Data: []byte("changed")}
    next, err := materializeLMDetector(files, root)
    if err != nil || next == a { t.Fatalf("changed bundle must get a new directory: %s / %v", next,err) }
    old, err := fs.ReadFile(os.DirFS(a), "index.js")
    if err != nil || string(old) != "export const Fingerprint = () => ({})" { t.Fatalf("old bundle mutated: %q / %v", old,err) }
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
