#!/usr/bin/env node
// One-shot, cross-platform "build everything from zero" orchestrator for Atelier.
//
// Why this exists: a full build is otherwise a hand-run sequence (submodule -> protogen ->
// host build -> VM image -> desktop) spread across bash/PowerShell/Make, easy to get wrong on a
// fresh clone, with outputs scattered across services/bin, image/bundle, and apps/desktop. This
// file is the SINGLE source of truth for the build: it drives the whole chain in order and writes
// every final artifact into one tree, build/<config>/.
//
// Node is the host because it's already required on both dev OSes (engines.node >=22.12) and runs
// identically on macOS and Windows; it branches on process.platform for the irreducibly
// platform-specific bits (codesign on macOS — VZ refuses to boot an unsigned broker; the `wsl`
// prefix for the Docker-based image build on Windows). The Go host build + codesign live here
// directly (no per-OS shell script); only the image build stays in image/build.sh (bash + Docker,
// run natively on macOS and via wsl on Windows — one cross-OS script, not a duplicated pair).
// Zero deps.
//
// Usage:
//   node scripts/build-all.mjs                      debug (default): clean(light) + build all + verify
//   node scripts/build-all.mjs --config=release     strip Go symbols, self-contained build/release
//   node scripts/build-all.mjs --only=host          just protogen + host binaries (+codesign on mac)
//   node scripts/build-all.mjs --only=image         just the VM image bundle
//   node scripts/build-all.mjs --only=desktop       just the packaged desktop app
//   node scripts/build-all.mjs --deep               true from-zero: also wipe node_modules + image/.work
//   node scripts/build-all.mjs --no-verify          build artifacts only
//   node scripts/build-all.mjs --skip-image         fast host-only iteration (skip the heavy Docker image)
//
// All artifacts land in build/<config>/: host(.exe), vmctl(.exe), image/<target>/, desktop/.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

const isWin = process.platform === 'win32';
const isMac = process.platform === 'darwin';
const exe = isWin ? '.exe' : '';
const target = isMac ? 'darwin-arm64-vz' : 'windows-amd64-hyperv';

// ----- args ---------------------------------------------------------------------------------------

const flags = { config: 'debug', deep: false, verify: true, skipImage: false, only: null };
const phases = ['host', 'image', 'desktop'];
for (const a of process.argv.slice(2)) {
  if (a === '--deep') flags.deep = true;
  else if (a === '--no-verify') flags.verify = false;
  else if (a === '--skip-image') flags.skipImage = true;
  else if (a === '--release' || a === '--config=release') flags.config = 'release';
  else if (a === '--debug' || a === '--config=debug') flags.config = 'debug';
  else if (a.startsWith('--only=')) {
    flags.only = a.slice('--only='.length);
    if (!phases.includes(flags.only)) die(`--only must be one of: ${phases.join(', ')}`);
  } else if (a === '-h' || a === '--help') {
    const header = fs.readFileSync(fileURLToPath(import.meta.url), 'utf8').split('\n');
    console.log(header.slice(1, 28).map((l) => l.replace(/^\/\/ ?/, '')).join('\n'));
    process.exit(0);
  } else die(`unknown flag: ${a} (try --help)`);
}

// Which phases run this invocation. --only narrows to one; otherwise all three. The image phase is
// additionally gated by --skip-image.
const want = (p) => !flags.only || flags.only === p;
const buildImage = want('image') && !flags.skipImage;

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

