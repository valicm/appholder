# AppHolder

AppHolder is an open-source Linux desktop app for keeping AppImages organized and up to date. It watches folders, integrates apps into the desktop, checks publisher-provided update information, keeps rollback versions, and can launch selected apps through Bubblewrap.

AppHolder is under active development. The desktop app is written in Go with a GTK interface. The current build targets Linux on amd64 and arm64; the release workflow builds each architecture natively.

## What it does

- Watches configurable folders, defaulting to `~/Applications`. A newly dropped AppImage is registered after it remains unchanged across scans. It is never launched automatically.
- Lets you open an AppImage from another folder and choose **Run once**, **Integrate**, or **Integrate and run**. Integrating copies it into the first watched folder and creates a desktop launcher.
- Shows installed apps, extracted metadata, update state, and task progress in a searchable library. Multiple installed copies remain independently managed.
- Checks AppImage update metadata for HTTPS zsync sources and GitHub release sources. It supports `latest`, `latest-pre`, `latest-all`, and explicit tags. Binary-only GitHub releases can use GitHub's asset SHA-256 digest when no `.zsync` asset is published.
- Installs one or all available updates, verifies their size and digest, saves the previous file, and activates the replacement. Saved versions can be restored; retention is configurable and defaults to three.
- Can enable automatic updates per app and send desktop notifications for new apps, available updates, and completed automatic updates.
- Offers per-app Bubblewrap launching with a private persistent home and networking disabled by default. Network access can be enabled per app.
- Provides a tray icon, startup controls, a single desktop-instance mechanism, and `appimg:` links for local files or HTTPS downloads.
- Signs AppHolder release checksum manifests with Ed25519. The installer and self-updater verify the signature using a pinned public key.

## Install

After the first signed GitHub Release is published, install with:

```sh
curl --proto '=https' --proto-redir '=https' -fsSL \
  https://github.com/valicm/appholder/releases/latest/download/install.sh | bash
```

The installer defaults to `/usr/local/bin/appholder`, integrates desktop launchers, and enables user startup. Run it as your normal user; it elevates only the binary copy. To install in a user-owned directory instead:

```sh
curl --proto '=https' --proto-redir '=https' -fsSL \
  https://github.com/valicm/appholder/releases/latest/download/install.sh |
  APPHOLDER_INSTALL_DIR="$HOME/.local/bin" bash
```

The installer needs Bash, `curl`, `jq`, `openssl`, `base64`, `sha256sum`, and `flock`. It does not install dependencies automatically. A desktop build needs GTK 3 development/runtime libraries; metadata extraction uses `unsquashfs`; the sandbox option requires Bubblewrap (`bwrap`). Tray support depends on a compatible desktop tray host and AppIndicator runtime.

## First run and storage

Start the desktop app with:

```sh
appholder gui
```

AppHolder starts a per-user daemon that watches the configured folders and performs update work. The desktop window and CLI communicate with it over a private Unix socket. The window can close while the daemon continues running. The installer enables startup by default using a systemd **user** service and a tray autostart entry. You can also control startup in Settings or with `appholder startup enable|disable|status`.

