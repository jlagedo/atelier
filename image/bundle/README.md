# image/bundle

Build output (gitignored), one subdirectory per build TARGET:

```text
windows-amd64-hyperv/   vmlinuz initrd rootfs.vhd runner.vhd manifest.txt   (make windows / default)
darwin-arm64-vz/        vmlinuz initrd rootfs.raw runner.raw manifest.txt   (make darwin)
```

Produced by `image/build.sh` (`make -C image <target>`); see `docs/design.md` §7.

`runner.{raw,vhd}` is the ro guest payload volume — it carries BOTH the runner daemon
(`/opt/runner/atelier-runner`) and the in-guest agent (`/opt/atelier`, code + node_modules), neither
baked into the rootfs. It's attached as a second disk and mounted at `/opt` by `init.sh`
(`LABEL=runner`). Rebuild just it with `make -C image runner` / `./build.sh runner` to iterate on the
runner or the agent without rebuilding the whole image (it does an `npm ci` for the agent, so it's not
as instant as the old daemon-only volume).
