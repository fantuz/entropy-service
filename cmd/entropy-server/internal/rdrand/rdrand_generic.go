//go:build !amd64 || purego

package rdrand

// Compiled reports whether this binary includes the native instruction path.
const Compiled = false

const available = false

const seedAvailable = false

func readUint64() (uint64, bool) {
	return 0, false
}

func readSeedUint64() (uint64, bool) {
	return 0, false
}
