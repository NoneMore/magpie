package detector

import (
    "io/fs"
    "os"
    "path/filepath"
    "testing"
    "testing/fstest"
)

func TestMaterializeIsImmutableAndIdempotent(t *testing.T) {
    source := fstest.MapFS{
        "index.js": {Data: []byte("export const Fingerprint = () => ({})")},
        "page/index.html": {Data: []byte("<html></html>")},
        "data/unified_bank.json": {Data: []byte("{}")},
    }
    root := t.TempDir()
    a, err := materialize(source, root)
    if err != nil { t.Fatal(err) }
    b, err := materialize(source, root)
    if err != nil || a != b { t.Fatalf("not idempotent: %s %s %v", a,b,err) }
    if _, err := os.Stat(filepath.Join(a, ".complete")); err != nil { t.Fatal(err) }
    source["index.js"] = &fstest.MapFile{Data: []byte("changed")}
    next, err := materialize(source, root)
    if err != nil || next == a { t.Fatalf("changed input must get new directory: %s / %v", next,err) }
    old, err := fs.ReadFile(os.DirFS(a), "index.js")
    if err != nil || string(old) != "export const Fingerprint = () => ({})" { t.Fatalf("old version mutated: %q / %v", old,err) }
}
