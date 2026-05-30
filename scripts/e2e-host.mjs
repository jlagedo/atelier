#!/usr/bin/env node
// End-to-end battery for the host broker (`npm run e2e:host`).
//
// Why this exists: unit tests cover the broker doors against fake drivers (services/internal/...),
// and the S7 probe (services/internal/vmm/s7_probe_darwin_test.go) proves the runtime-share shape.
// Neither boots the *shipped* broker and
// drives every door the way the product does. This harness does: it builds (or reuses) build/<config>/,
// spawns the real `host` over a unix socket, boots a real VM via VZ, and exercises all 12 protocol
// doors + the in-guest agent loop end to end through the `atelierctl` dev CLI — the same Hop-2 wire the
// desktop app uses.
//
// It mirrors build-all.mjs: zero-dep Node, the same logging helpers, --config/--skip-build flags,
// and the same artifact tree (build/<config>/). Tests run to completion and tally pass/fail rather
// than aborting on the first failure (a battery, not a build), exiting non-zero if any door fails.
//
// Coverage (a real boot, so VZ + a codesigned broker + the image bundle are required):
//   getStatus  createVM  startVM  setTime  exec  execInput  attachWorkspace  detachWorkspace
//   readFile   writeFile setEgressPolicy  stopVM   + the guest agent loop (Topology B: one-shot + serve)
// It also checks the in-guest sandbox seccomp filter (F-13): a non-privileged exec runs under
// Seccomp: 2, and unshare(CLONE_NEWUSER) is EPERM-denied (F-01 closed).
// The Files door is host-side and jailed to the legacy /workspace root, while the same folder is
// exposed to guest exec over the fs share (virtio-fs on VZ, 9p on HCS) — so we prove the
// host<->guest bridge in both directions. egress is
// proved both ways: a denied host fails (default-deny), and the agent's real model call succeeds
// through the jail.
//
// Usage:
//   node scripts/e2e-host.mjs                  debug (default): build host+image if missing, then test
//   node scripts/e2e-host.mjs --config=release run against build/release/
//   node scripts/e2e-host.mjs --skip-build     reuse build/<config>/ as-is (fail fast if incomplete)
//   node scripts/e2e-host.mjs --rebuild-image  force a guest-bundle rebuild (after guest changes)
//
// All console + build-subprocess output is mirrored (ANSI-stripped) to a timestamped
// build/<config>/e2e-host-<ts>.log; the broker's own stdout/stderr go to e2e-host-<ts>.broker.log,
// and the guest's serial console (kernel boot + init) is distilled into e2e-host-<ts>.console.log,
// all beside each other.
//
// The agent door needs ANTHROPIC_API_KEY in this shell's environment and live egress to
// api.anthropic.com; it fails the suite if the key is absent (per design: the battery covers it).

import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

const isWin = process.platform === 'win32';
const isMac = process.platform === 'darwin';
const exe = isWin ? '.exe' : '';
const target = isMac ? 'darwin-arm64-vz' : 'windows-amd64-hyperv';
const rootfsName = isMac ? 'rootfs.raw' : 'rootfs.vhd';
const runnerName = isMac ? 'runner.raw' : 'runner.vhd'; // guest payload volume: runner + agent (not baked into rootfs)

// ----- args ---------------------------------------------------------------------------------------

const flags = { config: 'debug', skipBuild: false, rebuildImage: false };
for (const a of process.argv.slice(2)) {
  if (a === '--release' || a === '--config=release') flags.config = 'release';
  else if (a === '--debug' || a === '--config=debug') flags.config = 'debug';
  else if (a === '--skip-build') flags.skipBuild = true;
  else if (a === '--rebuild-image') flags.rebuildImage = true;
  else if (a === '-h' || a === '--help') {
    const header = fs.readFileSync(fileURLToPath(import.meta.url), 'utf8').split('\n');
    console.log(header.slice(1, 30).map((l) => l.replace(/^\/\/ ?/, '')).join('\n'));
    process.exit(0);
  } else die(`unknown flag: ${a} (try --help)`);
}

const config = flags.config;
// Named pipe on Windows, unix socket elsewhere (matches the rpc.DefaultAddress scheme).
const ADDR = isWin ? '\\\\.\\pipe\\atelierd-e2e' : '/tmp/atelierd-e2e.sock';
// Both logs land in build/<config>/, sharing one filename-safe timestamp so a run's pair is obvious.
const stamp = new Date().toISOString().replace(/:/g, '-').replace(/\..+$/, ''); // 2026-05-24T15-30-45
const LOG_FILE = rel('build', config, `e2e-host-${stamp}.log`);
const BROKER_LOG = rel('build', config, `e2e-host-${stamp}.broker.log`);
const CONSOLE_LOG = rel('build', config, `e2e-host-${stamp}.console.log`); // distilled guest kernel/console
const HOST = rel('build', config, `atelierd${exe}`);
const VMCTL = rel('build', config, `atelierctl${exe}`);
const BUNDLE = rel('build', config, 'image', target);

