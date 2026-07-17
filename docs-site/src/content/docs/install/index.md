---
title: Install
description: Install the rowshape CLI — a single static binary, no runtime.
sidebar:
  order: 1
---

rowshape is a single static Go binary. There is no runtime to install and no
services to run. Pick whichever channel fits your environment — they all deliver
the same binary.

## Homebrew (macOS, Linux)

```sh
brew install rowshape/tap/rowshape
rowshape --help
```

## go install

```sh
go install github.com/rowshape/rowshape@latest
```

Installs into `$(go env GOPATH)/bin`; make sure that is on your `PATH`.

## npm wrapper (npx)

The npm package is a thin wrapper: on install it downloads the matching native
binary from the GitHub Release, so `npx` runs the real Go binary — not a
reimplementation.

```sh
npx rowshape --help
# or add it to a project:
npm install --save-dev rowshape
```

## Direct download (GitHub Releases)

Every release publishes binaries for macOS, Linux, and Windows on both amd64 and
arm64. Download the archive for your platform, verify it, and drop the binary on
your `PATH`:

```sh
curl -sSL -o rowshape.tar.gz \
  https://github.com/rowshape/rowshape/releases/latest/download/rowshape_<version>_<os>_<arch>.tar.gz
tar -xzf rowshape.tar.gz
./rowshape --help
```

## Docker (CI)

A `FROM scratch` image ships for use in CI pipelines:

```sh
docker run --rm ghcr.io/rowshape/rowshape:latest --help
```

The `rowshape/rowshape` GitHub Action wraps this for you — see the
[GitHub Action guide](../agent/) and the finding catalog for what it reports.

## Supply chain

Every release ships an [SBOM](https://en.wikipedia.org/wiki/Software_supply_chain)
(SPDX, one per archive) and a cosign signature. You can verify a downloaded
artifact against its signature before trusting it:

```sh
cosign verify-blob \
  --certificate rowshape_<version>_checksums.txt.pem \
  --signature   rowshape_<version>_checksums.txt.sig \
  rowshape_<version>_checksums.txt
```

The binary is a single static executable with a deliberately small dependency
set — half the reason rowshape is written in Go.
