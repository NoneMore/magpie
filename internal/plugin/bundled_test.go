package plugin

import (
    "context"
    "os"
    "path/filepath"
    "testing"

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
