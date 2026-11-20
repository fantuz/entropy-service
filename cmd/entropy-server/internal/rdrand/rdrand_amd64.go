//go:build amd64 && !purego

package rdrand

// Compiled reports whether this binary includes the native instruction path.
const Compiled = true

var available = cpuHasRDRAND()

var seedAvailable = cpuHasRDSEED()

func cpuHasRDRAND() bool

func readUint64() (value uint64, ok bool)

func cpuHasRDSEED() bool

func readSeedUint64() (value uint64, ok bool)
