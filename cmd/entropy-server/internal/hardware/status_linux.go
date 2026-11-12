package hardware

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// ProbeFileStatus sends read-only Quantis queries only after identifying the
// opened descriptor through sysfs. An arbitrary character device is not queried.
func ProbeFileStatus(file *os.File) (map[string]uint32, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeCharDevice == 0 {
		return map[string]uint32{}, nil
	}

	path, err := filepath.EvalSymlinks(fmt.Sprintf("/sys/dev/char/%d:%d", unix.Major(stat.Rdev), unix.Minor(stat.Rdev)))
	if err != nil {
		// Missing sysfs is an unavailable identity, not permission to guess
		// the ABI. Other errors must remain visible to the operator.
		if errors.Is(err, os.ErrNotExist) {
			return map[string]uint32{}, nil
		}

		return nil, err
	}

	var family string

	for path != "/" && path != "." {
		candidate, _, known := Identify(trimHex(readAttribute(path, "vendor")), trimHex(readAttribute(path, "device")))
		if known && (candidate == LegacyPCI || candidate == ChipPCIe) {
			family = candidate
			break
		}

		path = filepath.Dir(path)
	}

	if family == "" {
		return map[string]uint32{}, nil
	}

	connection, err := file.SyscallConn()
	if err != nil {
		return nil, err
	}

	var (
		status   map[string]uint32
		queryErr error
	)

	err = connection.Control(func(fd uintptr) { status, queryErr = linuxStatus(int(fd), family, unix.IoctlGetUint32) })

	return status, errors.Join(err, queryErr)
}

func linuxStatus(fd int, family string, query func(int, uint) (uint32, error)) (map[string]uint32, error) {
	if family != LegacyPCI && family != ChipPCIe {
		return map[string]uint32{}, nil
	}

	status := map[string]uint32{}

	for _, item := range []struct {
		name string
		code uint
	}{
		{"modules_mask", 0x80047102},
		{"modules_status", 0x80047108},
		{"board_version_raw", 0x80047103},
		{"driver_version_raw", 0x80047100},
	} {
		value, err := query(fd, item.code)
		if err != nil {
			return status, fmt.Errorf("read Quantis status %s: %w", item.name, err)
		}

		status[item.name] = value
	}

	limit := uint32(0xf)
	if family == ChipPCIe {
		limit = 0xfff
	}

	mask := status["modules_mask"]
	if mask == 0 || mask&^limit != 0 || status["modules_status"] != mask {
		return status, errors.New("installed Quantis modules are not all enabled and healthy")
	}

	return status, nil
}

func trimHex(value string) string {
	if len(value) >= 2 && value[:2] == "0x" {
		return value[2:]
	}

	return value
}
