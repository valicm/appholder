# AppHolder icon

Direction D, "version stack": stacked app tiles for kept versions and rollback, with an update chevron on the front tile.

## Layout

```
svg/                         source art
  appholder-app-icon.svg       app icon (dark tile), 32 px and up
  appholder-app-icon-small.svg simplified two-layer version for 16 and 24 px
  appholder.svg                bare mark for light backgrounds (README, website)
  appholder-on-dark.svg        bare mark for dark backgrounds
  appholder-symbolic.svg       16 px single-color icon for tray and headerbar
icons/hicolor/               ready to install
  scalable/apps/appholder.svg
  symbolic/apps/appholder-symbolic.svg
  {16,24,32,48,64,128,256}x…/apps/appholder.png
artwork/                     512 px PNGs for README and release artwork
build.py                     regenerates icons/ and artwork/ (pip install cairosvg)
```

## AppHolder integration

The AppHolder Go binary embeds `internal/desktop/assets/hicolor/`. Running `appholder install-desktop` installs the icon set under the user's XDG data directory and registers its desktop launchers. The launchers and GTK window use `appholder`; the tray uses `appholder-symbolic`.

Regenerate the hicolor files and 512 px artwork after editing the SVG sources with:

```sh
python3 assets/branding/build.py
```

This requires `cairosvg` (`python3 -m pip install cairosvg`). The script also refreshes the icon files embedded into the desktop package.

If the app uses a reverse-DNS app ID (for example `io.github.<owner>.AppHolder`), rename the icon files to match that ID and change `NAME` in `build.py`.

## Colors

| Token | Hex |
|---|---|
| Ink (tile, front on light) | `#1E2227` |
| Paper (front on dark) | `#F4F2EE` |
| Layer 2 light / dark | `#8C939B` / `#7C848E` |
| Layer 3 light / dark | `#C8CCD1` / `#4A5260` |
| Accent (chevron) | `#3A6EA5` |
| Symbolic | `#2e3436` (recolored by GTK) |
