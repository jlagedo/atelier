# image/bundle

Build output (gitignored), one subdirectory per build TARGET:

```text
windows-amd64-hyperv/   vmlinuz initrd rootfs.vhd guestd.vhd manifest.txt   (make windows / default)
darwin-arm64-vz/        vmlinuz initrd rootfs.raw guestd.raw manifest.txt   (make darwin)
```

Produced by `image/build.sh` (`make -C image <target>`); see `docs/design.md` §7.

`guestd.{raw,vhd}` is guestd's own ro volume (not baked into the rootfs), attached as a second disk
and mounted by `init.sh` (`LABEL=guestd`). Rebuild just it with `make -C image guestd` / `./build.sh
guestd` to iterate on guestd without rebuilding the whole image.
