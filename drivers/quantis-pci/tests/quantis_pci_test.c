// SPDX-License-Identifier: BSD-3-Clause OR GPL-2.0-only
/* Simulation module for an isolated VM only. Reuses the production driver file
 * operations, wait queues, worker, references and removal; only MMIO is fake. */
#define QUANTIS_TEST_DEVICE
#include "../linux/quantis_pci_linux.c"
#include <linux/capability.h>

#define TEST_FEED _IO('Q', 100)
#define TEST_FAULT _IO('Q', 101)
#define TEST_CLEAR _IO('Q', 102)
#define TEST_REMOVE _IO('Q', 103)
#define TEST_OVERFLOW _IO('Q', 104)

static DEFINE_MUTEX(test_control);
static struct quantis_device *test_device;
static struct {
	u32 modules, word;
	unsigned int available;
	bool overflow;
} test_regs;

static u32 test_read32(void *context, unsigned int offset)
{
	if (offset == Q_MODULE_STATUS)
		return test_regs.modules;
	if (offset == Q_VERSION)
		return 0x040a1201;
	if (offset == Q_FIFO_STATUS) {
		if (test_regs.overflow)
			return Q_FIFO_OVERFLOW;
		return test_regs.available >= 4096 ? Q_FIFO_FULL :
		       test_regs.available >= 3072 ? 8 :
		       test_regs.available >= 2048 ? 4 :
		       test_regs.available >= 1024 ? 2 :
						     1;
	}
	if (offset == Q_FIFO_DATA) {
		u32 n = test_regs.word;
		u32 value = (n & 255) | (((n + 1) & 255) << 8) |
			    (((n + 2) & 255) << 16) | (((n + 3) & 255) << 24);
		if (WARN_ON(test_regs.available < 4))
			return 0;
		test_regs.available -= 4;
		test_regs.word += 4;
		return value;
	}
	return 0;
}
static void test_write32(void *context, unsigned int offset, u32 value)
{
	if (offset == Q_FIFO_FLUSH) {
		test_regs.available = 0;
		test_regs.overflow = false;
	}
}

static long quantis_test_ioctl(struct file *file, unsigned int command,
			       unsigned long argument)
{
	struct quantis_device *q = file->private_data;
	int result = 0;
	if (_IOC_TYPE(command) != 'Q')
		return quantis_ioctl(file, command, argument);
	if (!capable(CAP_SYS_ADMIN))
		return -EPERM;
	mutex_lock(&test_control);
	mutex_lock(&q->lock);
	if (q->removed) {
		result = -ENODEV;
		goto unlock;
	}
	switch (command) {
	case TEST_FEED:
		test_regs.available = min_t(unsigned long, argument, 4096);
		break;
	case TEST_FAULT:
		test_regs.modules = argument ? 0 : 0xc2;
		break;
	case TEST_CLEAR:
		test_regs.available = 0;
		test_regs.word = 0;
		test_regs.overflow = false;
		q->stream.pending_size = 0;
		break;
	case TEST_OVERFLOW:
		test_regs.overflow = true;
		break;
	case TEST_REMOVE:
		mutex_unlock(&q->lock);
		quantis_detach(q);
		test_device = NULL;
		put_device(&q->dev);
		mutex_unlock(&test_control);
		return 0;
	default:
		result = -ENOTTY;
		break;
	}
/* Deliberately no quantis_refresh: tests require the real readiness worker
  * to detect new data/faults and wake epoll and blocking read waiters. */
unlock:
	mutex_unlock(&q->lock);
	mutex_unlock(&test_control);
	return result;
}

static int __init quantis_test_init(void)
{
	struct quantis_device *q;
	int error = quantis_class_init("quantis_pci_test");
	if (error)
		return error;
	q = quantis_allocate(NULL);
	if (IS_ERR(q)) {
		error = PTR_ERR(q);
		goto cleanup;
	}
	q->stream.read32 = test_read32;
	q->stream.write32 = test_write32;
	test_regs.modules = 0xc2;
	test_regs.word = 0;
	error = quantis_register(q, "qrandom-test");
	if (error) {
		ida_free(&quantis_slots, q->slot);
		put_device(&q->dev);
		goto cleanup;
	}
	test_device = q;
	return 0;
cleanup:
	quantis_class_exit();
	return error;
}
static void __exit quantis_test_exit(void)
{
	if (test_device) {
		quantis_detach(test_device);
		put_device(&test_device->dev);
	}
	quantis_class_exit();
}
module_init(quantis_test_init);
module_exit(quantis_test_exit);
