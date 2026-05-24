#!/usr/bin/env node
// One-shot, cross-platform "build everything from zero" orchestrator for Atelier.
//
// Why this exists: a full build is otherwise a hand-run sequence (submodule -> protogen ->
// host build -> VM image -> desktop) spread across bash/PowerShell/Make, easy to get wrong on a
// fresh clone, with outputs scattered across services/bin, image/bundle, and apps/desktop. This
// drives the whole chain in order and stages the final artifacts into a single build/<config>/ tree.
//
// Node is the host because it's already required on both dev OSes (engines.node >=22.12) and runs
// identically on macOS and Windows; it branches on process.platform to dispatch the right native
// tools (codesign on macOS, `wsl make` for the Docker-based image build on Windows). Zero deps.
//
// Usage:
//   node scripts/build-all.mjs                      debug (default): clean(light) + build + verify
//   node scripts/build-all.mjs --config=release     strip Go, package desktop, copy image+app into build/release
//   node scripts/build-all.mjs --deep               true from-zero: also wipe node_modules + image/.work
//   node scripts/build-all.mjs --no-verify          build artifacts only
//   node scripts/build-all.mjs --skip-image         fast host-only iteration (skip the heavy Docker image)

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

const isWin = process.platform === 'win32';
const isMac = process.platform === 'darwin';
const exe = isWin ? '.exe' : '';
const target = isMac ? 'darwin-arm64-vz' : 'windows-amd64-hyperv';

// ----- args ---------------------------------------------------------------------------------------

const flags = { config: 'debug', deep: false, verify: true, skipImage: false };
for (const a of process.argv.slice(2)) {
  if (a === '--deep') flags.deep = true;
  else if (a === '--no-verify') flags.verify = false;
  else if (a === '--skip-image') flags.skipImage = true;
  else if (a === '--release' || a === '--config=release') flags.config = 'release';
  else if (a === '--debug' || a === '--config=debug') flags.config = 'debug';
  else if (a === '-h' || a === '--help') {
    const header = fs.readFileSync(fileURLToPath(import.meta.url), 'utf8').split('\n');
    console.log(header.slice(1, 18).map((l) => l.replace(/^\/\/ ?/, '')).join('\n'));
    process.exit(0);
  } else die(`unknown flag: ${a} (try --help)`);
}

// ----- tiny helpers -------------------------------------------------------------------------------

const t0 = Date.now();
const elapsed = () => `${((Date.now() - t0) / 1000).toFixed(0)}s`;

function section(title) {
  console.log(`\n\x1b[1;36m=== ${title} \x1b[0;90m(+${elapsed()})\x1b[0m`);
}
function info(msg) {
  console.log(`\x1b[90m  ${msg}\x1b[0m`);
}
function warn(msg) {
  console.log(`\x1b[33m  ! ${msg}\x1b[0m`);
}
function die(msg) {
  console.error(`\x1b[31mbuild-all: ${msg}\x1b[0m`);
  process.exit(1);
}

// run a command, inheriting stdio; throws on non-zero. `env` is merged onto process.env.
function run(cmd, args, opts = {}) {
  const { env, ...rest } = opts;
  console.log(`\x1b[90m  $ ${[cmd, ...args].join(' ')}\x1b[0m`);
  const res = spawnSync(cmd, args, {
    cwd: repoRoot,
    stdio: 'inherit',
    env: env ? { ...process.env, ...env } : process.env,
    ...rest,
  });
  if (res.error) throw new Error(`could not launch ${cmd}: ${res.error.message}`);
  if (res.status !== 0) throw new Error(`${cmd} exited with ${res.status ?? `signal ${res.signal}`}`);
}

// npm is npm.cmd on Windows, which Node refuses to spawn without a shell.
function npm(args, opts = {}) {
  return isWin ? run('npm.cmd', args, { shell: true, ...opts }) : run('npm', args, opts);
}

// capture stdout, tolerating failure (returns null). Used for best-effort version probes.
function tryCapture(cmd, args, opts = {}) {
  const res = spawnSync(cmd, args, { cwd: repoRoot, encoding: 'utf8', ...opts });
  if (res.error || res.status !== 0) return null;
  return (res.stdout || '').trim();
}

function have(tool) {
  const finder = isWin ? 'where' : 'which';
  const res = spawnSync(finder, [tool], { encoding: 'utf8' });
  return !res.error && res.status === 0;
}

