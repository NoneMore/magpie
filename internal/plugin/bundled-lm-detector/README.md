Generated embedded plugin files live in the ignored runtime/ directory.

The tracked JS/TypeScript source of truth is plugins/lm-detector/.
Run node .github/scripts/prepare-lm-detector.mjs before building releases.
The script copies the Page, backend, shared scorer, and pinned lmfpd data
into runtime/; the Go bootstrap embeds that directory in the final binary.
