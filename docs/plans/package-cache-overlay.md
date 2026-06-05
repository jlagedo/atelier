# Package-manager cache via shared overlay

| Field | Detail |
|---|---|
| Status | Design — not yet executed |
| Primary reader | Engineers evaluating guest package install performance and isolation. |
| Objective | Make `pip` / `uv` / `npm` installs in the guest fast and persistent without eroding session isolation |
| Approach | Shared read-only package cache as overlayfs `lowerdir` + per-session writeable `upperdir`; install trees stay per-session in `/sessions` |
| Validated | overlayfs + uv/pip/npm cache semantics confirmed against kernel/Docker/tool docs + issue trackers (2026-05) |

Cowork's per-session installs hit the read-only-ish root partition, do not
persist, and re-burn disk every session on a VM with about 940 MB free for user
work. See [`../research/claude-cowork-internals.md`](../research/claude-cowork-internals.md)
§11-12.

## 1. Problem

Package installs create two different surfaces:

| Surface | Policy |
|---|---|
| Tool binaries (`node`, `pip`, `uv`, `npm`) | Bake into the read-only image surface, like `runner` and the agent. |
| Install output: download cache and install tree (`site-packages`, `node_modules`) | Make cache reuse fast, persistent, and isolated. |

Rejected alternatives:

| Alternative | Reason rejected |
|---|---|
| Pre-bake a big package zoo | Bloats the image. Cowork bakes 717 MB of pip packages, including dual OpenCV, and still misses user-requested packages. |
| One shared writable cache dir | Lets one session poison another and hits documented package-manager corruption bugs from concurrent writers. |

## 2. Design: overlay the cache, keep install trees per-session

Split cache artifacts from install trees.

### The download cache → overlayfs

```text
merged cache  =  what the package manager sees (e.g. ~/.cache/uv)
                       │
        ┌──────────────┴───────────────┐
   upperdir (rw)                    lowerdir (ro, SHARED)
   per-session: cache misses        warm cache, baked or host-managed:
   spill here                       common wheels / npm tarballs
```

- **Read:** hit shared `lower`; skip network and re-download.
- **Write miss:** fetch once into the session's private `upper`; leave shared `lower`
  untouched.

Each package manager already supports a redirectable cache dir:

| Tool | Env var | Notes |
|---|---|---|
| uv | `UV_CACHE_DIR` | Best fit — cache is content-addressed + designed thread-safe/append-only/shareable |
| pip | `PIP_CACHE_DIR` | Keep pip's temp build dir on the **same fs** as its cache (avoids non-atomic `shutil.move`) |
| npm | `npm_config_cache` | Least robust under sharing; lean on `npm cache verify` as a safety net |

Point all three at the merged overlay mount.

### The install tree → stays per-session

`site-packages`, venvs, and `node_modules` live in `/sessions/<tag>`. They are
never shared. Only the raw artifact cache goes through the overlay.

## 3. Map onto Atelier's volumes

- **lowerdir** → ship the warm cache on a ro volume, like `runner`/agent ride `/opt` today
  (`image/build.sh runner`, mounted in `image/guest/init.sh`). Or attach as a separate ro disk.
- **upperdir + workdir** → carve from `/sessions/<tag>` (e.g. `/sessions/<tag>/.cache-upper` +
  `.cache-work`). overlayfs requires `upper` and `workdir` on the **same filesystem**, both
  writeable — `/sessions` already is.
- **mount** → one `mount -t overlay` per session at boot/attach, alongside the other mounts in
  `init.sh`.

## 4. Lifecycle / "what happens when a session dies"

The merged **mount** is ephemeral; the **directories behind it are real files on a persistent disk**.

| Event | `/sessions/<tag>` (install tree + workspace) | Cache upper | overlay mount |
|---|---|---|---|
| Hibernate (idle/LRU → resume) | persists | persists | torn down, remount on resume |
| VM stop / crash / reboot | persists (disk volume, not tmpfs) | persists | gone, remount on boot |
| User deletes session | deleted (intentional) | deleted with it | gone |

Two surfaces, two policies:

- **Install tree + workspace** (`/sessions/<tag>`) → **must persist** so hibernate→resume works.
  Never auto-clear.
- **Cache upper** (overlay miss-spillover) → **disposable by choice.** Fully regenerable from the
  registry, so it *can* be GC'd on session end / under disk pressure (cost: re-download of misses).
  This is the knob Cowork lacks.

## 5. Validation notes + gotchas

Confirmed against kernel/Docker/tool docs + issue trackers (2026-05):

- **Shared ro `lower` across many mounts is standard** (this is how containers work). Hard rule:
  **each mount needs its own `upper` + `workdir`** — overlapping them ⇒ `EBUSY` / undefined
  behaviour. Per-session upper satisfies this.
- **Same-filesystem rule partially defeats zero-copy.** uv/pip rely on hardlinks/atomic rename,
  which need cache and target on the same fs. Cache **misses** (upper on `/sessions`) → hardlink
  into venv works. Cache **hits** (shared `lower` on a different ro volume) → cross-fs ⇒ **copy**,
  not hardlink (overlayfs also breaks hardlinks on `copy_up`). Net: the shared layer eliminates
  **network fetch + unpack** (the expensive part), not the local copy. Set `UV_LINK_MODE=copy` to
  make this deterministic. **Do not market it as zero-copy.**
- **Per-session upper is also a correctness win, not just isolation.** npm has documented shared-cache
  corruption under concurrent writes; pip's wheel cache has the analogous cross-fs concurrency bug.
  Both come from *multiple writers to one cache* — structurally impossible here (one writer per
  upper; shared lower is read-only).
- **Unclean-shutdown workdir hygiene.** overlayfs does *not* self-clean `workdir`; a non-empty
  `work/work` is the normal post-crash symptom and you must wipe it (scratch only — **never**
  `upper/`) before remount. Cleaner option: mount the cache overlay with the **`volatile`** option —
  it skips syncs (faster, non-durable, fine for a disposable layer) and drops a
  `$workdir/work/incompat/volatile` marker that makes overlayfs **refuse to remount if a crash left
  it dirty** — a built-in "discard upper" tripwire.

Tool ranking for this pattern: **uv > pip > npm** (cleanliness of cache sharing).

### Sources

- [Overlay Filesystem — Linux Kernel docs](https://docs.kernel.org/filesystems/overlayfs.html)
- [OverlayFS storage driver — Docker Docs](https://docs.docker.com/engine/storage/drivers/overlayfs-driver/)
- [Overlayfs non-standard behavior (hardlinks / copy_up)](https://github.com/amir73il/overlayfs/wiki/Overlayfs-non-standard-behavior)
- [Caching — uv docs](https://docs.astral.sh/uv/concepts/cache/)
- [uv: hardlink across filesystems (issue #15149)](https://github.com/astral-sh/uv/issues/15149)
- [pip Caching docs](https://pip.pypa.io/en/stable/topics/caching/)
- [pip wheel cache cross-fs concurrency (issue #13540)](https://github.com/pypa/pip/issues/13540)
- [npm concurrent install cache corruption (issue #5948)](https://github.com/npm/npm/issues/5948)
- [npm common errors / cache verify](https://docs.npmjs.com/common-errors/)
- [overlayfs power-cut workdir failure (linux-mtd)](https://linux-mtd.infradead.narkive.com/ZfUaAE91/overlayfs-ubifs-power-cut-results-in-failed-to-create-directory-overlay-work-work-errno-17-mounting-)
