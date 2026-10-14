#!/usr/bin/env bash
set -euo pipefail
die() { echo "AppHolder: $*" >&2; exit 1; }
local_release=""
startup=true
desktop=true
trusted_public_key_b64=''
while [[ $# -gt 0 ]]; do
 case "$1" in --no-startup) startup=false; shift;; --no-desktop) desktop=false; shift;; --startup) startup=true; shift;; --local) [[ $# -ge 2 ]] || die '--local requires a release directory'; local_release="$2"; shift 2;; *) die "Unknown argument: $1";; esac
done
if [[ ( "$startup" == true || "$desktop" == true ) && "$(id -u)" == 0 ]]; then die 'Run this installer without sudo; it elevates only the binary copy. For a root binary-only installation use --no-desktop --no-startup.'; fi
[[ "$(uname -s)" == Linux ]] || die 'Only Linux is supported'
case "$(uname -m)" in x86_64) arch=amd64;; aarch64|arm64) arch=arm64;; *) die 'Unsupported architecture';; esac
asset="appholder-linux-$arch"
install_dir="${APPHOLDER_INSTALL_DIR:-/usr/local/bin}"
[[ "$install_dir" == /* ]] || die 'APPHOLDER_INSTALL_DIR must be absolute'
command -v sha256sum >/dev/null || die 'sha256sum is required'
command -v openssl >/dev/null || die 'openssl is required to verify the release signature'
command -v base64 >/dev/null || die 'base64 is required to load the pinned release key'
[[ -n "$trusted_public_key_b64" ]] || die 'This installer has no pinned release-signing key; use the configured installer from the official release'
command -v flock >/dev/null || die 'flock is required'
work="$(mktemp -d)"
staged=""
privileged=()
cleanup() { rm -rf -- "$work"; if [[ -n "$staged" ]]; then "${privileged[@]}" rm -f -- "$staged"; fi; }
trap cleanup EXIT
if [[ -n "$local_release" ]]; then
 cp -- "$local_release/$asset" "$work/$asset"
 cp -- "$local_release/SHA256SUMS" "$work/SHA256SUMS"
 cp -- "$local_release/SHA256SUMS.sig" "$work/SHA256SUMS.sig"
else
 command -v curl >/dev/null || die 'curl is required'
 base="${APPHOLDER_RELEASE_BASE_URL:-}"
 [[ "$base" == https://* && "$base" != *[\?\#]* ]] || die 'Set APPHOLDER_RELEASE_BASE_URL to the HTTPS release directory'
 base="${base%/}"
 if [[ "$base" =~ ^https://github\.com/([^/]+)/([^/]+)$ ]]; then
  command -v jq >/dev/null || die 'jq is required to install from GitHub Releases'
  owner="${BASH_REMATCH[1]}"
  repository="${BASH_REMATCH[2]}"
  release="$(curl --proto '=https' --proto-redir '=https' -fsSL --max-time 30 "https://api.github.com/repos/$owner/$repository/releases/latest")"
  version="$(jq -er '.tag_name | select(type == "string")' <<< "$release")" || die 'Invalid GitHub release response'
  [[ "$version" =~ ^v?[0-9][A-Za-z0-9._-]{0,100}$ ]] || die 'Invalid published version'
  sums_url="$(jq -er '[.assets[] | select(.name == "SHA256SUMS")] | if length == 1 then .[0].browser_download_url else error("missing or duplicate SHA256SUMS") end' <<< "$release")" || die 'GitHub release is missing SHA256SUMS'
  signature_url="$(jq -er '[.assets[] | select(.name == "SHA256SUMS.sig")] | if length == 1 then .[0].browser_download_url else error("missing or duplicate signature") end' <<< "$release")" || die 'GitHub release is missing SHA256SUMS.sig'
  binary_url="$(jq -er --arg name "$asset" '[.assets[] | select(.name == $name)] | if length == 1 then .[0].browser_download_url else error("missing or duplicate binary") end' <<< "$release")" || die "GitHub release is missing $asset"
  curl --proto '=https' --proto-redir '=https' -fsSL --max-time 30 -o "$work/SHA256SUMS" "$sums_url"
  curl --proto '=https' --proto-redir '=https' -fsSL --max-time 30 -o "$work/SHA256SUMS.sig" "$signature_url"
  curl --proto '=https' --proto-redir '=https' -fsSL --max-time 600 --max-filesize 268435456 -o "$work/$asset" "$binary_url"
 else
  version="$(curl --proto '=https' --proto-redir '=https' -fsSL --max-time 30 "$base/latest.txt")"
  [[ "$version" =~ ^v?[0-9][A-Za-z0-9._-]{0,100}$ ]] || die 'Invalid published version'
  curl --proto '=https' --proto-redir '=https' -fsSL --max-time 30 -o "$work/SHA256SUMS" "$base/$version/SHA256SUMS"
  curl --proto '=https' --proto-redir '=https' -fsSL --max-time 30 -o "$work/SHA256SUMS.sig" "$base/$version/SHA256SUMS.sig"
  curl --proto '=https' --proto-redir '=https' -fsSL --max-time 600 --max-filesize 268435456 -o "$work/$asset" "$base/$version/$asset"
 fi
fi
printf '%s' "$trusted_public_key_b64" | base64 -d > "$work/release-public.pem" || die 'Invalid pinned release public key'
openssl pkeyutl -verify -pubin -inkey "$work/release-public.pem" -rawin -in "$work/SHA256SUMS" -sigfile "$work/SHA256SUMS.sig" >/dev/null 2>&1 || die 'Release signature verification failed; nothing installed'
want="$(awk -v asset="$asset" '$2 == asset || $2 == "*" asset {print $1}' "$work/SHA256SUMS")"
[[ "$want" =~ ^[a-fA-F0-9]{64}$ ]] || die 'Missing or ambiguous checksum'
got="$(sha256sum "$work/$asset")"; got="${got%% *}"
[[ "${want,,}" == "$got" ]] || die 'Checksum mismatch; nothing installed'
if [[ ! -w "$install_dir" ]]; then
 [[ "$(id -u)" == 0 ]] || { command -v sudo >/dev/null || die "Cannot write $install_dir; use sudo or APPHOLDER_INSTALL_DIR"; privileged=(sudo); }
fi
"${privileged[@]}" mkdir -p -- "$install_dir"
[[ ! -L "$install_dir/appholder" ]] || die 'Refusing to overwrite a symlink'
staged="$("${privileged[@]}" mktemp "$install_dir/.appholder-install-XXXXXX")"
"${privileged[@]}" install -m 0755 -- "$work/$asset" "$staged"
"${privileged[@]}" flock "$install_dir/appholder.update.lock" mv -f -- "$staged" "$install_dir/appholder"
staged=""
echo "Installed $install_dir/appholder"
if [[ "$desktop" == true ]]; then
 "$install_dir/appholder" install-desktop
 if command -v update-desktop-database >/dev/null; then
  data_home="${XDG_DATA_HOME:-$HOME/.local/share}"
  update-desktop-database "$data_home/applications" || echo 'Desktop entries installed; MIME cache refresh failed.' >&2
 fi
fi
echo 'Run as your normal user: appholder gui' 
echo 'Existing daemon and tray processes must be restarted to use the new binary.'

if [[ "$startup" == true ]]; then "$install_dir/appholder" startup enable; fi
