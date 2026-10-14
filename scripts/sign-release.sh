#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
version="${1:?Usage: scripts/sign-release.sh VERSION}"
[[ "$version" =~ ^v?[0-9][A-Za-z0-9._-]{0,100}$ ]] || { echo 'Invalid version' >&2; exit 1; }
private_key="${APPHOLDER_SIGNING_KEY:-}"
public_key="${APPHOLDER_RELEASE_PUBLIC_KEY:-}"
[[ -n "$private_key" && -r "$private_key" ]] || { echo 'Set APPHOLDER_SIGNING_KEY to the Ed25519 release private key' >&2; exit 1; }
[[ -n "$public_key" && -r "$public_key" ]] || { echo 'Set APPHOLDER_RELEASE_PUBLIC_KEY to the matching PEM public key' >&2; exit 1; }
dir="dist/$version"
[[ -s "$dir/SHA256SUMS" ]] || { echo "Missing $dir/SHA256SUMS" >&2; exit 1; }
openssl pkey -pubin -in "$public_key" -text_pub -noout 2>/dev/null | head -n 1 | grep -q '^ED25519 Public-Key:' || { echo 'Release public key must be Ed25519' >&2; exit 1; }
work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
openssl pkey -in "$private_key" -pubout -outform DER -out "$work/private-public.der" 2>/dev/null || { echo 'Invalid release private key' >&2; exit 1; }
openssl pkey -pubin -in "$public_key" -outform DER -out "$work/public.der" 2>/dev/null || { echo 'Invalid release public key' >&2; exit 1; }
cmp -s "$work/private-public.der" "$work/public.der" || { echo 'Signing and public keys do not match' >&2; exit 1; }
awk 'NF != 2 || $1 !~ /^[[:xdigit:]]{64}$/ || $2 !~ /^appholder-linux-(amd64|arm64)$/ || seen[$2]++ { exit 1 } END { if (NR == 0) exit 1 }' "$dir/SHA256SUMS" || { echo 'Invalid or duplicate asset entries in SHA256SUMS' >&2; exit 1; }
(cd "$dir" && sha256sum -c SHA256SUMS >/dev/null) || { echo 'A release binary does not match SHA256SUMS' >&2; exit 1; }
openssl pkeyutl -sign -inkey "$private_key" -rawin -in "$dir/SHA256SUMS" -out "$dir/SHA256SUMS.sig"
public_key_b64="$(base64 -w0 < "$public_key")"
sed "s|trusted_public_key_b64=''|trusted_public_key_b64='$public_key_b64'|" dist/install.sh > "$work/install.sh"
grep -q "trusted_public_key_b64='$public_key_b64'" "$work/install.sh" || { echo 'Could not pin the public key in the installer' >&2; exit 1; }
mv "$work/install.sh" dist/install.sh
echo "Signed $dir/SHA256SUMS and pinned the verifier key in dist/install.sh"
