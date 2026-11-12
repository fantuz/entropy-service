/* SPDX-License-Identifier: BSD-3-Clause OR GPL-2.0-only */
#include <assert.h>
#include <stdio.h>
#include <string.h>
#include "../linux/quantis_stream.h"

struct fake {
	u32 modules, word;
	unsigned int available, reads, flushes, overflow_at, fault_at;
	int overflow;
};
static u32 read_register(void *context, unsigned int reg)
{
	struct fake *f = context;
	if (reg == Q_MODULE_STATUS)
		return f->fault_at && f->reads >= f->fault_at ? 0 : f->modules;
	if (reg == Q_FIFO_STATUS) {
		if (f->overflow ||
		    (f->overflow_at && f->reads >= f->overflow_at))
			return Q_FIFO_OVERFLOW;
		return f->available >= 4096 ? Q_FIFO_FULL :
		       f->available >= 3072 ? 8 :
		       f->available >= 2048 ? 4 :
		       f->available >= 1024 ? 2 :
					      1;
	}
	if (reg == Q_FIFO_DATA) {
		assert(f->available >= 4);
		u32 n = f->word;
		f->available -= 4;
		f->reads++;
		f->word += 4;
		return (n & 255) | (((n + 1) & 255) << 8) |
		       (((n + 2) & 255) << 16) | (((n + 3) & 255) << 24);
	}
	assert(!"unexpected register");
	return 0;
}
static void write_register(void *context, unsigned int reg, u32 value)
{
	struct fake *f = context;
	assert(reg == Q_FIFO_FLUSH && value == 0);
	f->flushes++;
	f->available = 0;
	f->overflow = 0;
	f->overflow_at = 0;
}
static struct quantis_stream stream(struct fake *f)
{
	struct quantis_stream s = { .read32 = read_register,
				    .write32 = write_register,
				    .context = f };
	return s;
}
int main(void)
{
	struct fake f = { .modules = 0xc2, .word = 0 };
	struct quantis_stream s = stream(&f);
	u8 data[2048];
	assert(quantis_ready(&s) == Q_WAIT);
	assert(quantis_read_available(&s, data, sizeof(data)) == 0);
	assert(f.reads == 0 && f.flushes == 0);
	f.available = 4096;
	assert(quantis_ready(&s) == Q_READY);
	assert(quantis_read_available(&s, data, 3) == 3);
	assert(data[0] == 0 && data[1] == 1 && data[2] == 2);
	assert(s.pending_size == 1 && f.reads == 1);
	f.available = 0;
	assert(quantis_ready(&s) == Q_READY);
	assert(quantis_read_available(&s, data, 2) == 1 && data[0] == 3);
	assert(quantis_ready(&s) == Q_WAIT);
	f.available = 1024;
	assert(quantis_read_available(&s, data, sizeof(data)) ==
	       1024); /* No busy-wait for the requested remainder. */
	assert(quantis_ready(&s) == Q_WAIT);
	f.modules = 0;
	assert(quantis_ready(&s) == -EIO);
	f.modules = 0xc6; /* Test mode is never a seed source. */
	assert(quantis_ready(&s) == -EIO);
	f.modules = 0xc2;
	f.available = 1024;
	assert(quantis_ready(&s) == Q_WAIT &&
	       f.available == 0); /* Clear stale pre-error bytes. */
	f.overflow = 1;
	assert(quantis_ready(&s) == Q_WAIT && s.overflows == 1);
	f.available = 1024;
	f.overflow_at = f.reads + 3;
	memset(data, 0xff, sizeof(data));
	assert(quantis_read_available(&s, data, 16) == -EIO);
	for (int i = 0; i < 8; i++)
		assert(data[i] == 0);
	assert(s.pending_size == 0 && s.overflows == 2);
	f.available = 1024;
	f.fault_at = f.reads + 2;
	assert(quantis_read_available(&s, data, 16) == -EIO);
	assert(s.unhealthy && s.pending_size == 0);
	puts("Quantis bounded core: empty, partial/stashed bytes, starvation, module/test faults, overflow recovery and failed-batch discard passed");
	return 0;
}
