# Fork upstream sync & Windows releases

This is an independent Windows-focused fork of `yetone/magpie`. Its custom
commits are preserved during upstream synchronization.

## Daily upstream sync

- `.github/workflows/sync-upstream.yml` checks `yetone/magpie:main` daily
  at **02:17 UTC** (10:17 Beijing time), or can be run manually in Actions.
- The runner merges upstream into the fork's `main` with a normal Git merge.
  It never rebases, resets, or force-pushes the fork's custom work.
- Windows vet, compilation and focused fork regression tests run **before**
  the merged commit is pushed.
- If the merge conflicts or tests fail, nothing is pushed or published.
  Investigate the failed Actions run and resolve conflicts manually.
- A successful merge triggers the fork's Windows release workflow.
  An unchanged upstream produces no new release.

## Windows releases

- `.github/workflows/release.yml` builds and publishes from this fork's
  exact commit SHA, with a unique `fork-<12-char-SHA>` tag.
- It builds Windows x64 and ARM64 GUI executables (using the repository's
  `build/windows/winres.json` resources), CLI executables and ZIP archives.
  It publishes a SHA256SUMS.txt file to the fork's GitHub Releases page.
- It can also run on normal pushes to `main` or manually in Actions.
- It uses the automatic `GITHUB_TOKEN` and needs no third-party PAT or
  secrets from `yetone/magpie-releases`.
- Windows binaries are **unsigned**. The official upstream self-update
  mechanism may install upstream binaries rather than this fork's changes.
  To keep custom changes, install files from this fork's Releases page.
- No Docker publishing is triggered by the synchronization workflow. The
  repository's separate Docker workflow has not been modified.

If releases or pushing fail with a permissions error, check **Settings >
Actions > General > Workflow permissions**, and any branch rules for `main`.