The initial watched folder is `~/Applications`. Settings lets you add or remove watched folders, choose the backup location, and set the saved-version retention count. AppHolder respects `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, and `XDG_DATA_HOME`:

- Configuration: `$XDG_CONFIG_HOME/appholder/config.json` (normally `~/.config/appholder/config.json`)
- State, backups, sandbox profiles, and incoming link downloads: `$XDG_STATE_HOME/appholder/` (normally `~/.local/state/appholder/`)
- Desktop launchers: `$XDG_DATA_HOME/applications/` (normally `~/.local/share/applications/`)

`~/Applications` contains the live AppImages. Backups and sandbox profiles are stored separately.

## Common commands

```sh
appholder list
appholder integrate /path/to/Example.AppImage
appholder run /path/to/Example.AppImage
appholder integrate-run /path/to/Example.AppImage
appholder check-updates
appholder update-all
appholder update APPLICATION_ID
appholder rollback APPLICATION_ID
appholder operations
appholder remove APPLICATION_ID
appholder uninstall APPLICATION_ID --yes
appholder uninstall-app
appholder uninstall-app --purge
appholder self-update --check
```

Update, rollback, remove, and uninstall operations normally require the daemon. **Remove** unregisters an app while keeping its file and saved versions. **Uninstall** permanently deletes that AppImage file; its saved versions remain subject to the retention setting.

`uninstall-app` removes AppHolder itself and its generated desktop integration. It keeps your AppImages, settings, and saved versions by default. Add `--purge` to also delete AppHolder's configuration, metadata, and saved versions. AppImages in watched folders are kept either way.

## Updates and version history

AppHolder reads update information embedded by the AppImage publisher. Supported sources are HTTPS zsync metadata and `gh-releases-zsync`. It does not infer update sources from filenames or websites. GitHub's `latest-pre` and `latest-all` selectors are supported. When a matching `.zsync` file is absent, AppHolder can use the corresponding `.AppImage` release asset if GitHub provides its SHA-256 digest and size.

If the system `zsync` tool is installed, AppHolder attempts a delta update. It verifies the completed file and falls back to a full HTTPS download if the delta path is unavailable or fails. Downloads are size-limited. Before activation, AppHolder saves the current file; saved versions can be selected and restored from the application details. Automatic updates are optional for each app and do not restart running applications.

These checks establish that a downloaded file matches the publisher's update metadata or GitHub's published asset digest. They do not prove who authored an AppImage. AppHolder does not execute an AppImage to read its metadata.

## Per-app sandbox

Select an app and enable **Run in sandbox** to launch it through Bubblewrap. Each app uses its own persistent home under AppHolder's state directory, and networking is disabled unless **Allow network access in sandbox** is selected. AppHolder still shares the host display connection so graphical apps can open. This is a best-effort restriction layer, not a hardened boundary for hostile software; some apps that depend on D-Bus, removable media, or desktop services may not work.

## AppImage links

Desktop integration registers the `x-scheme-handler/appimg` handler. Local files can be opened with a link such as:

```text
appimg:///home/alice/Applications/Example.AppImage
```

HTTPS downloads use the explicit form:

```text
appimg://download?url=https%3A%2F%2Fexample.com%2FExample.AppImage
```

Remote downloads are limited to 16 GiB, must remain on HTTPS through redirects, and are checked for an AppImage marker before AppHolder presents the Run once / Integrate choices. They are staged under AppHolder's state directory and are not executed automatically.

## Releases and signing

The GitHub Actions release workflow runs when a `v*` tag is pushed. It builds native amd64 and arm64 binaries, creates a SHA-256 manifest, signs the exact manifest with Ed25519, and publishes the binaries, manifest, signature, and configured installer as GitHub Release assets. Tags containing `-` are marked as prereleases; self-update follows GitHub's latest stable release.

Configure these GitHub Actions settings before creating a release:

- Repository variable `APPHOLDER_RELEASE_PUBLIC_KEY`: the PEM Ed25519 public key.
- Repository secret `APPHOLDER_SIGNING_KEY`: the matching PEM private key.

Keep the private key outside the repository. Generate a pair locally with:

```sh
install -d -m 700 "$HOME/.local/share/appholder-release"
openssl genpkey -algorithm ED25519 \
  -out "$HOME/.local/share/appholder-release/private.pem"
openssl pkey -in "$HOME/.local/share/appholder-release/private.pem" \
  -pubout -out "$HOME/.local/share/appholder-release/public.pem"
```

The test workflow runs race-enabled Go tests and desktop-tagged tests for pushes and pull requests. The release workflow uses GitHub-hosted Ubuntu x64 and Arm64 runners. See [GitHub Actions workflows](.github/workflows/) and [GitHub's runner reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners).

For a local desktop build and test run:

```sh
go build -tags desktop -o bin/appholder ./cmd/appholder
go test -race ./...
go test -tags desktop ./...
shellcheck install.sh scripts/*.sh
```

Release builds without `APPHOLDER_RELEASE_PUBLIC_KEY` cannot self-update. The release scripts can also publish to a generic HTTPS directory host, but GitHub Releases are the configured project path.

## GitHub Pages

The `site/` directory contains the AppHolder project page. A Pages workflow publishes it when changes reach the default branch. In the repository's **Settings → Pages**, set the publishing source to **GitHub Actions**. The page is generated with the repository's own URL, so it works as a project site under `https://OWNER.github.io/REPOSITORY/`.

GitHub Pages sites are public. The site contains project information and release links only; do not place signing keys or other secrets in `site/`.

## Current limitations

- AppImage format detection and metadata are not a trust check. Files remain publisher-controlled executable code.
- The sandbox shares the display connection and may expose desktop-session capabilities; it is not intended to contain determined malware.
- Delta downloads need the system `zsync` program. Otherwise, verified full downloads are used.
- AppImage metadata support focuses on Type 2 images and PNG icons. `unsquashfs` is required for SquashFS metadata extraction.
- Tray availability and file-manager double-click behavior depend on the Linux desktop environment. Visual testing across desktop environments and distributions remains ongoing.
- Release signing protects updates only when the installed binary or trusted installer already has the project's public key pinned.

## License

AppHolder is distributed under the MIT License. See [LICENSE](LICENSE).
