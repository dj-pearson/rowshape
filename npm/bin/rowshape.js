#!/usr/bin/env node
// Thin shim: exec the native rowshape binary fetched by install.js, forwarding
// argv and the exit code (the exit code is part of rowshape's public contract).
"use strict";

const path = require("path");
const { spawnSync } = require("child_process");

const binName = process.platform === "win32" ? "rowshape.exe" : "rowshape";
const bin = path.join(__dirname, binName);

const result = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });
if (result.error) {
  console.error(`rowshape: failed to launch native binary: ${result.error.message}`);
  process.exit(3);
}
process.exit(result.status === null ? 3 : result.status);
