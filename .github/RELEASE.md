# Release Process

This document explains how the notx release pipeline works and how to set it
up from scratch (including the Homebrew tap).

---

## How a release is triggered

Push a **semver tag** to `main`:

```sh
git tag v1.0.0
git push origin v1.0.0
```

The `.github/workflows/release.yml` workflow fires automatically and:

1. Builds the `notx` binary for **macOS arm64** and **macOS amd64**
   (including the embedded admin UI).
2. Packages each binary into a `.tar.gz` archive.
3. Creates a **GitHub Release** named after the tag, attaches the archives,
   and auto-generates release notes from merged PRs / commits.
4. Pushes an updated **Homebrew formula** to the
   [`zebaqui/homebrew-notx`](https://github.com/zebaqui/homebrew-notx) tap
   repository so `brew install notx` picks up the new version automatically.

Pre-release tags (anything containing a `-`, e.g. `v1.0.0-beta.1`) are marked
as pre-releases on GitHub and are **not** picked up by default `brew install`.

---

## One-time setup

### 1. Create the Homebrew tap repository

1. Create a **new public GitHub repository** named exactly `homebrew-notx`
   under the `zebaqui` org:
   `https://github.com/zebaqui/homebrew-notx`

2. Inside that repo create the directory structure:
   ```
   homebrew-notx/
   └── Formula/
       └── notx.rb      ← copied from .github/homebrew/notx.rb (seed)
   ```

3. Commit and push — the workflow will overwrite `Formula/notx.rb` on every
   release from that point on.

### 2. Create the `HOMEBREW_TAP_TOKEN` secret

The release workflow needs write access to `zebaqui/homebrew-notx`.

1. Go to **GitHub → Settings → Developer settings → Personal access tokens →
   Fine-grained tokens** (or a classic PAT with `repo` scope).
2. Grant it **Contents: Read and Write** on `zebaqui/homebrew-notx`.
3. In the **`notx-engine` repository** go to
   **Settings → Secrets and variables → Actions → New repository secret**.
4. Name it `HOMEBREW_TAP_TOKEN` and paste the token value.

### 3. Verify the workflow permissions

In `notx-engine` → **Settings → Actions → General → Workflow permissions**,
make sure **"Read and write permissions"** is enabled (this allows the workflow
to create GitHub Releases).

---

## Installing notx via Homebrew

Once at least one release has been published:

```sh
# Add the tap (only needed once)
brew tap zebaqui/notx

# Install
brew install notx

# Upgrade to the latest release later
brew upgrade notx
```

Or as a one-liner:

```sh
brew install zebaqui/notx/notx
```

---

## Release archive naming convention

| Platform      | Archive name                                    |
|---------------|-------------------------------------------------|
| macOS arm64   | `notx-<version>-darwin-arm64.tar.gz`            |
| macOS amd64   | `notx-<version>-darwin-amd64.tar.gz`            |

Each archive contains a single binary named `notx-darwin-<arch>`.
The Homebrew formula renames it to `notx` on install.

---

## Adding more platforms later

To add **Linux** (or Windows) builds:

1. Add a new job in `release.yml` (or extend the build matrix) with the
   appropriate `GOOS` / `GOARCH` values.
2. Upload the resulting archive as an artifact and include it in the
   `softprops/action-gh-release` `files:` list.
3. Extend the Homebrew formula `on_linux` block (or create a separate
   Linuxbrew formula) if desired.

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|-------------|-----|
| Tap push fails with 403 | `HOMEBREW_TAP_TOKEN` missing or expired | Rotate token, update secret |
| `brew install` gets old SHA256 | Formula not updated | Check the `update-tap` job logs |
| Binary reports `version: dev` | Tag not pushed, only branch | Push the tag with `git push origin <tag>` |
| `shasum` mismatch on install | Archive was modified after release | Re-release with a new tag |
