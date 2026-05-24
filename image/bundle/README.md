# image/bundle

Build output (gitignored), one subdirectory per build TARGET:

```text
windows-amd64-hyperv/   vmlinuz initrd rootfs.vhd guestd.vhd manifest.txt   (make windows / default)
darwin-arm64-vz/        vmlinuz initrd rootfs.raw guestd.raw manifest.txt   (make darwin)
```

Produced by `image/build.sh` (`make -C image <target>`); see `docs/design.md` §7.

`guestd.{raw,vhd}` is the ro guest payload volume — it carries BOTH guestd (`/opt/guestd/guestd`)
and the in-guest agent (`/opt/atelier`, code + node_modules), neither baked into the rootfs. It's
attached as a second disk and mounted at `/opt` by `init.sh` (`LABEL=guestd`). Rebuild just it with
`make -C image guestd` / `./build.sh guestd` to iterate on guestd or the agent without rebuilding the
whole image (it does an `npm ci` for the agent, so it's not as instant as the old guestd-only volume).