// Run an image/build.sh subcommand with the output base redirected into build/<config>/image. The
// path is RELATIVE to the image/ dir (../build/...) so it resolves identically on macOS and inside
// WSL — no Windows<->WSL path translation. On Windows the env is passed inline to `wsl bash -lc`
// (WSLENV is finicky); on macOS it's a plain env on the bash child.
const imageOutBase = path.posix.join('..', 'build', flags.config, 'image');
function imageRun(subcmd) {
  if (isWin) {
    run('wsl', ['bash', '-lc', `cd image && TARGET=${target} ATELIER_OUT_BASE='${imageOutBase}' ./build.sh ${subcmd}`]);
  } else {
    run('bash', ['build.sh', subcmd], { cwd: rel('image'), env: { TARGET: target, ATELIER_OUT_BASE: imageOutBase } });
  }
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

// ----- phases -------------------------------------------------------------------------------------

function preflight() {
  section('Preflight');
  if (!isMac && !isWin) die(`unsupported platform '${process.platform}' (darwin or win32 only)`);

  const required = ['node', 'npm', 'go', 'git'];
  if (buildImage) {
    required.push('docker');
    required.push(isWin ? 'wsl' : 'bash'); // image build is bash + Docker (via wsl on Windows); no make
  }
  if (isMac && want('host')) required.push('codesign'); // ad-hoc sign the broker for the VZ entitlement

  const missing = required.filter((t) => !have(t));
  if (missing.length) die(`missing required tool(s): ${missing.join(', ')}`);

  info(`node    ${process.version}`);
  info(`npm     ${tryCapture(isWin ? 'npm.cmd' : 'npm', ['--version'], { shell: isWin }) ?? '?'}`);
  info(`go      ${(tryCapture('go', ['version']) ?? '?').replace(/^go version /, '')}`);
  info(`git     ${(tryCapture('git', ['--version']) ?? '?').replace(/^git version /, '')}`);
  if (buildImage) {
    info(`docker  ${tryCapture('docker', ['--version']) ?? '?'}`);
    if (tryCapture('docker', ['info', '--format', '{{.ServerVersion}}']) === null)
      die('docker daemon is not reachable (start OrbStack/Docker Desktop, or pass --skip-image)');
  }
  info(`platform ${process.platform} -> config=${flags.config}, target=${target}, ` +
    `phases=${flags.only ?? 'all'}${flags.deep ? ', deep' : ''}${flags.skipImage ? ', skip-image' : ''}${flags.verify ? '' : ', no-verify'}`);
}

function submodule() {
  section('Submodule (patched VZ binding)');
  // services/go.mod sources third_party/vz from a submodule; a fresh clone's Go build fails without this.
  run('git', ['submodule', 'update', '--init', '--recursive']);
}

function clean() {
  section(`Clean (${flags.deep ? 'deep' : 'light'})`);
  // Generated source (imported by module path) is always regenerated by protogen below.
  rm('packages/protocol/src/index.ts');
  rm('services/pkg/protocol/protocol.go');
  // Drop any stale services/bin from older or manual builds — the orchestrator now writes the host
  // binaries straight into build/<config>/, so services/bin should not shadow them.
  for (const b of ['host', 'vmctl', 'guestd']) {
    rm('services/bin', b);
    rm('services/bin', `${b}.exe`);
  }

  // Only remove the build/<config>/ subtrees the running phases will rebuild — so --only and
  // --skip-image never destroy a sibling artifact (e.g. the ~4GB image bundle on a host-only run).
  const cleaned = [];
  if (want('host')) {
    rm('build', flags.config, `host${exe}`);
    rm('build', flags.config, `vmctl${exe}`);
    cleaned.push(`host${exe},vmctl${exe}`);
  }
  if (want('desktop')) {
    rm('apps/desktop/.vite');
    rm('apps/desktop/out');
    rm('build', flags.config, 'desktop');
    cleaned.push('desktop/');
  }
  if (buildImage) {
    rm('build', flags.config, 'image', target);
    cleaned.push(`image/${target}/`);
  }
  info(`removed generated code, services/bin, build/${flags.config}/{${cleaned.join(', ')}}`);

  if (flags.deep) {
    for (const d of ['', 'apps/desktop', 'packages/agent', 'packages/provider', 'packages/protocol', 'tools/protogen'])
      rm(d, 'node_modules');
    info('removed all node_modules');
    if (buildImage) {
      // Wipe Docker scratch (.work/<target>) so the rootfs is fully re-exported (the Docker layer
      // cache still persists). build.sh clean removes $WORK and $OUT (build/<config>/image/<target>).
      imageRun('clean');
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
  const dest = rel('build', flags.config);
  fs.mkdirSync(dest, { recursive: true });

  // host instantiates the VM via cgo (VZ on macOS); CGO_ENABLED=1 is required. Release strips
  // symbols + trims paths; debug keeps them. `-o <dir>` writes both binaries into build/<config>/.
  const args = ['-C', 'services', 'build'];
  if (flags.config === 'release') args.push('-trimpath', '-ldflags=-s -w');
  args.push('-o', dest, './cmd/host', './cmd/vmctl');
  run('go', args, { env: { CGO_ENABLED: '1' } });
  info(`host${exe}, vmctl${exe} -> build/${flags.config}/`);

  if (isMac) {
    // Virtualization.framework refuses to run unless the broker is codesigned with
    // com.apple.security.virtualization under the hardened runtime, and cgo invalidates the
    // signature on every rebuild — so (re)sign here. vmctl is a plain RPC client; no entitlement.
    section('Codesign broker (virtualization entitlement)');
    const entitlements = rel('services/packaging/darwin/atelier-vm.entitlements');
    const host = path.join(dest, 'host');
    run('codesign', ['--force', '--sign', '-', '--options', 'runtime', '--entitlements', entitlements, host]);
    run('codesign', ['--display', '--entitlements', '-', host]);
  }
}

function imageBuild() {
  if (flags.skipImage) {
    section('VM image (skipped)');
    return;
  }
  section('VM image bundle');
  if (isWin)
    warn("Windows image build runs under WSL2 and is not verified from this repo author's macOS machine");
  imageRun('all'); // -> build/<config>/image/<target>/{vmlinuz,initrd,rootfs.*,*.origin,manifest.txt}
  info(`image/${target}/ -> build/${flags.config}/image/${target}/`);
}

// Text of the dev launcher staged at build/<config>/run.sh. It points the packaged app at THIS
// tree's VM bundle (ATELIER_BUNDLE_DIR wins over the app's packaged Resources/bundle default) and
// execs it. macOS-only for now: the .app path layout below is mac-specific. The broker is started
// separately by the developer (see summary).
function desktopLauncher() {
  return `#!/usr/bin/env bash
# Launch the packaged Atelier desktop app against the VM image bundle in this build tree.
# Generated by scripts/build-all.mjs — re-run \`npm run build:all\` to refresh; don't hand-edit.
# Start the broker yourself first, e.g.:  ./host -addr /tmp/atelier-host.sock
set -euo pipefail

HERE="$(cd "$(dirname "\${BASH_SOURCE[0]}")" && pwd)"
BUNDLE="$HERE/image/${target}"

if [ ! -f "$BUNDLE/vmlinuz" ]; then
  echo "atelier: VM image not found at $BUNDLE — run: npm run build:all" >&2
  exit 1
fi

APP="$(/bin/ls -d "$HERE"/desktop/*/Atelier.app 2>/dev/null | head -n1 || true)"
if [ -z "$APP" ] || [ ! -x "$APP/Contents/MacOS/Atelier" ]; then
  echo "atelier: packaged app not found under $HERE/desktop — run: npm run build:all" >&2
  exit 1
fi

export ATELIER_BUNDLE_DIR="$BUNDLE"
exec "$APP/Contents/MacOS/Atelier" "$@"
`;
}

function desktop() {
  section('JS dependencies');
  npm(['--prefix', 'apps/desktop', 'install']);
  npm(['--prefix', 'packages/agent', 'install']); // for the verify phase; runtime deps ship baked in the rootfs

  // Package the Electron app for both configs so build/<config>/desktop/ is runnable. Debug pays the
  // Forge packaging cost too; for fast host-only iteration, run the desktop via `npm run dev`.
  section('Desktop package');
  npm(['--prefix', 'apps/desktop', 'run', 'package']);

  // electron-forge writes to apps/desktop/out (its own scratch). Stage the final app into
  // build/<config>/desktop/. Don't fail the whole build if packaging produced nothing (a known
  // finalize quirk surfaces here) — warn and move on.
  section(`Stage desktop -> build/${flags.config}/desktop`);
  const out = rel('apps/desktop/out');
  if (fs.existsSync(out)) {
    const desktopDir = rel('build', flags.config, 'desktop');
    fs.rmSync(desktopDir, { recursive: true, force: true });
    fs.mkdirSync(path.dirname(desktopDir), { recursive: true });
    // verbatimSymlinks is required: the .app's Electron Framework uses relative symlinks
    // (Resources -> Versions/Current/Resources). Without it, cpSync rewrites them to absolute
    // paths back into apps/desktop/out/, which (a) makes build/ non-self-contained — the next
    // build deletes out/ and the links dangle — and (b) breaks Forge's ad-hoc code signature
    // (the sealed symlink bytes change), so the hardened runtime rejects the framework load
    // ("icudtl.dat not found in bundle" -> GPU/network helpers die on launch).
    fs.cpSync(out, desktopDir, { recursive: true, force: true, verbatimSymlinks: true });
    info('desktop/ (packaged app)');
  } else {
    warn('apps/desktop/out missing — desktop package not staged (check `npm --prefix apps/desktop run package`)');
  }

  // Stage a self-contained dev launcher next to host/vmctl/desktop/image so the workflow is just
  // `cd build/<config> && ./run.sh`. It self-validates the tree and errors clearly if a phase is
  // missing (e.g. ran with --skip-image), so it's safe to write whenever the desktop phase runs.
  if (isMac) {
    const launcher = rel('build', flags.config, 'run.sh');
    fs.mkdirSync(path.dirname(launcher), { recursive: true });
    fs.writeFileSync(launcher, desktopLauncher());
    fs.chmodSync(launcher, 0o755); // writeFileSync mode is masked by umask; force it executable
    info(`run.sh -> launches packaged desktop against image/${target}`);
  }
}

function verify() {
  if (flags.only) {
    section(`Verify (skipped: --only=${flags.only})`);
    return;
  }
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
  const probe = rel('build', flags.config, `.guestd-probe-${process.pid}`);
  fs.mkdirSync(path.dirname(probe), { recursive: true });
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
  section(`Done (${flags.config}${flags.only ? `, only=${flags.only}` : ''}) in ${elapsed()}`);
  const destAbs = rel('build', flags.config);
  const staged = fs.existsSync(destAbs) ? fs.readdirSync(destAbs).sort() : [];
  console.log(`  build/${flags.config}/ contains: ${staged.join(', ') || '(nothing)'}`);
  if (flags.config === 'release') {
    console.log('  release/ is the self-contained deliverable');
    return;
  }
  console.log('  next:');
  // macOS drives VZ via a codesigned binary with the virtualization entitlement — no root needed.
  // Only the Windows/HCS broker must run elevated.
  if (isWin) {
    console.log(`    build\\${flags.config}\\host.exe -addr \\\\.\\pipe\\atelier-host   # broker (run elevated)`);
  } else {
    console.log(`    build/${flags.config}/host -addr /tmp/atelier-host.sock              # broker`);
  }
  console.log(`    ATELIER_BUNDLE_DIR=build/${flags.config}/image/${target} npm run dev   # desktop (dev)`);
  if (isMac)
    console.log(`    ( cd build/${flags.config} && ./run.sh )                                  # desktop (packaged)`);
}

// ----- main ---------------------------------------------------------------------------------------

try {
  preflight();
  if (want('host')) submodule();
  clean();
  protogen();
  if (want('host')) hostBuild();
  if (want('image')) imageBuild();
  if (want('desktop')) desktop();
  verify();
  summary();
} catch (err) {
  console.error(`\n\x1b[31mbuild-all failed after ${elapsed()}: ${err.message}\x1b[0m`);
  process.exit(1);
}