const rel = (...p) => path.join(repoRoot, ...p);
function rm(...p) {
  fs.rmSync(rel(...p), { recursive: true, force: true });
}
function copyInto(src, destDir) {
  fs.mkdirSync(destDir, { recursive: true });
  fs.cpSync(src, path.join(destDir, path.basename(src)), { recursive: true, force: true });
}

// ----- phases -------------------------------------------------------------------------------------

function preflight() {
  section('Preflight');
  if (!isMac && !isWin) die(`unsupported platform '${process.platform}' (darwin or win32 only)`);

  const required = ['node', 'npm', 'go', 'git'];
  if (!flags.skipImage) {
    required.push('docker');
    required.push(isWin ? 'wsl' : 'make');
  }
  if (isMac) required.push('codesign', 'bash');

  const missing = required.filter((t) => !have(t));
  if (missing.length) die(`missing required tool(s): ${missing.join(', ')}`);

  info(`node    ${process.version}`);
  info(`npm     ${tryCapture(isWin ? 'npm.cmd' : 'npm', ['--version'], { shell: isWin }) ?? '?'}`);
  info(`go      ${(tryCapture('go', ['version']) ?? '?').replace(/^go version /, '')}`);
  info(`git     ${(tryCapture('git', ['--version']) ?? '?').replace(/^git version /, '')}`);
  if (!flags.skipImage) {
    info(`docker  ${tryCapture('docker', ['--version']) ?? '?'}`);
    if (tryCapture('docker', ['info', '--format', '{{.ServerVersion}}']) === null)
      die('docker daemon is not reachable (start OrbStack/Docker Desktop, or pass --skip-image)');
  }
  info(`platform ${process.platform} -> config=${flags.config}, target=${target}` +
    `${flags.deep ? ', deep' : ''}${flags.skipImage ? ', skip-image' : ''}${flags.verify ? '' : ', no-verify'}`);
}

function submodule() {
  section('Submodule (patched VZ binding)');
  // services/go.mod sources third_party/vz from a submodule; a fresh clone's Go build fails without this.
  run('git', ['submodule', 'update', '--init', '--recursive']);
}

function clean() {
  section(`Clean (${flags.deep ? 'deep' : 'light'})`);
  // Generated source (imported by module path) + per-component build outputs.
  rm('packages/protocol/src/index.ts');
  rm('services/pkg/protocol/protocol.go');
  for (const b of ['host', 'vmctl', 'guestd']) {
    rm('services/bin', b);
    rm('services/bin', `${b}.exe`);
  }
  rm('apps/desktop/.vite');
  rm('apps/desktop/out');
  rm('build', flags.config);
  // Only drop the VM bundle when we're going to rebuild it — otherwise --skip-image would
  // destroy a working ~4GB artifact. image/.work scratch is kept for a fast rootfs rebuild.
  if (!flags.skipImage) rm('image/bundle', target);
  info(`removed generated code, services/bin, desktop build output, build/${flags.config}` +
    `${flags.skipImage ? '' : `, image/bundle/${target}`}`);

  if (flags.deep) {
    for (const d of ['', 'apps/desktop', 'packages/agent', 'packages/provider', 'packages/protocol', 'tools/protogen'])
      rm(d, 'node_modules');
    info('removed all node_modules');
    if (!flags.skipImage) {
      // Wipe Docker scratch so the rootfs is fully re-exported (Docker layer cache still persists).
      if (isWin) run('wsl', ['make', '-C', 'image', 'clean', `TARGET=${target}`]);
      else run('make', ['-C', 'image', 'clean', `TARGET=${target}`]);
      info('wiped image/.work (full rootfs re-export on next build)');
    }
  }
}

function protogen() {
  section('Protocol codegen');
  npm(['run', 'protogen']);
}

