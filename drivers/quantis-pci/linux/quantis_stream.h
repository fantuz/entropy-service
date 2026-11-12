/* SPDX-License-Identifier: BSD-3-Clause OR GPL-2.0-only */
/* Register semantics: ID Quantique's common/quantis_pci_common.h in the pinned
 * source. This bounded core is shared by the Linux driver and simulated tests.
 * Callers serialize every access, including reset, status and pending bytes. */
#ifndef QUANTIS_STREAM_H
#define QUANTIS_STREAM_H
#ifdef __KERNEL__
#include <linux/types.h>
#include <linux/errno.h>
#else
#include <stdint.h>
#include <stddef.h>
#include <errno.h>
typedef uint32_t u32;
typedef unsigned char u8;
#endif

enum {
	Q_CORE_ENABLE = 0,
	Q_CORE_DISABLE = 4,
	Q_VERSION = 28,
	Q_FIFO_FLUSH = 32,
	Q_FIFO_STATUS = 40,
	Q_FIFO_DATA = 44,
	Q_MODULE_ENABLE = 48,
	Q_MODULE_DISABLE = 52,
	Q_MODULE_STATUS = 60,
	Q_FIFO_OVERFLOW = 1 << 7,
	Q_FIFO_FULL = 1 << 6,
	Q_WAIT = 0,
	Q_READY = 1
};

struct quantis_stream {
	u32 (*read32)(void *, unsigned int);
	void (*write32)(void *, unsigned int, u32);
	void *context;
	u8 pending[4];
	unsigned int pending_at, pending_size, unhealthy;
	unsigned long overflows;
};

static inline unsigned int quantis_mask(u32 reg, unsigned int bit)
{
	return ((reg >> bit) & 1) | (((reg >> (bit + 8)) & 1) << 1) |
	       (((reg >> (bit + 16)) & 1) << 2) |
	       (((reg >> (bit + 24)) & 1) << 3);
}

static inline void quantis_discard(struct quantis_stream *s)
{
	s->pending_at = s->pending_size = 0;
	s->write32(s->context, Q_FIFO_FLUSH, 0);
}

static inline unsigned int quantis_fifo_bytes(u32 status)
{
	if (status & Q_FIFO_FULL)
		return 4096;
	if (status & (1 << 3))
		return 3072;
	if (status & (1 << 2))
		return 2048;
	if (status & (1 << 1))
		return 1024;
	return 0; /* Conservative: hardware exposes quarter-full thresholds. */
}

static inline int quantis_health(struct quantis_stream *s)
{
	u32 modules = s->read32(s->context, Q_MODULE_STATUS);
	unsigned int enabled = quantis_mask(modules, 1);
	unsigned int healthy = quantis_mask(modules, 7);
	unsigned int present = quantis_mask(modules, 6);
	/* Never seed from test mode or an enabled but unhealthy/absent module. */
	if (!enabled || quantis_mask(modules, 2) ||
	    (enabled & healthy & present) != enabled)
		return -EIO;
	return 0;
}

static inline int quantis_ready(struct quantis_stream *s)
{
	u32 status;
	if (quantis_health(s)) {
		s->pending_at = s->pending_size = 0;
		s->unhealthy = 1;
		return -EIO;
	}
	if (s->unhealthy) {
		s->unhealthy = 0;
		quantis_discard(s);
		return Q_WAIT;
	}
	status = s->read32(s->context, Q_FIFO_STATUS);
	if (status & Q_FIFO_OVERFLOW) {
		/* An idle producer can fill its FIFO. Preserve the historical recovery
   * policy, but discard before advertising any bytes from a fresh window.
   * Overflow during an actual read instead fails that whole read below. */
		s->overflows++;
		quantis_discard(s);
		return Q_WAIT;
	}
	return s->pending_size || quantis_fifo_bytes(status) ? Q_READY : Q_WAIT;
}

static inline int quantis_read_available(struct quantis_stream *s, u8 *out,
					 unsigned int length)
{
	unsigned int used = 0, available;
	int state = quantis_ready(s);
	if (state != Q_READY)
		return state;
	available = quantis_fifo_bytes(s->read32(s->context, Q_FIFO_STATUS));
	while (used < length && (s->pending_size || available >= 4)) {
		if (!s->pending_size) {
			u32 value = s->read32(s->context, Q_FIFO_DATA);
			unsigned int i;
			/* Explicit little-endian bytes; no host-endian or cacheable MMIO loads. */
			for (i = 0; i < 4; i++)
				s->pending[i] = value >> (i * 8);
			s->pending_at = 0;
			s->pending_size = 4;
			available -= 4;
			if (s->read32(s->context, Q_FIFO_STATUS) &
			    Q_FIFO_OVERFLOW) {
				s->overflows++;
				quantis_discard(s);
				while (used)
					out[--used] = 0;
				return -EIO;
			}
		}
		out[used++] = s->pending[s->pending_at++];
		s->pending_size--;
	}
	if (s->read32(s->context, Q_FIFO_STATUS) & Q_FIFO_OVERFLOW) {
		s->overflows++;
		quantis_discard(s);
		while (used)
			out[--used] = 0;
		return -EIO;
	}
	if (quantis_health(s)) {
		s->pending_at = s->pending_size = 0;
		s->unhealthy = 1;
		while (used)
			out[--used] = 0;
		return -EIO;
	}

	return (int)used;
}
#endif
