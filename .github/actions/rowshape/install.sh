#!/usr/bin/env bash
# Download the released rowshape binary for this runner and expose it on PATH so
# the run step can invoke `rowshape validate`.
#
# RELEASE-GATED (P0-T4): this cannot be exercised end-to-end until a tagged
# release publishes assets. To keep the release-gated part from being wholly
# unverified, the archive-name computation is factored into rowshape_asset_name()
# and mirrors .goreleaser.yaml archives.name_template and npm/install.js EXACTLY
# — raw lowercase GOOS/GOARCH, windows -> .zip — and is unit-tested for all five
# published platform/arch combos in test/action (TestInstallAssetNaming), the
# same way npm/naming.test.js guards install.js.
#
# Sourcing with ROWSHAPE_INSTALL_SOURCE_ONLY=1 defines the helpers without
# performing any network I/O, so the naming can be checked in isolation.
set -u

rowshape_os() { # uname -s -> goreleaser GOOS
  case "$1" in
    Linux) echo linux ;;
    Darwin) echo darwin ;;
    MINGW* | MSYS* | CYGWIN* | Windows_NT) echo windows ;;
    *) echo "" ;;
  esac
}

rowshape_arch() { # uname -m -> goreleaser GOARCH
  case "$1" in
    x86_64 | amd64) echo amd64 ;;
    arm64 | aarch64) echo arm64 ;;
    *) echo "" ;;
  esac
}

rowshape_asset_name() { # version os arch -> archive filename
  local v="$1" os="$2" arch="$3" ext=tar.gz
  [ "$os" = windows ] && ext=zip
  printf 'rowshape_%s_%s_%s.%s' "$v" "$os" "$arch" "$ext"
}

if [ "${ROWSHAPE_INSTALL_SOURCE_ONLY:-}" = "1" ]; then
  return 0 2>/dev/null || exit 0
fi

REPO="${INPUT_REPO:-rowshape/rowshape}"
OS=$(rowshape_os "$(uname -s)")
ARCH=$(rowshape_arch "$(uname -m)")
if [ -z "$OS" ] || [ -z "$ARCH" ]; then
  echo "rowshape: unsupported runner $(uname -s)/$(uname -m)" >&2
  echo "rowshape: install a binary from https://github.com/${REPO}/releases and set the 'binary' input" >&2
  exit 1
fi
# goreleaser ignores windows/arm64 (.goreleaser.yaml ignore block), so no asset
# exists — say so plainly rather than 404 (mirrors npm/install.js).
if [ "$OS" = windows ] && [ "$ARCH" = arm64 ]; then
  echo "rowshape: windows/arm64 is not a released target" >&2
  exit 1
fi

raw="${INPUT_VERSION:-latest}"
if [ "$raw" = latest ] || [ -z "$raw" ]; then
  # Resolve the latest tag from the redirect target of /releases/latest.
  tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
    "https://github.com/${REPO}/releases/latest" | sed 's#.*/tag/##')
  if [ -z "$tag" ]; then
    echo "rowshape: could not resolve the latest release tag for ${REPO}" >&2
    exit 1
  fi
else
  tag="$raw"
fi
# The tag carries a leading v (v1.2.3); goreleaser's {{ .Version }} strips it.
case "$tag" in
  v*) version="${tag#v}" ;;
  *) version="$tag"; tag="v${tag}" ;;
esac

asset=$(rowshape_asset_name "$version" "$OS" "$ARCH")
url="https://github.com/${REPO}/releases/download/${tag}/${asset}"

workdir=$(mktemp -d)
echo "rowshape: downloading ${url}" >&2
if ! curl -fsSL -o "${workdir}/${asset}" "$url"; then
  echo "rowshape: download failed for ${url}" >&2
  exit 1
fi

case "$asset" in
  *.zip) (cd "$workdir" && unzip -q "$asset") ;;
  *.tar.gz) tar -xzf "${workdir}/${asset}" -C "$workdir" ;;
esac

bin="${workdir}/rowshape"
[ "$OS" = windows ] && bin="${workdir}/rowshape.exe"
if [ ! -f "$bin" ]; then
  echo "rowshape: binary not found in ${asset} after extraction" >&2
  exit 1
fi
[ "$OS" != windows ] && chmod +x "$bin"

# Expose the binary to the run step: on PATH, and pinned via ROWSHAPE_BIN so the
# exact downloaded artifact is used even if another rowshape is on PATH.
if [ -n "${GITHUB_PATH:-}" ]; then echo "$workdir" >>"$GITHUB_PATH"; fi
if [ -n "${GITHUB_ENV:-}" ]; then echo "ROWSHAPE_BIN=$bin" >>"$GITHUB_ENV"; fi
echo "rowshape: installed ${tag} at ${bin}" >&2
