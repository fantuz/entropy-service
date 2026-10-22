//go:build amd64 && !purego

#include "textflag.h"

// CPUID leaf 1, ECX bit 30 advertises RDRAND support.
TEXT ·cpuHasRDRAND(SB), NOSPLIT, $0-1
	MOVL $1, AX
	XORL CX, CX
	CPUID
	SHRL $30, CX
	ANDL $1, CX
	MOVB CL, ret+0(FP)
	RET

// One initial attempt plus ten retries. The Go wrapper checks CPU support.
TEXT ·readUint64(SB), NOSPLIT, $0-9
	MOVL $11, CX
retry:
	RDRANDQ AX
	JCS success
	PAUSE
	DECL CX
	JNZ retry
	MOVQ $0, value+0(FP)
	MOVB $0, ok+8(FP)
	RET
success:
	MOVQ AX, value+0(FP)
	MOVB $1, ok+8(FP)
	RET

// Check the maximum basic leaf before querying leaf 7, subleaf 0, EBX bit 18.
TEXT ·cpuHasRDSEED(SB), NOSPLIT, $0-1
	XORL AX, AX
	CPUID
	CMPL AX, $7
	JCS noRDSEED
	MOVL $7, AX
	XORL CX, CX
	CPUID
	SHRL $18, BX
	ANDL $1, BX
	MOVB BL, ret+0(FP)
	RET
noRDSEED:
	MOVB $0, ret+0(FP)
	RET

// RDSEED availability is checked in Go; retry exhaustion permits fallback.
TEXT ·readSeedUint64(SB), NOSPLIT, $0-9
	MOVL $11, CX
seedRetry:
	RDSEEDQ AX
	JCS seedSuccess
	PAUSE
	DECL CX
	JNZ seedRetry
	MOVQ $0, value+0(FP)
	MOVB $0, ok+8(FP)
	RET
seedSuccess:
	MOVQ AX, value+0(FP)
	MOVB $1, ok+8(FP)
	RET