// ----- tee all stdout/stderr to the timestamped log ----------------------------------------------
// Everything this script prints (and the build subprocesses' streamed output) is mirrored, with ANSI
// stripped, to LOG_FILE. fs.writeSync (not a WriteStream) keeps the log intact across the script's
// many process.exit() paths, which would otherwise drop unflushed buffers.
fs.mkdirSync(path.dirname(LOG_FILE), { recursive: true });
const logFd = fs.openSync(LOG_FILE, 'a');
const stripAnsi = (s) => s.replace(/\x1b\[[0-9;]*m/g, '');
function teeStream(raw) {
  return (chunk, enc, cb) => {
    if (typeof enc === 'function') {
      cb = enc;
      enc = undefined;
    }
    const s = typeof chunk === 'string' ? chunk : Buffer.from(chunk).toString('utf8');
    try {
      fs.writeSync(logFd, stripAnsi(s));
    } catch {
      // never let a logging failure break the test run
    }
    return raw(chunk, enc, cb);
  };
}
process.stdout.write = teeStream(process.stdout.write.bind(process.stdout));
process.stderr.write = teeStream(process.stderr.write.bind(process.stderr));
fs.writeSync(logFd, `# e2e:host — ${new Date().toISOString()} — config=${config}\n`);

// ----- tiny helpers (same palette as build-all.mjs) -----------------------------------------------

const t0 = Date.now();
const elapsed = () => `${((Date.now() - t0) / 1000).toFixed(0)}s`;
function rel(...p) {
  return path.join(repoRoot, ...p);
}
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
  console.error(`\x1b[31me2e-host: ${msg}\x1b[0m`);
  process.exit(1);
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// run a build command, streaming its stdout/stderr through our tee'd streams (so it lands in the log
// too, unlike inherited stdio); rejects on non-zero (build, not test).
function run(cmd, args) {
  console.log(`\x1b[90m  $ ${[cmd, ...args].join(' ')}\x1b[0m`);
  return new Promise((resolve, reject) => {
    const child = spawn(cmd, args, { cwd: repoRoot, stdio: ['inherit', 'pipe', 'pipe'], env: process.env });
    child.stdout.on('data', (d) => process.stdout.write(d));
    child.stderr.on('data', (d) => process.stderr.write(d));
    child.on('error', (e) => reject(new Error(`could not launch ${cmd}: ${e.message}`)));
    child.on('close', (code, signal) => {
      if (code === 0) resolve();
      else reject(new Error(`${cmd} exited with ${code ?? `signal ${signal}`}`));
    });
  });
}

// atelierctl drives one door. atelierctl wants the subcommand FIRST (a leading "-" makes it fall back to
// getStatus), so -addr is injected right after it. For `exec`, atelierctl's own exit status IS the guest
// exit code; for null-result doors it prints "ok (<method>)" and exits 0.
function atelierctl(sub, args = [], { timeout = 30000 } = {}) {
  const res = spawnSync(VMCTL, [sub, '-addr', ADDR, ...args], {
    cwd: repoRoot,
    encoding: 'utf8',
    timeout,
    env: process.env,
  });
  return {
    status: res.status,
    out: res.stdout || '',
    err: res.stderr || '',
    signal: res.signal,
    error: res.error,
  };
}

// ----- assertions + tally -------------------------------------------------------------------------

const pass = [];
const fail = [];
function assert(cond, msg) {
  if (!cond) throw new Error(msg);
}
async function test(name, fn) {
  try {
    const note = await fn();
    pass.push(name);
    console.log(`  \x1b[32m✅ ${name}\x1b[0m${note ? `\x1b[90m — ${note}\x1b[0m` : ''}`);
  } catch (e) {
    fail.push(name);
    console.log(`  \x1b[31m❌ ${name} — ${e.message}\x1b[0m`);
  }
}
const tail = (s, n = 12) => s.trim().split('\n').slice(-n).join('\n');

// Read a host file, retrying briefly: multi-session shares have no Files door (the broker only
// jails the legacy /workspace), so a guest write into /sessions/<tag> is verified by reading the
// host backing dir directly — with a few retries to absorb any fs-share write-back lag.
async function readHostRetry(p, tries = 6, ms = 200) {
  for (let i = 0; ; i++) {
    try {
      return fs.readFileSync(p, 'utf8');
    } catch (e) {
      if (i >= tries) throw e;
      await sleep(ms);
    }
  }
}

// ----- build / preflight --------------------------------------------------------------------------

function bundleComplete() {
  return ['vmlinuz', 'initrd', rootfsName, runnerName].every((f) => fs.existsSync(path.join(BUNDLE, f)));
}

async function ensureBuild() {
  if (flags.skipBuild) {
    section('Preflight (reusing build, --skip-build)');
    for (const [label, p] of [
      ['atelierd', HOST],
      ['atelierctl', VMCTL],
    ]) {
      if (!fs.existsSync(p)) die(`${label} not found at ${p} — run: npm run build:all -- --config=${config} --only=host`);
    }
    if (!bundleComplete()) die(`image bundle incomplete at ${BUNDLE} — run: npm run build:all -- --config=${config} --only=image`);
    info(`host:   ${HOST}`);
    info(`atelierctl:  ${VMCTL}`);
    info(`bundle: ${BUNDLE}`);
    if (isMac) warn('--skip-build trusts the existing broker is codesigned; VZ refuses an unsigned host.');
    return;
  }

  // build-all --only=host does the cgo build + (on macOS) the codesign VZ requires.
  section(`Build atelierd + atelierctl (${config})`);
  await run('node', [rel('scripts', 'build-all.mjs'), `--config=${config}`, '--only=host', '--no-verify']);

  if (flags.rebuildImage || !bundleComplete()) {
    // Fast path: if kernel/initrd/rootfs are already present (in the build tree or in the legacy
    // image/bundle/ staging location) and only the runner volume is missing, build just the runner.
    // This avoids the full multi-minute rootfs rebuild on machines that have a prior --image build
    // or the pre-staged files from image/bundle/.
    if (!flags.rebuildImage) {
      const baseFiles = ['vmlinuz', 'initrd', rootfsName];
      const legacyBase = rel('image', 'bundle', target);
      if (!baseFiles.every((f) => fs.existsSync(path.join(BUNDLE, f)))) {
        if (baseFiles.every((f) => fs.existsSync(path.join(legacyBase, f)))) {
          fs.mkdirSync(BUNDLE, { recursive: true });
          for (const f of baseFiles) {
            info(`seeding ${f} from image/bundle/ -> ${BUNDLE}`);
            fs.copyFileSync(path.join(legacyBase, f), path.join(BUNDLE, f));
          }
        }
      }
      if (baseFiles.every((f) => fs.existsSync(path.join(BUNDLE, f))) &&
          !fs.existsSync(path.join(BUNDLE, runnerName))) {
        section(`Build runner volume only (${target})`);
        const outBase = path.posix.join('..', 'build', config, 'image');
        const runnerCmd = `cd image && TARGET=${target} ATELIER_OUT_BASE='${outBase}' ./build.sh runner`;
        await (isWin
          ? run('wsl', ['bash', '-lc', runnerCmd])
          : run('bash', ['-c', runnerCmd]));
        if (bundleComplete()) return;
      }
    }
    section(`Build VM image bundle (${target})`);
    await run('node', [rel('scripts', 'build-all.mjs'), `--config=${config}`, '--only=image', '--no-verify']);
  } else {
    info(`image bundle present at ${BUNDLE} (pass --rebuild-image to force)`);
  }
  if (!bundleComplete()) die(`image bundle still incomplete at ${BUNDLE} after build`);
}

// ----- broker lifecycle ---------------------------------------------------------------------------

let broker = null;
function startBroker() {
  if (!isWin) fs.rmSync(ADDR, { force: true }); // named pipes aren't files — only unix sockets need pre-cleanup
  const log = fs.openSync(BROKER_LOG, 'w');
  broker = spawn(HOST, ['-addr', ADDR], { cwd: repoRoot, stdio: ['ignore', log, log], env: process.env });
  broker.on('error', (e) => die(`could not launch host: ${e.message}`));
}
async function waitForBroker() {
  for (let i = 0; i < 50; i++) {
    if (atelierctl('getStatus', [], { timeout: 3000 }).status === 0) return;
    if (broker && broker.exitCode !== null) {
      die(`broker exited early (code ${broker.exitCode}); see ${BROKER_LOG}:\n${tail(fs.readFileSync(BROKER_LOG, 'utf8'))}`);
    }
    await sleep(200);
  }
  die(`broker did not come up on ${ADDR}; see ${BROKER_LOG}`);
}
function stopBroker() {
  atelierctl('stopVM', ['-id', 'vm0'], { timeout: 30000 });
  if (broker && broker.exitCode === null) broker.kill(); // SIGTERM on unix; TerminateProcess on windows
}

// The guest's serial console (kernel boot + init) is logged by both drivers as JSON `console` records
// on the broker's stderr — already in BROKER_LOG, but interleaved with JSON-RPC audit noise. Distil
// just those lines into a clean, readable kernel-console log beside it. Returns the line count.
function extractConsole() {
  let raw;
  try {
    raw = fs.readFileSync(BROKER_LOG, 'utf8');
  } catch {
    return 0; // broker never wrote a log
  }
  const lines = [];
  for (const ln of raw.split('\n')) {
    if (!ln.includes('"console"')) continue; // cheap prefilter before JSON.parse
    try {
      const rec = JSON.parse(ln);
      if (rec.msg === 'console' && typeof rec.line === 'string') lines.push(rec.line);
    } catch {
      // skip partial / non-JSON lines
    }
  }
  if (lines.length) fs.writeFileSync(CONSOLE_LOG, lines.join('\n') + '\n');
  return lines.length;
}

// ----- the battery --------------------------------------------------------------------------------

async function runBattery(work) {
  // Workspace fixtures: A is the legacy /workspace share; s1/s2/proj are concurrent per-session
  // shares (the new model — many shares on one VM, each at a tag-chosen target).
  for (const d of ['A', 's1', 's2', 'proj']) fs.mkdirSync(path.join(work, d), { recursive: true });
  fs.writeFileSync(path.join(work, 'A', 'a.txt'), 'alpha\n');
  fs.writeFileSync(path.join(work, 's1', 'b.txt'), 'bravo\n');
  fs.writeFileSync(path.join(work, 's2', 'c.txt'), 'charlie\n');
  fs.writeFileSync(path.join(work, 'proj', 'p.txt'), 'project\n');

  section('Doors: control plane (no VM)');

  await test('getStatus reports a fresh host', () => {
    const r = atelierctl('getStatus');
    assert(r.status === 0, `exit ${r.status}: ${r.err}`);
    const st = JSON.parse(r.out);
    assert(st.service === 'atelierd', `service=${st.service}`);
    assert(st.platform.includes(isMac ? 'darwin' : 'windows'), `platform=${st.platform}`);
    assert(st.vmCount === 0, `vmCount=${st.vmCount} (expected 0)`);
    return `${st.service} ${st.version} ${st.platform}`;
  });

  await test('createVM registers vm0', () => {
    const r = atelierctl('createVM', [
      '-id', 'vm0',
      '-kernel', path.join(BUNDLE, 'vmlinuz'),
      '-initrd', path.join(BUNDLE, 'initrd'),
      '-rootfs', path.join(BUNDLE, rootfsName),
      '-runner', path.join(BUNDLE, runnerName),
    ]);
    assert(r.status === 0, `${r.err}`);
    assert(JSON.parse(atelierctl('getStatus').out).vmCount === 1, 'vmCount did not become 1');
  });

  section('Doors: boot + compute');

  await test('startVM boots the guest', () => {
    const r = atelierctl('startVM', ['-id', 'vm0'], { timeout: 180000 });
    assert(r.status === 0, `boot failed: ${r.err}\n${tail(fs.readFileSync(BROKER_LOG, 'utf8'))}`);
  });

  await test('setTime pushes the host wall clock into the guest', () => {
    // The slim virtual-hwe kernel has no RTC and VZ offers no time-sync, so the
    // guest boots at 1970; the host must push its clock or the agent's TLS fails.
    const r = atelierctl('setTime', ['-id', 'vm0']);
    assert(r.status === 0, `setTime failed: ${r.err}`);
    const y = atelierctl('exec', ['-id', 'vm0', '--', 'date', '-u', '+%Y']);
    assert(y.status === 0, `date exit ${y.status}: ${y.err}`);
    const year = parseInt(y.out.trim(), 10);
    assert(year >= 2026, `guest clock not seeded: year ${JSON.stringify(y.out)}`);
  });

  await test('exec runs a command and streams output', () => {
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'uname', '-s']);
    assert(r.status === 0, `exit ${r.status}: ${r.err}`);
    assert(r.out.includes('Linux'), `unexpected output: ${JSON.stringify(r.out)}`);
  });

  await test('exec propagates the guest exit code', () => {
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'sh', '-c', 'exit 7']);
    assert(r.status === 7, `expected exit 7, got ${r.status}`);
  });

  await test('exec honors -cwd and -env', () => {
    const r = atelierctl('exec', ['-id', 'vm0', '-cwd', '/tmp', '-env', 'FOO=bar', '--', 'sh', '-c', 'pwd; echo $FOO']);
    assert(r.status === 0, `exit ${r.status}: ${r.err}`);
    assert(r.out.includes('/tmp') && r.out.includes('bar'), `cwd/env not applied: ${JSON.stringify(r.out)}`);
  });

  await test('execInput feeds a running session\'s stdin', async () => {
    // head -n1 reads one line then exits; the session registers a stdin channel that execInput targets.
    const sess = 'e2e-stdin';
    const child = spawn(VMCTL, ['exec', '-addr', ADDR, '-id', 'vm0', '-session', sess, '--', 'head', '-n1'], {
      cwd: repoRoot,
      env: process.env,
    });
    let out = '';
    child.stdout.on('data', (d) => (out += d));
    child.stderr.on('data', () => {});
    await sleep(1500); // let runner register the session before we push input
    const fed = atelierctl('execInput', ['-id', 'vm0', '-session', sess, '-content', 'STDIN_ECHO\n']);
    assert(fed.status === 0, `execInput rpc failed: ${fed.err}`);
    const code = await new Promise((resolve) => {
      const to = setTimeout(() => {
        child.kill('SIGKILL');
        resolve('timeout');
      }, 15000);
      child.on('exit', (c) => {
        clearTimeout(to);
        resolve(c);
      });
    });
    assert(code === 0, `backgrounded exec did not exit cleanly (got ${code})`);
    assert(out.includes('STDIN_ECHO'), `fed stdin not echoed: ${JSON.stringify(out)}`);
  });

  section('Doors: sandbox seccomp (F-13, closing F-01)');

  await test('sandboxed exec runs under a seccomp filter', () => {
    // atelierctl exec is the non-privileged (bwrap) path, so the cBPF filter is installed. /proc/self
    // is the grep process inside that sandbox: Seccomp: 2 = SECCOMP_MODE_FILTER.
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'grep', '-E', '^(Seccomp|NoNewPrivs):', '/proc/self/status']);
    assert(r.status === 0, `exit ${r.status}: ${r.err}`);
    assert(/Seccomp:\s*2/.test(r.out), `expected seccomp filter mode (Seccomp: 2): ${JSON.stringify(r.out)}`);
    assert(/NoNewPrivs:\s*1/.test(r.out), `expected NoNewPrivs: 1: ${JSON.stringify(r.out)}`);
    return r.out.trim().replace(/\s+/g, ' ');
  });

  await test('user-namespace creation is denied (F-01)', () => {
    // The no-cap Docker profile drops unshare to the default ERRNO(EPERM); SCMP_ACT_ERRNO returns
    // the errno without killing, so python runs and reports it. 0x10000000 = CLONE_NEWUSER. Before
    // the filter this returned uid=0 with all caps — the user-namespace escalation primitive.
    const py =
      "import ctypes;l=ctypes.CDLL(None,use_errno=True);r=l.unshare(0x10000000);print('rc=%d errno=%d'%(r,ctypes.get_errno()))";
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'python3', '-c', py]);
    assert(r.status === 0, `python probe failed: exit ${r.status}: ${r.err}`);
    assert(/rc=-1 errno=1/.test(r.out), `unshare(CLONE_NEWUSER) was not EPERM-denied: ${JSON.stringify(r.out)}${r.err}`);
    return 'unshare(CLONE_NEWUSER) → EPERM';
  });

  await test('ptrace is denied by seccomp (no CAP_SYS_PTRACE)', () => {
    // ptrace is allowed only with CAP_SYS_PTRACE in the profile; the no-cap agent gets ERRNO.
    const py = "import ctypes;l=ctypes.CDLL(None,use_errno=True);r=l.ptrace(0,0,0,0);print('rc=%d errno=%d'%(r,ctypes.get_errno()))";
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'python3', '-c', py]);
    assert(r.status === 0, `probe failed: ${r.err}`);
    assert(/rc=-1 errno=1/.test(r.out), `ptrace not EPERM-denied: ${JSON.stringify(r.out)}`);
    return 'ptrace → EPERM';
  });

  await test('mount is denied by seccomp (no CAP_SYS_ADMIN)', () => {
    const py =
      "import ctypes;l=ctypes.CDLL(None,use_errno=True);r=l.mount(b'none',b'/tmp/x',b'tmpfs',0,None);print('rc=%d errno=%d'%(r,ctypes.get_errno()))";
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'python3', '-c', py]);
    assert(r.status === 0, `probe failed: ${r.err}`);
    assert(/rc=-1 errno=1/.test(r.out), `mount not EPERM-denied: ${JSON.stringify(r.out)}`);
    return 'mount → EPERM';
  });

  section('Doors: sandbox hardening (narrowed bind, sysctls, cgroups, Landlock)');

  await test('Landlock shim runs ahead of every sandboxed command', () => {
    // sandboxedCommand makes the shim the exec target; it logs to stderr, then execs the cmd.
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'true']);
    assert(r.status === 0, `exec failed: ${r.err}`);
    assert(/atelier-landlock: Landlock ruleset applied/.test(r.err), `shim did not apply Landlock: ${JSON.stringify(r.err)}`);
    return 'Landlock applied before exec';
  });

  await test('runner volume (/opt/runner) is not bound into the sandbox (F-03)', () => {
    // The runner binary + seccomp blob are absent (not bound). Landlock also denies them.
    const cat = atelierctl('exec', ['-id', 'vm0', '--', 'cat', '/opt/runner/atelier-runner']);
    assert(cat.status !== 0, 'runner binary was readable inside the sandbox');
    // /opt/atelier IS present (the toolbox). NB: `ls /opt` is itself denied by Landlock (the
    // /opt parent isn't in the allow-list), so probe the granted subtree directly.
    const ls = atelierctl('exec', ['-id', 'vm0', '--', 'ls', '/opt/atelier']);
    assert(ls.status === 0 && ls.out.includes('packages'), `/opt/atelier not usable in sandbox: ${ls.out}${ls.err}`);
    return '/opt/runner absent, /opt/atelier present';
  });

  await test('kernel hardening sysctls are set (F-04, F-16)', () => {
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'sh', '-c',
      'cat /proc/sys/kernel/kptr_restrict /proc/sys/kernel/io_uring_disabled /proc/sys/kernel/modules_disabled /proc/sys/kernel/yama/ptrace_scope']);
    assert(r.status === 0, `read sysctls failed: ${r.err}`);
    const v = r.out.split(/\s+/).filter(Boolean);
    assert(v[0] === '2', `kptr_restrict=${v[0]} want 2`);
    assert(v[1] === '2', `io_uring_disabled=${v[1]} want 2`);
    assert(v[2] === '1', `modules_disabled=${v[2]} want 1`);
    assert(v[3] === '2', `ptrace_scope=${v[3]} want 2`);
    return `kptr=${v[0]} io_uring=${v[1]} modules=${v[2]} ptrace=${v[3]}`;
  });

  await test('io_uring_setup is blocked (seccomp + io_uring_disabled)', () => {
    // io_uring_setup = 425 on both arm64 and amd64. EPERM(1) from seccomp, or ENOSYS(38)
    // from io_uring_disabled=2 — either way it must not succeed.
    const py =
      "import ctypes;l=ctypes.CDLL(None,use_errno=True);r=l.syscall(425,8,0);print('rc=%d errno=%d'%(r,ctypes.get_errno()))";
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'python3', '-c', py]);
    assert(r.status === 0, `probe failed: ${r.err}`);
    assert(/rc=-1 errno=(1|38)/.test(r.out), `io_uring_setup not blocked: ${JSON.stringify(r.out)}`);
    return r.out.trim();
  });

  await test('cgroup pids.max caps process count (F-06)', () => {
    // Spawn many short sleeps; pids.max=512 makes fork fail well before 700. The PID
    // namespace tears them all down when the root command exits (no leak).
    const py = [
      'import subprocess as s',
      'ps=[]',
      'try:',
      " for i in range(700): ps.append(s.Popen(['sleep','30']))",
      'except OSError: pass',
      "print('SPAWNED', len(ps))",
      'for p in ps: p.kill()',
    ].join('\n');
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'python3', '-c', py], { timeout: 60000 });
    assert(r.status === 0, `probe failed: ${r.err}`);
    const m = r.out.match(/SPAWNED (\d+)/);
    assert(m, `no SPAWNED count: ${r.out}`);
    assert(Number(m[1]) < 640, `pids not capped (spawned ${m[1]}, expected <640 with pids.max=512)`);
    return `spawned ${m[1]} (capped)`;
  });

  await test('read-only toolbox rejects writes (Landlock + ro-bind)', () => {
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'sh', '-c', 'touch /usr/atelier-evil 2>&1; echo rc=$?']);
    assert(/rc=[^0\s]/.test(r.out), `write to /usr was not denied: ${JSON.stringify(r.out)}`);
    return '/usr is read-only';
  });

  await test('toolbox userland still works under the narrowed bind', () => {
    // Regression guard: narrowing --bind / / must not starve Node/python/git/ripgrep.
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'sh', '-c',
      'node --version && python3 --version && git --version && rg --version | head -n1']);
    assert(r.status === 0, `a toolbox binary failed under the narrowed bind: ${r.err}${r.out}`);
    assert(/v\d+\./.test(r.out), `node missing: ${r.out}`);
    assert(/Python 3/.test(r.out), `python3 missing: ${r.out}`);
    assert(/git version/.test(r.out), `git missing: ${r.out}`);
    return r.out.trim().replace(/\s+/g, ' ').slice(0, 70);
  });

  await test('host rootfs is not exposed in the sandbox (F-03 scope)', () => {
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'sh', '-c',
      'for p in /etc/shadow /root /boot /var/log /opt/runner /srv; do [ -e "$p" ] && echo "LEAK:$p"; done; echo done']);
    assert(r.status === 0, `probe failed: ${r.err}`);
    assert(!/LEAK:/.test(r.out), `a host rootfs path leaked into the sandbox: ${r.out}`);
    return 'no /etc/shadow, /root, /boot, /var/log, /opt/runner, /srv';
  });

  await test('raw disk and vsock are unreachable from the sandbox (key invariants)', () => {
    // The agent must never see the backing block device or the host↔guest vsock node.
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'sh', '-c',
      'for d in /dev/vda /dev/vda1 /dev/sda /dev/vsock /dev/mem; do [ -e "$d" ] && echo "PRESENT:$d"; done; echo done']);
    assert(r.status === 0, `probe failed: ${r.err}`);
    assert(!/PRESENT:/.test(r.out), `a block/vsock device is reachable in the sandbox: ${r.out}`);
    return 'no /dev/vda*, /dev/sda, /dev/vsock, /dev/mem';
  });

  await test('/etc is an allow-list, not the whole host /etc', () => {
    const ok = atelierctl('exec', ['-id', 'vm0', '--', 'cat', '/etc/resolv.conf']);
    assert(ok.status === 0, `/etc/resolv.conf should be readable: ${ok.err}`);
    const bad = atelierctl('exec', ['-id', 'vm0', '--', 'cat', '/etc/shadow']);
    assert(bad.status !== 0, '/etc/shadow was readable in the sandbox');
    return 'resolv.conf in, shadow out';
  });

  await test('runs as uid 1001 with an empty capability set', () => {
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'sh', '-c', 'id -u; grep -E "^CapEff:" /proc/self/status']);
    assert(r.status === 0, `probe failed: ${r.err}`);
    assert(/^1001$/m.test(r.out), `not uid 1001: ${JSON.stringify(r.out)}`);
    assert(/CapEff:\s*0{16}\b/.test(r.out), `non-empty effective capabilities: ${JSON.stringify(r.out)}`);
    return r.out.trim().replace(/\s+/g, ' ');
  });

  await test('PID namespace isolates the process table', () => {
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'sh', '-c', 'ls /proc | grep -cE "^[0-9]+$"']);
    assert(r.status === 0, `probe failed: ${r.err}`);
    const n = Number(r.out.trim());
    assert(n > 0 && n < 30, `expected a small isolated pid table, got ${n}`);
    return `${n} pids visible`;
  });

  await test('Landlock confines outbound TCP to port 443', () => {
    // Landlock ConnectTCP(443) denies connect() to any other port at the syscall (EACCES),
    // before the egress jail even sees it; 443 is allowed through (then refused by the stack
    // since nothing listens locally). EACCES on :9 vs a non-EACCES on :443 isolates the
    // Landlock network layer from the egress jail.
    const probe =
      "const net=require('net');" +
      "const t=p=>new Promise(r=>{const s=net.connect(p,'127.0.0.1');" +
      "s.on('connect',()=>{s.destroy();r('OPEN')});s.on('error',e=>r(e.code))});" +
      "(async()=>{console.log('p9='+await t(9));console.log('p443='+await t(443))})()";
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'node', '-e', probe], { timeout: 15000 });
    assert(/p9=(EACCES|EPERM)/.test(r.out), `non-443 connect not denied by Landlock: ${r.out}${r.err}`);
    assert(!/p443=(EACCES|EPERM)/.test(r.out), `port 443 was wrongly denied by Landlock: ${r.out}`);
    return r.out.trim().replace(/\s+/g, ' ');
  });

  section('Doors: legacy workspace + Files door (host<->guest bridge)');

  await test('attachWorkspace (legacy) shares the folder at /workspace', () => {
    const r = atelierctl('attachWorkspace', ['-id', 'vm0', '-path', path.join(work, 'A')]);
    assert(r.status === 0, `${r.err}`);
    const ls = atelierctl('exec', ['-id', 'vm0', '--', 'cat', '/workspace/a.txt']);
    assert(ls.out.includes('alpha'), `a.txt not visible at /workspace: ${ls.out}${ls.err}`);
  });

  await test('writeFile (host Files door) is visible to guest exec over the fs share', () => {
    const w = atelierctl('writeFile', ['-path', 'from-host.txt', '-content', 'HOST_WROTE_THIS']);
    assert(w.status === 0, `${w.err}`);
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'cat', '/workspace/from-host.txt']);
    assert(r.out.includes('HOST_WROTE_THIS'), `host write not seen in guest: ${r.out}${r.err}`);
  });

  await test('readFile (host Files door) sees what guest exec wrote', () => {
    const g = atelierctl('exec', ['-id', 'vm0', '--', 'sh', '-c', 'printf GUEST_WROTE_THIS > /workspace/from-guest.txt']);
    assert(g.status === 0, `guest write failed: ${g.err}`);
    const r = atelierctl('readFile', ['-path', 'from-guest.txt']);
    assert(r.status === 0, `${r.err}`);
    assert(r.out.includes('GUEST_WROTE_THIS'), `readFile returned: ${JSON.stringify(r.out)}`);
  });

  await test('Files door jails path traversal', () => {
    const r = atelierctl('readFile', ['-path', '../../../../etc/passwd']);
    assert(r.status !== 0, 'traversal was NOT rejected');
    assert(/escape|relative|workspace/i.test(r.err), `unexpected error text: ${r.err}`);
  });

  await test('detachWorkspace (legacy) closes the Files door', () => {
    const r = atelierctl('detachWorkspace', ['-id', 'vm0']);
    assert(r.status === 0, `${r.err}`);
    assert(atelierctl('readFile', ['-path', 'a.txt']).status !== 0, 'Files door still open after detach');
  });

  section('Doors: multi-session shares (the new model — concurrent /sessions/<tag>)');

  const attach = (tag, dir, tgt) =>
    atelierctl('attachWorkspace', ['-id', 'vm0', '-path', path.join(work, dir), '-tag', tag, '-target', tgt]);
  // Under the narrowed bind, an exec only sees its OWN workspace, so it must declare -cwd
  // (or -session) for the target it operates on. sexec scopes an exec to one target.
  const sexec = (tgt, ...cmd) => atelierctl('exec', ['-id', 'vm0', '-cwd', tgt, '--', ...cmd]);

  await test('many tagged shares mount concurrently at chosen targets', () => {
    assert(attach('s1', 's1', '/sessions/s1').status === 0, 's1 attach failed');
    assert(attach('s2', 's2', '/sessions/s2').status === 0, 's2 attach failed');
    assert(attach('proj', 'proj', '/mnt/proj').status === 0, 'custom-target attach failed');
    assert(sexec('/sessions/s1', 'cat', '/sessions/s1/b.txt').out.includes('bravo'), 's1 not mounted');
    assert(sexec('/sessions/s2', 'cat', '/sessions/s2/c.txt').out.includes('charlie'), 's2 not mounted');
    // The new model isn't tied to /sessions: target is arbitrary (here /mnt/proj).
    assert(sexec('/mnt/proj', 'cat', '/mnt/proj/p.txt').out.includes('project'), 'custom target not mounted');
  });

  await test('a session cannot see sibling sessions (F-09, structural)', () => {
    // The narrowed bind only mounts the exec's own target, so siblings are absent from the
    // namespace entirely — not merely empty.
    const r = sexec('/sessions/s1', 'ls', '/sessions');
    assert(!/\bs2\b/.test(r.out), `sibling session s2 visible from s1: ${JSON.stringify(r.out)}`);
    assert(sexec('/sessions/s1', 'cat', '/sessions/s2/c.txt').status !== 0, 's2 readable from an s1-scoped exec');
    return 'siblings invisible';
  });

  await test('sessions are isolated from one another', () => {
    assert(!sexec('/sessions/s1', 'ls', '/sessions/s1').out.includes('c.txt'), 's2 content leaked into s1');
    assert(!sexec('/sessions/s2', 'ls', '/sessions/s2').out.includes('b.txt'), 's1 content leaked into s2');
  });

  await test('a session share is read-write host<->guest (no Files door)', async () => {
    // host -> guest: write the host backing dir directly, guest sees it through the share.
    fs.writeFileSync(path.join(work, 's1', 'host-seed.txt'), 'FROM_HOST_S1');
    assert(
      sexec('/sessions/s1', 'cat', '/sessions/s1/host-seed.txt').out.includes('FROM_HOST_S1'),
      'host write not seen in session',
    );
    // guest -> host: guest writes via the share; verify on the host backing dir (readFile can't —
    // the Files door is jailed to the legacy workspace, which is now detached).
    const g = sexec('/sessions/s1', 'sh', '-c', 'printf FROM_GUEST_S1 > /sessions/s1/guest-out.txt');
    assert(g.status === 0, `guest write failed: ${g.err}`);
    const got = await readHostRetry(path.join(work, 's1', 'guest-out.txt'));
    assert(got.includes('FROM_GUEST_S1'), `guest write not on host backing dir: ${JSON.stringify(got)}`);
    assert(atelierctl('readFile', ['-path', 'guest-out.txt']).status !== 0, 'Files door unexpectedly reached a session share');
  });

  await test('detach is sibling-safe (one session out, the rest survive)', () => {
    assert(atelierctl('detachWorkspace', ['-id', 'vm0', '-tag', 's1']).status === 0, 's1 detach failed');
    // s1 is gone: a fresh exec can no longer reach it (bind source absent).
    assert(!sexec('/sessions/s2', 'cat', '/sessions/s1/b.txt').out.includes('bravo'), 's1 survived detach');
    assert(sexec('/sessions/s2', 'cat', '/sessions/s2/c.txt').out.includes('charlie'), 'sibling s2 lost after s1 detach');
    assert(sexec('/mnt/proj', 'cat', '/mnt/proj/p.txt').out.includes('project'), 'sibling proj lost after s1 detach');
    assert(atelierctl('detachWorkspace', ['-id', 'vm0', '-tag', 's2']).status === 0, 's2 detach failed');
    assert(atelierctl('detachWorkspace', ['-id', 'vm0', '-tag', 'proj']).status === 0, 'proj detach failed');
  });

  await test('detaching an unknown tag is rejected', () => {
    assert(atelierctl('detachWorkspace', ['-id', 'vm0', '-tag', 'nope']).status !== 0, 'unknown-tag detach was not rejected');
  });

  section('Doors: egress jail');

  await test('setEgressPolicy accepts deny-all and an allowlist', () => {
    assert(atelierctl('setEgressPolicy', ['-allow', '']).status === 0, 'deny-all rejected');
    assert(atelierctl('setEgressPolicy', ['-allow', 'pypi.org,files.pythonhosted.org']).status === 0, 'allowlist rejected');
  });

  await test('default-deny blocks a non-allowlisted host', () => {
    atelierctl('setEgressPolicy', ['-allow', '']); // deny all
    const probe =
      "fetch('https://example.com',{signal:AbortSignal.timeout(4000)})" +
      ".then(r=>console.log('REACHED '+r.status)).catch(e=>console.log('BLOCKED '+(e&&e.name)))";
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'node', '-e', probe], { timeout: 30000 });
    assert((r.out + r.err).includes('BLOCKED'), `egress was not blocked: ${r.out}${r.err}`);
  });

  await test('DNS sinkhole: a disallowed host does not resolve', () => {
    atelierctl('setEgressPolicy', ['-allow', 'api.anthropic.com']); // only the model host resolves
    const probe = "require('dns').lookup('example.com',e=>console.log(e?('NXDOMAIN '+e.code):'RESOLVED'))";
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'node', '-e', probe], { timeout: 15000 });
    assert(/NXDOMAIN|ENOTFOUND|EAI_AGAIN/.test(r.out), `disallowed host resolved: ${r.out}${r.err}`);
    return 'example.com → not resolved';
  });

  await test('direct-by-IP egress is blocked (bypasses the DNS allowlist)', () => {
    atelierctl('setEgressPolicy', ['-allow', 'api.anthropic.com']); // host allowed, but a raw IP skips DNS
    const probe =
      "fetch('https://1.1.1.1',{signal:AbortSignal.timeout(4000)})" +
      ".then(r=>console.log('REACHED '+r.status)).catch(e=>console.log('BLOCKED '+(e&&e.name)))";
    const r = atelierctl('exec', ['-id', 'vm0', '--', 'node', '-e', probe], { timeout: 30000 });
    assert((r.out + r.err).includes('BLOCKED'), `direct-IP egress was not blocked: ${r.out}${r.err}`);
    return 'direct IP → blocked';
  });

  section('Door + loop: in-guest agent (Topology B)');

  await test('agent loop runs and reaches the model through the jail', () => {
    assert(process.env.ANTHROPIC_API_KEY, 'ANTHROPIC_API_KEY is not set in this environment');
    const token = 'ATELIER_E2E_OK_4710';
    const taskText = `Respond with exactly this token and nothing else: ${token}`;
    // `atelierctl agent` opens egress to api.anthropic.com, then execs the in-guest agent CLI; a real
    // model round-trip through the jail is the positive egress proof.
    const r = atelierctl('agent', ['-id', 'vm0', '--', taskText], { timeout: 180000 });
    assert(r.status === 0, `agent exited ${r.status}: ${tail(r.err)}`);
    assert((r.out + r.err).includes(token), `agent output missing token:\n${tail(r.out + r.err)}`);
  });

  await test('serve-mode agent loop: a persistent turn over execInput (the desktop path)', async () => {
    assert(process.env.ANTHROPIC_API_KEY, 'ANTHROPIC_API_KEY is not set in this environment');
    // The test above drives `atelierctl agent` (one-shot runOnce). The DESKTOP instead runs a PERSISTENT
    // loop — partisan `cli_guest.py --serve`, fed user turns over execInput as NDJSON (the Session
    // Manager). That path had zero e2e coverage, which is how an env-drift regression shipped: HOME on a
    // non-writable dir → the agent can't launch. Reproduce the desktop path: attach a session share,
    // launch --serve with the SAME env the manager builds, feed one turn, and assert the loop streams
    // init → a result carrying the token, then closes cleanly.
    const tag = 'serve';
    const gpath = `/sessions/${tag}`;
    fs.mkdirSync(path.join(work, tag), { recursive: true });
    atelierctl('setEgressPolicy', ['-allow', 'api.anthropic.com']); // serve's only escape (atelierctl exec won't set it)
    atelierctl('setTime', ['-id', 'vm0']); // the model's TLS needs a valid guest clock
    assert(
      atelierctl('attachWorkspace', ['-id', 'vm0', '-path', path.join(work, tag), '-tag', tag, '-target', gpath]).status === 0,
      'serve share attach failed',
    );

    const sess = 'e2e-serve';
    const token = 'ATELIER_SERVE_OK_8842';
    // KEEP IN SYNC with apps/desktop/src/main/sessions/manager.ts startLoop (itself mirroring atelierctl
    // agent's genv): the writable HOME/TMPDIR/XDG_CACHE_HOME/PARTISAN_PERSIST are load-bearing — the
    // non-root agent (uid 1001, /opt read-only) can't write its conversation store or caches without them.
    const env = [
      '-env', `ANTHROPIC_API_KEY=${process.env.ANTHROPIC_API_KEY}`,
      '-env', 'DISABLE_AUTOUPDATER=1',
      '-env', 'DISABLE_TELEMETRY=1',
      '-env', 'DISABLE_ERROR_REPORTING=1',
      '-env', 'OPENHANDS_SUPPRESS_BANNER=1',
      '-env', 'LITELLM_LOCAL_MODEL_COST_MAP=True',
      '-env', 'HOME=/home/atelier',
      '-env', 'TMPDIR=/tmp',
      '-env', 'XDG_CACHE_HOME=/home/atelier/.cache',
      '-env', 'PARTISAN_PERSIST=/home/atelier/.partisan',
    ];
    const child = spawn(
      VMCTL,
      [
        'exec', '-addr', ADDR, '-id', 'vm0', '-session', sess, '-cwd', '/opt/atelier/packages/partisan', ...env,
        '--', '/opt/atelier/packages/partisan/.venv/bin/python', 'cli_guest.py', '--serve', '--workspace', gpath,
      ],
      { cwd: repoRoot, env: process.env },
    );

    // Parse the loop's NDJSON stdout exactly as the Session Manager's onOutput does.
    const events = [];
    let buf = '';
    let errOut = '';
    child.stdout.on('data', (d) => {
      buf += d.toString('utf8');
      for (let nl; (nl = buf.indexOf('\n')) >= 0; ) {
        const line = buf.slice(0, nl).trim();
        buf = buf.slice(nl + 1);
        if (line)
          try {
            events.push(JSON.parse(line));
          } catch {
            /* atelierctl/guest noise that isn't a wire event */
          }
      }
    });
    child.stderr.on('data', (d) => (errOut += d)); // guest loop diagnostics, incl. SDK launch errors
    let exited = null;
    child.on('exit', (c) => (exited = c));

    // Resolve when an event matches; reject on an error event, early exit, or timeout — surfacing the
    // loop's own message (e.g. "native binary ... failed to launch") so a failure is self-explanatory.
    const waitEvent = (pred, ms, label) =>
      new Promise((resolve, reject) => {
        const started = Date.now();
        const tick = () => {
          const hit = events.find(pred);
          if (hit) return resolve(hit);
          const errEv = events.find((e) => e.type === 'error');
          if (errEv) return reject(new Error(`loop error before ${label}: ${errEv.message}`));
          if (exited !== null) return reject(new Error(`loop exited (code ${exited}) before ${label}\n${tail(errOut)}`));
          if (Date.now() - started > ms) return reject(new Error(`timed out for ${label}; saw [${events.map((e) => e.type).join(', ')}]`));
          setTimeout(tick, 200);
        };
        tick();
      });

    try {
      await sleep(1500); // let runner register the session's stdin channel (mirrors the stdin-feed test)
      // If the loop crashed on launch (env-drift / missing venv / tmux), report it plainly.
      const early = events.find((e) => e.type === 'error');
      assert(!early, `loop errored on launch: ${early && early.message}\n${tail(errOut)}`);
      assert(exited === null, `loop exited (code ${exited}) before first turn\n${tail(errOut)}`);

      const turn = JSON.stringify({ type: 'user', text: `Respond with exactly this token and nothing else: ${token}` });
      const fed = atelierctl('execInput', ['-id', 'vm0', '-session', sess, '-content', turn + '\n']);
      assert(fed.status === 0, `execInput failed: ${fed.err}`);
      await waitEvent((e) => e.type === 'result' || e.type === 'turn_done', 180000, 'turn result');

      assert(events.some((e) => e.type === 'init' && e.sessionId), 'no init event (the resume handle) was emitted');
      const sawToken = events.some(
        (e) =>
          (e.type === 'text' && typeof e.text === 'string' && e.text.includes(token)) ||
          (e.type === 'result' && typeof e.result === 'string' && e.result.includes(token)),
      );
      assert(sawToken, `token missing from loop output; saw [${events.map((e) => e.type).join(', ')}]\n${tail(errOut)}`);

      // Clean shutdown: {"type":"close"} ends the input queue → the loop exits 0 (hibernate's path too).
      atelierctl('execInput', ['-id', 'vm0', '-session', sess, '-content', '{"type":"close"}\n']);
      const code = await new Promise((resolve) => {
        const started = Date.now();
        const poll = () => {
          if (exited !== null) return resolve(exited);
          if (Date.now() - started > 15000) {
            child.kill('SIGKILL');
            return resolve('timeout');
          }
          setTimeout(poll, 200);
        };
        poll();
      });
      assert(code === 0, `serve loop did not exit cleanly on close (got ${code})`);
    } finally {
      if (exited === null) child.kill('SIGKILL');
      atelierctl('detachWorkspace', ['-id', 'vm0', '-tag', tag]);
    }
    return 'init → turn → token → clean close';
  });

  section('Doors: teardown');

  await test('stopVM tears the guest down', () => {
    const r = atelierctl('stopVM', ['-id', 'vm0']);
    assert(r.status === 0, `${r.err}`);
    assert(JSON.parse(atelierctl('getStatus').out).vmCount === 0, 'vmCount did not return to 0');
  });
}

