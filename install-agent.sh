#!/usr/bin/env bash
set -euo pipefail

# UnitSense Agent installer
# Pin cosign trust root by repo identity + OIDC issuer.

REPO="UnitSense/agent"
COSIGN_OIDC_ISSUER="https://token.actions.githubusercontent.com"
INSTALL_DIR="${UNITSENSE_AGENT_INSTALL_DIR:-$HOME/.local/bin}"

err()  { echo "error: $*" >&2; exit 1; }
note() { echo "==> $*"; }

# 1. Resolve version (allow override via env)
VERSION="${UNITSENSE_AGENT_VERSION:-}"
if [ -z "$VERSION" ]; then
  note "Looking up latest release"
  VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' | head -1 | cut -d'"' -f4 | sed 's/^v//')
fi
[ -n "$VERSION" ] || err "could not resolve version"
TAG="v$VERSION"
note "Installing UnitSense Agent $TAG"

# 2. Resolve OS/arch
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) err "unsupported arch: $ARCH" ;;
esac

ARCHIVE="unitsense-agent_${VERSION}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/$REPO/releases/download/$TAG"

# 3. Cosign present?
command -v cosign >/dev/null || err "cosign required. Install: brew install cosign / apt install cosign"

# 4. Download artifacts to a temp dir
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
cd "$TMP"
note "Downloading artifacts"
curl -fsSLO "$BASE_URL/$ARCHIVE"
curl -fsSLO "$BASE_URL/$ARCHIVE.sig"
curl -fsSLO "$BASE_URL/$ARCHIVE.crt"

# 5. Verify with PINNED identity (refuse if mismatch)
COSIGN_IDENTITY="https://github.com/$REPO/.github/workflows/release.yml@refs/tags/$TAG"
note "Verifying cosign signature against $COSIGN_IDENTITY"
cosign verify-blob \
  --certificate-identity "$COSIGN_IDENTITY" \
  --certificate-oidc-issuer "$COSIGN_OIDC_ISSUER" \
  --signature "$ARCHIVE.sig" \
  --certificate "$ARCHIVE.crt" \
  "$ARCHIVE" >/dev/null || err "cosign verification failed — REFUSING to install"
note "Signature OK"

# 6. Extract + install to ~/.local/bin
tar -xzf "$ARCHIVE"
mkdir -p "$INSTALL_DIR"
install -m 0755 unitsense-agent "$INSTALL_DIR/unitsense-agent"
note "Installed to $INSTALL_DIR/unitsense-agent"

# 7. PATH check
case ":$PATH:" in
  *:"$INSTALL_DIR":*) ;;
  *)
    echo
    echo "  Note: $INSTALL_DIR is not in your PATH."
    echo "  Add to ~/.zshrc or ~/.bashrc:    export PATH=\"\$HOME/.local/bin:\$PATH\""
    ;;
esac

echo
echo "Next steps:"
echo "  unitsense-agent setup     # interactive (tenant, email, registration token)"
echo "  unitsense-agent install --schedule=10m"
