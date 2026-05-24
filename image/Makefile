# Thin wrapper over build.sh. See design.md §7 for the image spec.
# TARGET selects the platform bundle (default: the Windows/Hyper-V one). Override per
# invocation (`make all TARGET=darwin-arm64-vz`) or use the `windows`/`darwin` shortcuts.
.PHONY: all check rootfs initrd kernel guestd bundle clean windows darwin
TARGET ?= windows-amd64-hyperv

all:    ; TARGET=$(TARGET) ./build.sh all
check:  ; TARGET=$(TARGET) ./build.sh check
rootfs: ; TARGET=$(TARGET) ./build.sh rootfs
initrd: ; TARGET=$(TARGET) ./build.sh initrd
kernel: ; TARGET=$(TARGET) ./build.sh kernel
guestd: ; TARGET=$(TARGET) ./build.sh guestd
bundle: ; TARGET=$(TARGET) ./build.sh bundle
clean:  ; TARGET=$(TARGET) ./build.sh clean

windows: ; $(MAKE) all TARGET=windows-amd64-hyperv
darwin:  ; $(MAKE) all TARGET=darwin-arm64-vz
