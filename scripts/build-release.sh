#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
version="${1:?Usage: scripts/build-release.sh VERSION [HTTPS_RELEASE_BASE]}"
base="${2:-${APPHOLDER_RELEASE_BASE_URL:-}}"
[[ "$version" =~ ^v?[0-9][A-Za-z0-9._-]{0,100}$ ]] || { echo 'Invalid version' >&2; exit 1; }
[[ -z "$base" || "$base" =~ ^https://[A-Za-z0-9./_-]+$ ]] || { echo 'Invalid release base URL' >&2; exit 1; }
[[ "$(go env GOOS)" == linux ]] || { echo 'Desktop releases must be built on Linux' >&2; exit 1; }
arch="$(go env GOARCH)"
case "$arch" in amd64|arm64) ;; *) echo 'Unsupported architecture' >&2; exit 1;; esac
public_key_b64=""
if [[ -n "${APPHOLDER_RELEASE_PUBLIC_KEY:-}" ]]; then
 [[ -r "$APPHOLDER_RELEASE_PUBLIC_KEY" ]] || { echo 'Cannot read APPHOLDER_RELEASE_PUBLIC_KEY' >&2; exit 1; }
 openssl pkey -pubin -in "$APPHOLDER_RELEASE_PUBLIC_KEY" -noout >/dev/null 2>&1 || { echo 'Release public key must be a valid PEM key' >&2; exit 1; }
 public_key_b64="$(base64 -w0 < "$APPHOLDER_RELEASE_PUBLIC_KEY")"
elif [[ -n "$base" ]]; then
 echo 'APPHOLDER_RELEASE_PUBLIC_KEY is required for a hosted release build' >&2
 exit 1
fi
dir="dist/$version"
mkdir -p "$dir"
go build -trimpath -buildvcs=false -tags desktop -ldflags "-s -w -X appholder/internal/selfupdate.Version=$version -X appholder/internal/selfupdate.ReleaseBaseURL=$base -X appholder/internal/selfupdate.ReleasePublicKey=$public_key_b64" -o "$dir/appholder-linux-$arch" ./cmd/appholder
(cd "$dir" && sha256sum appholder-linux-* > SHA256SUMS)
printf '%s\n' "$version" > dist/latest.txt
sed "s|base=\"\${APPHOLDER_RELEASE_BASE_URL:-}\"|base=\"\${APPHOLDER_RELEASE_BASE_URL:-$base}\"|" install.sh > dist/install.sh
echo "Built $dir/appholder-linux-$arch"
