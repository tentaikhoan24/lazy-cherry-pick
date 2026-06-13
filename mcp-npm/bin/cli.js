#!/usr/bin/env node
'use strict';

const fs = require('fs');
const path = require('path');
const os = require('os');
const https = require('https');
const crypto = require('crypto');
const { spawnSync } = require('child_process');

const pkg = require('../package.json');

const REPO = 'tentaikhoan24/lazy-cherry-pick';

// process.platform-process.arch -> release asset name (target-triple suffix)
const ASSET_BY_PLATFORM = {
  'win32-x64': 'sidecar-x86_64-pc-windows-msvc.exe',
};

function assetName() {
  return ASSET_BY_PLATFORM[`${process.platform}-${process.arch}`];
}

function cacheDir() {
  const base = process.platform === 'win32'
    ? (process.env.LOCALAPPDATA || os.homedir())
    : (process.env.XDG_CACHE_HOME || path.join(os.homedir(), '.cache'));
  return path.join(base, 'lazy-cherry-pick-mcp', `v${pkg.version}`);
}

function sha256File(file) {
  return new Promise((resolve, reject) => {
    const hash = crypto.createHash('sha256');
    const stream = fs.createReadStream(file);
    stream.on('data', (chunk) => hash.update(chunk));
    stream.on('error', reject);
    stream.on('end', () => resolve(hash.digest('hex')));
  });
}

function download(url, dest, redirectsLeft) {
  if (redirectsLeft === undefined) redirectsLeft = 5;
  return new Promise((resolve, reject) => {
    const req = https.get(url, { headers: { 'User-Agent': 'lazy-cherry-pick-mcp' } }, (res) => {
      const { statusCode, headers } = res;
      if (statusCode >= 300 && statusCode < 400 && headers.location) {
        res.resume();
        if (redirectsLeft <= 0) return reject(new Error('too many redirects'));
        return download(headers.location, dest, redirectsLeft - 1).then(resolve, reject);
      }
      if (statusCode !== 200) {
        res.resume();
        return reject(new Error(`HTTP ${statusCode} fetching ${url}`));
      }
      const file = fs.createWriteStream(dest);
      res.pipe(file);
      file.on('finish', () => file.close((err) => (err ? reject(err) : resolve())));
      file.on('error', reject);
    });
    req.on('error', reject);
  });
}

async function ensureBinary() {
  const asset = assetName();
  if (!asset) {
    throw new Error(
      `unsupported platform ${process.platform}/${process.arch} — only Windows x64 is published today. ` +
      `Build the sidecar from source: https://github.com/${REPO}#mcp-server--ai-integration`
    );
  }

  const dir = cacheDir();
  const dest = path.join(dir, asset);
  if (fs.existsSync(dest)) return dest;

  fs.mkdirSync(dir, { recursive: true });
  const url = `https://github.com/${REPO}/releases/download/v${pkg.version}/${asset}`;
  const tmp = `${dest}.${process.pid}.tmp`;
  process.stderr.write(`lazy-cherry-pick-mcp: downloading sidecar binary (v${pkg.version})...\n`);
  try {
    await download(url, tmp);
  } catch (err) {
    try { fs.unlinkSync(tmp); } catch {}
    throw new Error(`failed to download ${url}: ${err.message}`);
  }

  if (pkg.sidecarSha256) {
    const expected = pkg.sidecarSha256.toLowerCase();
    const actual = await sha256File(tmp);
    if (actual !== expected) {
      try { fs.unlinkSync(tmp); } catch {}
      throw new Error(
        `checksum mismatch for ${url}\n` +
        `  expected: ${expected}\n` +
        `  actual:   ${actual}\n` +
        `refusing to run an unverified binary`
      );
    }
  } else {
    process.stderr.write(`lazy-cherry-pick-mcp: warning: no pinned checksum for v${pkg.version}, skipping integrity check\n`);
  }

  fs.renameSync(tmp, dest);
  return dest;
}

(async () => {
  let bin;
  try {
    bin = await ensureBinary();
  } catch (err) {
    process.stderr.write(`lazy-cherry-pick-mcp: ${err.message}\n`);
    process.exit(1);
  }

  const result = spawnSync(bin, ['--mcp'], { stdio: 'inherit' });
  if (result.error) {
    process.stderr.write(`lazy-cherry-pick-mcp: failed to launch sidecar: ${result.error.message}\n`);
    process.exit(1);
  }
  process.exit(result.status === null ? 1 : result.status);
})();
