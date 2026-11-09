// SPDX-License-Identifier: MIT
// Local adapter only; TestU01 remains an external statistical-tool dependency.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include "unif01.h"
#include "bbattery.h"
static unsigned long long words = 0;
static unsigned int next_word(void) {
 unsigned char b[4];
 if (fread(b, 1, 4, stdin) != 4) {
  fprintf(stderr,"INCOMPLETE: input EOF after %llu 32-bit words\n",words);
  exit(2);
 }
 ++words;
 return (unsigned int)((uint32_t)b[0] | ((uint32_t)b[1]<<8) | ((uint32_t)b[2]<<16) | ((uint32_t)b[3]<<24));
}
int main(void) {
 unif01_Gen *gen=unif01_CreateExternGenBits("entropy-service decoded bytes, little-endian 32-bit words",next_word);
 bbattery_SmallCrush(gen);
 fprintf(stderr,"COMPLETE: %llu words, %llu bytes consumed\n",words,words*4);
 unif01_DeleteExternGenBits(gen);
 return 0;
}
