// firstNonZeroAVX2 scans buf for the first non-zero byte using AVX2.
//
// Each iteration loads 4 × 32-byte vectors (128 bytes), ORs them together,
// and tests the result against zero with vptest. On a hit we narrow down to
// the byte that broke zero with a scalar tail.
//
// func firstNonZeroAVX2(buf *byte, n int) int
//   returns the byte index of the first non-zero byte, or -1 if all zero.
//   n MUST be a multiple of 128.

#include "textflag.h"

TEXT ·firstNonZeroAVX2(SB), NOSPLIT, $0-24
	MOVQ    buf+0(FP),  SI      // SI = base pointer
	MOVQ    n+8(FP),    CX      // CX = length
	XORQ    AX,         AX      // AX = current offset

	VPXOR   Y4, Y4, Y4          // Y4 = zero (for vptest)

loop128:
	CMPQ    AX, CX
	JGE     notfound
	VMOVDQU (SI)(AX*1),   Y0
	VMOVDQU 32(SI)(AX*1), Y1
	VMOVDQU 64(SI)(AX*1), Y2
	VMOVDQU 96(SI)(AX*1), Y3
	VPOR    Y1, Y0, Y0
	VPOR    Y3, Y2, Y2
	VPOR    Y2, Y0, Y0
	VPTEST  Y0, Y0              // ZF=1 iff Y0 is all zero
	JNZ     found128            // jump if Y0 != 0
	ADDQ    $128, AX
	JMP     loop128

found128:
	// Scalar tail: find the exact byte within the 128-byte window starting
	// at SI+AX.
	MOVQ    $0, DX              // DX = byte offset within window
tailloop:
	MOVBLZX (SI)(AX*1), BX
	TESTB   BX, BX
	JNZ     hit
	INCQ    AX
	INCQ    DX
	CMPQ    DX, $128
	JL      tailloop
	// Should not happen: vptest said non-zero but we didn't find it.
	MOVQ    $-1, AX
	JMP     done

hit:
	JMP     done

notfound:
	MOVQ    $-1, AX

done:
	VZEROUPPER
	MOVQ    AX, ret+16(FP)
	RET
