// postinstall: download the platform-appropriate rowshape binary from the
// matching GitHub Release into ./bin so `npx rowshape` runs the native binary.
// This is the answer to the "why not pure npm" objection (PRD §7): npm is a
// delivery channel for the single static Go binary, not a reimplementation.
"use strict";

const fs = require("fs");
const path = require("path");
const https = require("https");
const zlib = require("zlib");
const { execSync } = require("child_process");

const REPO = "rowshape/rowshape";
const VERSION = require("./package.json").version;

// Map Node's platform/arch onto goreleaser's archive naming.
//
// These MUST match .goreleaser.yaml's archives.name_template, which is
// `{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}` — raw lowercase GOOS
// and GOARCH. This file previously used the older goreleaser convention
// (title-cased OS, amd64 rewritten to x86_64) and asked for
// `rowshape_1.0.0_Darwin_x86_64.tar.gz` where the release actually publishes
// `rowshape_1.0.0_darwin_amd64.tar.gz`. Every install would have 404'd, and
// nothing could catch it before the first real release.
const PLATFORM = { darwin: "darwin", linux: "linux", win32: "windows" }[process.platform];
const ARCH = { x64: "amd64", arm64: "arm64" }[process.arch];

function fail(msg) {
  console.error(`rowshape: ${msg}`);
  console.error(
    "Install a binary directly from https://github.com/rowshape/rowshape/releases " +
      "or `go install github.com/rowshape/rowshape@latest`."
  );
  process.exit(1);
}

if (!PLATFORM || !ARCH) {
  fail(`unsupported platform ${process.platform}/${process.arch}`);
}
// The release builds 5 combos: darwin/linux on amd64+arm64, windows on amd64.
// goreleaser explicitly ignores windows/arm64, so there is no asset to fetch —
// say that plainly rather than reporting a confusing 404.
if (PLATFORM === "windows" && ARCH === "arm64") {
  fail("windows/arm64 is not a released target");
}

// assetName mirrors .goreleaser.yaml archives.name_template exactly:
//   {{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}
// with the windows format_override to zip.
function assetName(version, platform, arch) {
  const ext = platform === "windows" ? "zip" : "tar.gz";
  return `rowshape_${version}_${platform}_${arch}.${ext}`;
}

const ext = process.platform === "win32" ? "zip" : "tar.gz";
const asset = assetName(VERSION, PLATFORM, ARCH);
const url = `https://github.com/${REPO}/releases/download/v${VERSION}/${asset}`;
const binName = process.platform === "win32" ? "rowshape.exe" : "rowshape";
const binDir = path.join(__dirname, "bin");

function get(u, cb) {
  https
    .get(u, { headers: { "User-Agent": "rowshape-npm-installer" } }, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        return get(res.headers.location, cb);
      }
      if (res.statusCode !== 200) {
        return fail(`download failed (${res.statusCode}) for ${u}`);
      }
      cb(res);
    })
    .on("error", (e) => fail(`network error: ${e.message}`));
}

// Exported so the naming can be checked against what goreleaser actually
// publishes (npm/naming.test.js). Requiring this file must not download
// anything — the postinstall hook runs it directly.
module.exports = { assetName, PLATFORM, ARCH };
if (require.main !== module) return;

fs.mkdirSync(binDir, { recursive: true });
const archivePath = path.join(binDir, asset);

get(url, (res) => {
  const out = fs.createWriteStream(archivePath);
  res.pipe(out);
  out.on("finish", () => {
    out.close(() => {
      try {
        if (ext === "zip") {
          // Rely on the system unzip / tar (tar handles zip on modern Windows).
          execSync(`tar -xf "${archivePath}" -C "${binDir}"`);
        } else {
          const tar = fs.readFileSync(archivePath);
          const tarballPath = path.join(binDir, "rowshape.tar");
          fs.writeFileSync(tarballPath, zlib.gunzipSync(tar));
          execSync(`tar -xf "${tarballPath}" -C "${binDir}"`);
          fs.unlinkSync(tarballPath);
        }
        fs.unlinkSync(archivePath);
        const bin = path.join(binDir, binName);
        if (!fs.existsSync(bin)) fail("binary not found after extraction");
        if (process.platform !== "win32") fs.chmodSync(bin, 0o755);
      } catch (e) {
        fail(`extraction failed: ${e.message}`);
      }
    });
  });
});
