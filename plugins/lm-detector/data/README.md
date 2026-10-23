LM Detector reference assets are provided by lmfpd 0.0.1792416845676.

The large files unified_bank.json and shared_detector.bin are deliberately
not committed to the fork. Before building a release binary, run:

    node .github/scripts/prepare-lm-detector.mjs

That script fetches the fixed npm package at BUILD TIME only. bundle.go embeds
the generated files in the executable, so end users never install npm or
download fingerprint reference files. CI fails if the assets are missing.