function hostBuild() {
  section(`Host binaries (${flags.config})`);
  // Release strips symbols and trims paths; debug keeps them. The per-OS scripts read these envs.
  const env =
    flags.config === 'release' ? { ATELIER_GOFLAGS: '-trimpath', ATELIER_LDFLAGS: '-s -w' } : {};
  if (isMac) run('bash', ['scripts/build-sign-darwin.sh'], { env });
  else run('powershell', ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', 'scripts/build-go.ps1'], { env });
}

function imageBuild() {
  if (flags.skipImage) {
    section('VM image (skipped)');
    return;
  }
  section('VM image bundle');
  if (isMac) run('make', ['-C', 'image', 'darwin']);
  else {
    warn('Windows image build runs under WSL2 and is not verified from this repo author\'s macOS machine');
    run('wsl', ['make', '-C', 'image', 'windows']);
  }
}

function desktop() {
  section('JS dependencies');
  npm(['--prefix', 'apps/desktop', 'install']);
  npm(['--prefix', 'packages/agent', 'install']); // for the verify phase; runtime deps ship baked in the rootfs
  if (flags.config === 'release') {
    section('Desktop package');
    npm(['--prefix', 'apps/desktop', 'run', 'package']);
  }
}

function stage() {
  section(`Stage -> build/${flags.config}`);
  const dest = rel('build', flags.config);
  fs.mkdirSync(dest, { recursive: true });
  copyInto(rel('services/bin', `host${exe}`), dest);
  copyInto(rel('services/bin', `vmctl${exe}`), dest);
  info(`host${exe}, vmctl${exe}`);
  if (flags.config === 'release') {
    if (!flags.skipImage) {
      copyInto(rel('image/bundle', target), path.join(dest, 'image'));
      info(`image/${target}/ (copied bundle)`);
    } else {
      warn('release with --skip-image: VM bundle not staged');
    }
    // electron-forge writes to apps/desktop/out. Don't fail the whole build if packaging
    // produced nothing (a known finalize quirk surfaces here) — warn and stage the rest.
    const out = rel('apps/desktop/out');
    if (fs.existsSync(out)) {
      const desktopDir = path.join(dest, 'desktop');
      fs.rmSync(desktopDir, { recursive: true, force: true });
      fs.cpSync(out, desktopDir, { recursive: true, force: true });
      info('desktop/ (packaged app)');
    } else {
      warn('apps/desktop/out missing — desktop package not staged (check `npm --prefix apps/desktop run package`)');
    }
  }
}

function verify() {
  if (!flags.verify) {
    section('Verify (skipped)');
    return;
  }
  section('Verify: Go');
  run('go', ['-C', 'services', 'vet', './...']);
  run('go', ['-C', 'services', 'test', './...']);
  const unformatted = tryCapture('gofmt', ['-l', 'services']);
  if (unformatted) warn(`gofmt -l flagged:\n${unformatted}`);
  run('go', ['-C', 'services', 'build', './...'], { env: { GOOS: 'windows', CGO_ENABLED: '0' } });
  // guestd is cross-compiled into the rootfs by the image build, so the host builds above only
  // exercise its !linux stubs. Compile it for its real linux target too, so a break in
  // egress/mount/sandbox _linux.go is caught here even when --skip-image is set.
  const guestArch = isMac ? 'arm64' : 'amd64';
  const probe = path.join(os.tmpdir(), `atelier-guestd-probe-${process.pid}`);
  run('go', ['-C', 'services', 'build', '-o', probe, './cmd/guestd'],
    { env: { GOOS: 'linux', GOARCH: guestArch, CGO_ENABLED: '0' } });
  fs.rmSync(probe, { force: true });

  section('Verify: desktop');
  npm(['--prefix', 'apps/desktop', 'run', 'typecheck']);
  npm(['--prefix', 'apps/desktop', 'run', 'lint']);
  npm(['--prefix', 'apps/desktop', 'run', 'test']);

  section('Verify: agent');
  npm(['--prefix', 'packages/agent', 'run', 'typecheck']);
  npm(['--prefix', 'packages/agent', 'run', 'test']);
}

function summary() {
  section(`Done (${flags.config}) in ${elapsed()}`);
  const destAbs = rel('build', flags.config);
  const staged = fs.readdirSync(destAbs).sort();
  console.log(`  build/${flags.config}/ contains: ${staged.join(', ')}`);
  if (flags.config === 'release') {
    console.log('  release/ is the self-contained deliverable');
  } else {
    console.log('  next:');
    console.log(`    sudo build/${flags.config}/host -addr /tmp/atelier-host.sock   # elevated broker`);
    console.log(`    ATELIER_BUNDLE_DIR=image/bundle/${target} npm run dev          # desktop (dev)`);
  }
}

// ----- main ---------------------------------------------------------------------------------------

try {
  preflight();
  submodule();
  clean();
  protogen();
  hostBuild();
  imageBuild();
  desktop();
  stage();
  verify();
  summary();
} catch (err) {
  console.error(`\n\x1b[31mbuild-all failed after ${elapsed()}: ${err.message}\x1b[0m`);
  process.exit(1);
}
