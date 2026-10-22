//go:build !amd64 || purego

package rdrand

const available = false

const seedAvailable = false

func readUint64() (uint64, bool) {
	return 0, false
}

func readSeedUint64() (uint64, bool) {
	return 0, false
}
