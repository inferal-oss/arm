#!/bin/sh
# Run only as PID-1-controlled work in a freshly booted disposable Linux VM.
# The caller must match the fresh VM's SMBIOS serial. This script formats and
# mounts only a new sparse image created inside that VM.
set -eu

if [ "$(id -u)" -ne 0 ]; then
    echo 'root inside disposable guest required' >&2
    exit 76
fi
if [ -z "${ARM_SYNC_XFS_GUEST_NONCE:-}" ] || [ "$(cat /sys/class/dmi/id/product_serial)" != "$ARM_SYNC_XFS_GUEST_NONCE" ]; then
    echo 'disposable guest serial witness missing' >&2
    exit 76
fi
if ! command -v systemd-detect-virt >/dev/null 2>&1 || ! systemd-detect-virt --vm >/dev/null 2>&1; then
    echo 'virtual-machine witness missing' >&2
    exit 76
fi
if [ "$#" -ne 2 ] || [ ! -x "$1" ] || [ ! -x "$2/mkfs.xfs" ] ||
    [ ! -x "$2/xfs_quota" ] || [ ! -r "$2/libinih.so.1" ]; then
    echo 'usage: guest-run.sh test-binary pinned-tool-directory' >&2
    exit 64
fi
PATH="$2:$PATH"
LD_LIBRARY_PATH="$2${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export PATH LD_LIBRARY_PATH
for tool in mkfs.xfs xfs_quota mount umount truncate; do
    command -v "$tool" >/dev/null 2>&1 || { echo "missing guest tool: $tool" >&2; exit 76; }
done

fixture_dir=$(mktemp -d /var/tmp/arm-sync-xfs-XXXXXX)
fixture_mount="$fixture_dir/mount"
fixture_image="$fixture_dir/fresh-xfs.img"
mounted=no
cleanup() {
    if [ "$mounted" = yes ]; then
        umount "$fixture_mount" || true
    fi
    rm -rf -- "$fixture_dir"
}
trap cleanup EXIT HUP INT TERM
mkdir -m 0700 "$fixture_mount"
truncate -s 512M "$fixture_image"
mkfs.xfs -f "$fixture_image"
mount -o loop,prjquota "$fixture_image" "$fixture_mount"
mounted=yes
mkdir -m 0700 "$fixture_mount/scope"
mkdir -m 0700 "$fixture_mount/scope/selected"
mkdir -m 0700 "$fixture_mount/scope/home"
xfs_quota -x -c "project -s -p $fixture_mount/scope 42" "$fixture_mount"
xfs_quota -x -c 'limit -p bhard=64m 42' "$fixture_mount"
xfs_quota -x -c "project -c -p $fixture_mount/scope 42" "$fixture_mount"
xfs_quota -x -c 'state -p' "$fixture_mount"
set +e
ARM_SYNC_XFS_DISPOSABLE_GUEST=yes ARM_SYNC_XFS_GUEST_PROJECT="$fixture_mount/scope" "$1" -test.run '^TestNativeChargedScopeGuest$' -test.v -test.count=1 > "$fixture_dir/test.log" 2>&1
test_status=$?
set -e
cat "$fixture_dir/test.log"
echo "ARM_SYNC_TEST_STATUS exit=$test_status"
exit "$test_status"
