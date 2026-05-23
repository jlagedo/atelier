# image/bundle

Build output (gitignored), one subdirectory per build TARGET:

```text
windows-amd64-hyperv/   vmlinuz initrd rootfs.vhd manifest.txt   (make windows / default)
darwin-arm64-vz/        vmlinuz initrd rootfs.raw manifest.txt   (make darwin)
```

Produced by `image/build.sh` (`make -C image <target>`); see `docs/design.md` §7.
