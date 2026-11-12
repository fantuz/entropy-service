#!/bin/sh
# Only run inside the isolated VM: this loads a simulated device, never PCI.
set -eu
build_dir=$1
deadline_binary=$2
log_before=$(dmesg | wc -l)
for iteration in 1 2 3; do
    insmod "$build_dir/tests/quantis_pci_test.ko"
    python3 "$build_dir/tests/fileops_test.py" "$deadline_binary"
    rmmod quantis_pci_test
    rm -f /dev/qrandom-test0
done
dmesg | tail -n "+$((log_before + 1))" > "$build_dir/kernel-test.log"
if rg 'BUG:|Oops:|WARNING:|KASAN:|general protection fault' "$build_dir/kernel-test.log"; then
    exit 1
fi
printf '%s\n' 'QUANTIS_VM_TESTS_PASSED'
