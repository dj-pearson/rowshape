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
const PLATFORM = { darwin: "Darwin", linux: "Linux", win32: "Windows" }[process.platform];
const ARCH = { x64: "x86_64", arm64: "arm64", amd64: "x86_64" }[process.arch];

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

const ext = process.platform === "win32" ? "zip" : "tar.gz";
const asset = `rowshape_${VERSION}_${PLATFORM}_${ARCH}.${ext}`;
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