// ----- main ---------------------------------------------------------------------------------------

async function main() {
  if (!isMac && !isWin) die(`unsupported platform '${process.platform}' (darwin or win32 only)`);
  if (isWin) info('Windows: broker on named pipe, VM through HCS/Hyper-V (not VZ).');

  await ensureBuild();

  const work = fs.mkdtempSync(path.join(os.tmpdir(), 'atelier-e2e-'));
  let cleaned = false;
  const cleanup = () => {
    if (cleaned) return;
    cleaned = true;
    stopBroker();
    extractConsole();
    fs.rmSync(work, { recursive: true, force: true });
    if (!isWin) fs.rmSync(ADDR, { force: true }); // named pipes aren't files
  };
  process.on('SIGINT', () => {
    cleanup();
    process.exit(130);
  });

  try {
    section('Spawn broker');
    startBroker();
    await waitForBroker();
    info(`broker pid ${broker.pid} on ${ADDR} (log: ${BROKER_LOG})`);
    await runBattery(work);
  } finally {
    cleanup();
  }

  section('Summary');
  for (const n of pass) console.log(`  \x1b[32m✅ ${n}\x1b[0m`);
  for (const n of fail) console.log(`  \x1b[31m❌ ${n}\x1b[0m`);
  console.log(`\n  ${pass.length} passed, ${fail.length} failed  \x1b[90m(+${elapsed()})\x1b[0m`);
  console.log(`  \x1b[90mfull log:   ${LOG_FILE}\x1b[0m`);
  console.log(`  \x1b[90mbroker log: ${BROKER_LOG}\x1b[0m`);
  if (fs.existsSync(CONSOLE_LOG)) console.log(`  \x1b[90mvm console: ${CONSOLE_LOG}\x1b[0m`);
  if (fail.length) {
    console.log(`\n\x1b[31mRESULT: FAIL\x1b[0m  (broker log: ${BROKER_LOG})`);
    process.exit(1);
  }
  console.log('\n\x1b[32mRESULT: PASS\x1b[0m');
}

main().catch((e) => {
  console.error(`\x1b[31me2e-host: ${e.stack || e.message}\x1b[0m`);
  process.exit(1);
});
