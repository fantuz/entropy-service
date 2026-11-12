/*
 * Quantis PCI driver
 *
 * Copyright (C) 2004-2012 ID Quantique SA, Carouge/Geneva, Switzerland
 * All rights reserved.
 *
 * ----------------------------------------------------------------------------
 *
 * Redistribution and use in source and binary forms, with or without
 * modification, are permitted provided that the following conditions
 * are met:
 * 1. Redistributions of source code must retain the above copyright
 *    notice, this list of conditions, and the following disclaimer,
 *    without modification.
 * 2. Redistributions in binary form must reproduce the above copyright
 *    notice, this list of conditions and the following disclaimer in the
 *    documentation and/or other materials provided with the distribution.
 * 3. The name of the author may not be used to endorse or promote products
 *    derived from this software without specific prior written permission.
 *
 * THIS SOFTWARE IS PROVIDED BY THE AUTHOR AND CONTRIBUTORS ``AS IS'' AND
 * ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
 * IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
 * ARE DISCLAIMED. IN NO EVENT SHALL THE AUTHOR OR CONTRIBUTORS BE LIABLE FOR
 * ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
 * DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS
 * OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION)
 * HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT
 * LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY.
 *
 * ----------------------------------------------------------------------------
 *
 * Alternatively, this software may be distributed under the terms of the
 * terms of the GNU General Public License version 2 as published by the
 * Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but
 * WITHOUT ANY WARRANTY; without even the implied warranty of MERCHANTABILITY
 * or FITNESS FOR A PARTICULAR PURPOSE. See the GNU General Public License
 * for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, write to the Free Software
 * Foundation, Inc., 59 Temple Place, Suite 330, Boston, MA  02111-1307 USA
 *
 * ----------------------------------------------------------------------------
 *
 * For history of changes, ChangeLog.txt
 */

/* Linux 6.1+ bounded-read overlay. See ../README.md for source provenance. */
#include <linux/cdev.h>
#include <linux/compat.h>
#include <linux/device.h>
#include <linux/fs.h>
#include <linux/idr.h>
#include <linux/io.h>
#include <linux/jiffies.h>
#include <linux/module.h>
#include <linux/mutex.h>
#include <linux/pci.h>
#include <linux/poll.h>
#include <linux/proc_fs.h>
#include <linux/seq_file.h>
#include <linux/slab.h>
#include <linux/uaccess.h>
#include <linux/version.h>
#include <linux/workqueue.h>
#include "../common/quantis_pci.h"
#include "quantis_stream.h"

#define QUANTIS_READ_MAX 1024
#define QUANTIS_CHECK_INTERVAL max_t(unsigned long, 1, msecs_to_jiffies(1))

struct quantis_device {
	struct device dev;
	struct cdev cdev;
	struct mutex lock;
	wait_queue_head_t readers;
	struct delayed_work check_work;
	struct quantis_stream stream;
	void __iomem *regs;
	struct pci_dev *pci;
	__poll_t ready;
	unsigned int slot;
	unsigned int bus_device;
	bool removed, suspended, enabled;
};

static dev_t quantis_number;
static struct class *quantis_class;
static DEFINE_IDA(quantis_slots);
static DEFINE_MUTEX(quantis_table_lock);
static struct quantis_device *quantis_devices[QUANTIS_PCI_MAX_CARDS];
static unsigned int quantis_count;

/* All register accesses and removal are serialized by lock. Wakeups use only
 * cached state; .poll and the wait predicate never sleep or touch unmapped MMIO. */
static void quantis_publish(struct quantis_device *q, __poll_t state)
{
	__poll_t previous = READ_ONCE(q->ready);
	WRITE_ONCE(q->ready, state);
	if (state && state != previous)
		wake_up_interruptible_poll(&q->readers, state);
}

static void quantis_refresh(struct quantis_device *q)
{
	int state;
	if (q->removed) {
		quantis_publish(q, EPOLLERR | EPOLLHUP);
		return;
	}
	if (q->suspended) {
		quantis_publish(q, EPOLLERR);
		return;
	}
	state = quantis_ready(&q->stream);
	quantis_publish(q, state < 0 ? EPOLLERR :
			   state     ? EPOLLIN | EPOLLRDNORM :
				       0);
}

static void quantis_check(struct work_struct *work)
{
	struct quantis_device *q = container_of(
		to_delayed_work(work), struct quantis_device, check_work);
	mutex_lock(&q->lock);
	if (!q->removed && !q->suspended) {
		quantis_refresh(q);
		schedule_delayed_work(&q->check_work, QUANTIS_CHECK_INTERVAL);
	}
	mutex_unlock(&q->lock);
}

static void quantis_free(struct device *dev)
{
	struct quantis_device *q =
		container_of(dev, struct quantis_device, dev);
	kfree(q);
}

static int quantis_open(struct inode *inode, struct file *file)
{
	struct quantis_device *q =
		container_of(inode->i_cdev, struct quantis_device, cdev);
	/* cdev_device_add pins the parent device while cdev is reachable, including
  * an .open already racing cdev_device_del. Each open takes its own reference. */
	get_device(&q->dev);
	if (mutex_lock_interruptible(&q->lock)) {
		put_device(&q->dev);
		return -ERESTARTSYS;
	}
	if (q->removed) {
		mutex_unlock(&q->lock);
		put_device(&q->dev);
		return -ENODEV;
	}
	file->private_data = q;
	mutex_unlock(&q->lock);
	return nonseekable_open(inode, file);
}

static int quantis_close(struct inode *inode, struct file *file)
{
	struct quantis_device *q = file->private_data;
	put_device(&q->dev);
	return 0;
}

static ssize_t quantis_read(struct file *file, char __user *buffer,
			    size_t length, loff_t *position)
{
	struct quantis_device *q = file->private_data;
	u8 *sample;
	int count, result;
	unsigned long remaining;
	if (!length)
		return 0;
	length = min_t(size_t, length, QUANTIS_READ_MAX);
	/* Keep the kernel stack bounded even on CONFIG_FRAME_WARN=1024 kernels.
  * Nonblocking callers never wait for memory reclaim or for sample data. */
	sample = kmalloc(length,
			 file->f_flags & O_NONBLOCK ? GFP_NOWAIT : GFP_KERNEL);
	if (!sample)
		return -ENOMEM;
	for (;;) {
		if (file->f_flags & O_NONBLOCK) {
			if (!mutex_trylock(&q->lock)) {
				result = -EAGAIN;
				goto free;
			}
		} else if (mutex_lock_interruptible(&q->lock)) {
			result = -ERESTARTSYS;
			goto free;
		}
		if (q->removed) {
			mutex_unlock(&q->lock);
			result = -ENODEV;
			goto free;
		}
		if (q->suspended) {
			mutex_unlock(&q->lock);
			result = -EIO;
			goto free;
		}
		count = quantis_read_available(&q->stream, sample, length);
		quantis_refresh(q);
		mutex_unlock(&q->lock);
		if (count < 0) {
			result = count;
			goto free;
		}
		if (count)
			break;
		if (file->f_flags & O_NONBLOCK) {
			result = -EAGAIN;
			goto free;
		}
		result = wait_event_interruptible(q->readers,
						  READ_ONCE(q->ready));
		if (result)
			goto free;
	}
	/* User faults must not hold the MMIO/removal mutex. VFS holds the file until
  * .read returns, even if another thread closes the descriptor. */
	remaining = copy_to_user(buffer, sample, count);
	result = remaining == count ? -EFAULT : count - remaining;
free:
	kfree_sensitive(sample);
	return result;
}

static __poll_t quantis_poll(struct file *file, poll_table *table)
{
	struct quantis_device *q = file->private_data;
	poll_wait(file, &q->readers, table);
	return READ_ONCE(q->ready);
}

static long quantis_ioctl(struct file *file, unsigned int command,
			  unsigned long argument)
{
	struct quantis_device *q = file->private_data;
	u32 value = 0;
	int result = 0;
	bool output = true;
	if (mutex_lock_interruptible(&q->lock))
		return -ERESTARTSYS;
	if (q->removed) {
		result = -ENODEV;
		goto out;
	}
	if (q->suspended) {
		result = -EIO;
		goto out;
	}
	switch (command) {
	case QUANTIS_IOCTL_GET_DRIVER_VERSION:
		value = QUANTIS_PCI_DRIVER_VERSION;
		break;
	case QUANTIS_IOCTL_GET_CARD_COUNT:
		value = READ_ONCE(quantis_count);
		break;
	case QUANTIS_IOCTL_GET_BOARD_VERSION:
		value = q->stream.read32(q->stream.context, Q_VERSION);
		break;
	case QUANTIS_IOCTL_GET_MODULES_MASK:
		value = quantis_mask(q->stream.read32(q->stream.context,
						      Q_MODULE_STATUS),
				     6);
		break;
	case QUANTIS_IOCTL_GET_MODULES_STATUS:
		value = quantis_mask(q->stream.read32(q->stream.context,
						      Q_MODULE_STATUS),
				     1) &
			quantis_mask(q->stream.read32(q->stream.context,
						      Q_MODULE_STATUS),
				     7);
		break;
	case QUANTIS_IOCTL_GET_PCI_BUS_DEVICE_ID:
		value = q->bus_device;
		break;
	case QUANTIS_IOCTL_RESET_BOARD:
		q->stream.write32(q->stream.context, Q_CORE_ENABLE, 1);
		q->stream.write32(q->stream.context, Q_CORE_DISABLE, 1);
		quantis_discard(&q->stream);
		output = false;
		break;
	case QUANTIS_IOCTL_ENABLE_MODULE:
	case QUANTIS_IOCTL_DISABLE_MODULE:
		/* Copy outside the device lock: userfaultfd must not stall unplug. */
		mutex_unlock(&q->lock);
		if (get_user(value, (u32 __user *)argument))
			return -EFAULT;
		if (value & ~0xfU)
			return -EINVAL;
		if (mutex_lock_interruptible(&q->lock))
			return -ERESTARTSYS;
		if (q->removed) {
			result = -ENODEV;
			goto out;
		}
		if (q->suspended) {
			result = -EIO;
			goto out;
		}
		value = (value & 1) | ((value & 2) << 7) | ((value & 4) << 14) |
			((value & 8) << 21);
		q->stream.write32(q->stream.context,
				  command == QUANTIS_IOCTL_ENABLE_MODULE ?
					  Q_MODULE_ENABLE :
					  Q_MODULE_DISABLE,
				  value << 1);
		quantis_discard(&q->stream);
		output = false;
		break;
	default:
		result = -ENOTTY;
		output = false;
		break;
	}
	quantis_refresh(q);
out:
	mutex_unlock(&q->lock);
	if (!result && output)
		result = put_user(value, (u32 __user *)argument);
	return result;
}

#ifdef QUANTIS_TEST_DEVICE
static long quantis_test_ioctl(struct file *, unsigned int, unsigned long);
#define QUANTIS_IOCTL_FN quantis_test_ioctl
#else
#define QUANTIS_IOCTL_FN quantis_ioctl
#endif

static const struct file_operations quantis_fops = {
	.owner = THIS_MODULE,
	.open = quantis_open,
	.release = quantis_close,
	.read = quantis_read,
	.poll = quantis_poll,
	.unlocked_ioctl = QUANTIS_IOCTL_FN,
#ifdef CONFIG_COMPAT
	.compat_ioctl = compat_ptr_ioctl,
#endif

};

static struct quantis_device *quantis_allocate(struct device *parent)
{
	struct quantis_device *q;
	int slot = ida_alloc_max(&quantis_slots, QUANTIS_PCI_MAX_CARDS - 1,
				 GFP_KERNEL);
	if (slot < 0)
		return ERR_PTR(slot);
	q = kzalloc(sizeof(*q), GFP_KERNEL);
	if (!q) {
		ida_free(&quantis_slots, slot);
		return ERR_PTR(-ENOMEM);
	}
	q->slot = slot;
	mutex_init(&q->lock);
	init_waitqueue_head(&q->readers);
	INIT_DELAYED_WORK(&q->check_work, quantis_check);
	device_initialize(&q->dev);
	q->dev.release = quantis_free;
	q->dev.devt = MKDEV(MAJOR(quantis_number), slot);
	q->dev.class = quantis_class;
	q->dev.parent = parent;
	cdev_init(&q->cdev, &quantis_fops);
	q->cdev.owner = THIS_MODULE;
	return q;
}

static int quantis_register(struct quantis_device *q, const char *name)
{
	int error = dev_set_name(&q->dev, "%s%u", name, q->slot);
	if (!error)
		error = cdev_device_add(&q->cdev, &q->dev);
	if (error) {
		/* cdev_device_add can expose .open even if device_add subsequently fails. */
		mutex_lock(&q->lock);
		q->removed = true;
		quantis_publish(q, EPOLLHUP | EPOLLERR);
		mutex_unlock(&q->lock);
		return error;
	}
	mutex_lock(&quantis_table_lock);
	quantis_devices[q->slot] = q;
	quantis_count++;
	mutex_unlock(&quantis_table_lock);
	schedule_delayed_work(&q->check_work, 0);
	return 0;
}

static void quantis_detach(struct quantis_device *q)
{
	mutex_lock(&quantis_table_lock);
	quantis_devices[q->slot] = NULL;
	quantis_count--;
	mutex_lock(&q->lock);
	q->removed = true;
	quantis_publish(q, EPOLLHUP | EPOLLERR);
	mutex_unlock(&q->lock);
	mutex_unlock(&quantis_table_lock);
	/* Never hold lock across cancel_delayed_work_sync: a running worker needs it. */
	cancel_delayed_work_sync(&q->check_work);
	cdev_device_del(&q->cdev, &q->dev);
	ida_free(&quantis_slots, q->slot);
	/* Caller releases MMIO before dropping the probe-owned device reference. */
}

static int quantis_class_init(const char *name)
{
	int error = alloc_chrdev_region(&quantis_number, 0,
					QUANTIS_PCI_MAX_CARDS, name);
	if (error)
		return error;
#if LINUX_VERSION_CODE >= KERNEL_VERSION(6, 4, 0)
	quantis_class = class_create(name);
#else
	quantis_class = class_create(THIS_MODULE, name);
#endif
	if (IS_ERR(quantis_class)) {
		error = PTR_ERR(quantis_class);
		unregister_chrdev_region(quantis_number, QUANTIS_PCI_MAX_CARDS);
		return error;
	}
	return 0;
}

static void quantis_class_exit(void)
{
	class_destroy(quantis_class);
	unregister_chrdev_region(quantis_number, QUANTIS_PCI_MAX_CARDS);
	ida_destroy(&quantis_slots);
}

#ifndef QUANTIS_TEST_DEVICE
static u32 quantis_mmio_read(void *context, unsigned int offset)
{
	struct quantis_device *q = context;
	if (pci_dev_is_disconnected(q->pci))
		return ~0U;
	return readl(q->regs + offset);
}

static void quantis_mmio_write(void *context, unsigned int offset, u32 value)
{
	struct quantis_device *q = context;
	if (!pci_dev_is_disconnected(q->pci))
		writel(value, q->regs + offset);
}

static int quantis_probe(struct pci_dev *pci, const struct pci_device_id *id)
{
	struct quantis_device *q = quantis_allocate(&pci->dev);
	int error;
	if (IS_ERR(q))
		return PTR_ERR(q);
	q->pci = pci;
	q->bus_device = pci->bus->number;
	error = pci_enable_device_mem(pci);
	if (error)
		goto free;
	q->enabled = true;
	if (!(pci_resource_flags(pci, 1) & IORESOURCE_MEM) ||
	    pci_resource_len(pci, 1) < 64) {
		error = -ENODEV;
		goto disable;
	}
	error = pci_request_region(pci, 1, "quantis_pci");
	if (error)
		goto disable;
	q->regs =
		pci_iomap(pci, 1, 64); /* Uncached MMIO; never ioremap_cache. */
	if (!q->regs) {
		error = -ENOMEM;
		goto release;
	}
	q->stream.context = q;
	q->stream.read32 = quantis_mmio_read;
	q->stream.write32 = quantis_mmio_write;
	q->stream.write32(q, Q_CORE_ENABLE, 1);
	q->stream.write32(q, Q_CORE_DISABLE, 1);
	quantis_discard(&q->stream);
	error = quantis_register(q, QUANTIS_PCI_DEVICE_NAME);
	if (error)
		goto unmap;
	pci_set_drvdata(pci, q);
	dev_info(
		&pci->dev,
		"bounded Quantis PCI reader /dev/qrandom%u, core 0x%08x; periodic readiness, no IRQ assumed\n",
		q->slot, quantis_mmio_read(q, Q_VERSION));
	return 0;
unmap:
	pci_iounmap(pci, q->regs);
release:
	pci_release_region(pci, 1);
disable:
	pci_disable_device(pci);
free:
	ida_free(&quantis_slots, q->slot);
	put_device(&q->dev);
	return error;
}

static void quantis_remove(struct pci_dev *pci)
{
	struct quantis_device *q = pci_get_drvdata(pci);
	quantis_detach(q);
	pci_iounmap(pci, q->regs);
	pci_release_region(pci, 1);
	if (q->enabled)
		pci_disable_device(pci);
	pci_set_drvdata(pci, NULL);
	put_device(&q->dev);
}

static int quantis_suspend(struct device *dev)
{
	struct pci_dev *pci = to_pci_dev(dev);
	struct quantis_device *q = pci_get_drvdata(pci);
	mutex_lock(&q->lock);
	q->suspended = true;
	quantis_publish(q, EPOLLERR);
	mutex_unlock(&q->lock);
	cancel_delayed_work_sync(&q->check_work);
	pci_save_state(pci);
	pci_disable_device(pci);
	q->enabled = false;
	return 0;
}

static int quantis_resume(struct device *dev)
{
	struct pci_dev *pci = to_pci_dev(dev);
	struct quantis_device *q = pci_get_drvdata(pci);
	int error = pci_enable_device_mem(pci);
	if (error)
		return error;
	pci_restore_state(pci);
	mutex_lock(&q->lock);
	q->enabled = true;
	quantis_discard(&q->stream);
	q->suspended = false;
	quantis_refresh(q);
	mutex_unlock(&q->lock);
	schedule_delayed_work(&q->check_work, 0);
	return 0;
}
static SIMPLE_DEV_PM_OPS(quantis_pm, quantis_suspend, quantis_resume);

static int quantis_proc_show(struct seq_file *m, void *unused)
{
	unsigned int i;
	mutex_lock(&quantis_table_lock);
	seq_printf(
		m,
		"quantis_pci bounded-read overlay; source driver ABI 2.6\ncards: %u\n",
		quantis_count);
	for (i = 0; i < QUANTIS_PCI_MAX_CARDS; i++) {
		struct quantis_device *q = quantis_devices[i];
		if (!q)
			continue;
		mutex_lock(&q->lock);
		seq_printf(
			m,
			"qrandom%u readiness=0x%x recovered_fifo_overflows=%lu\n",
			i, q->ready, q->stream.overflows);
		mutex_unlock(&q->lock);
	}
	mutex_unlock(&quantis_table_lock);
	return 0;
}
DEFINE_PROC_SHOW_ATTRIBUTE(quantis_proc);
static const struct pci_device_id quantis_ids[] = { { PCI_DEVICE(0x179a,
								 0x0001) },
						    { 0 } };
MODULE_DEVICE_TABLE(pci, quantis_ids);
static struct pci_driver quantis_driver = {
	.name = "quantis_pci",
	.id_table = quantis_ids,
	.probe = quantis_probe,
	.remove = quantis_remove,
	.driver = { .pm = pm_sleep_ptr(&quantis_pm) },
};

static int __init quantis_init(void)
{
	int error = quantis_class_init("qrandom");
	if (error)
		return error;
	if (!proc_create("qrandom", 0444, NULL, &quantis_proc_proc_ops)) {
		error = -ENOMEM;
		goto cleanup;
	}
	error = pci_register_driver(&quantis_driver);
	if (!error)
		return 0;
	remove_proc_entry("qrandom", NULL);
cleanup:
	quantis_class_exit();
	return error;
}
static void __exit quantis_exit(void)
{
	pci_unregister_driver(&quantis_driver);
	remove_proc_entry("qrandom", NULL);
	quantis_class_exit();
}
module_init(quantis_init);
module_exit(quantis_exit);
#endif
MODULE_LICENSE("Dual BSD/GPL");
MODULE_DESCRIPTION(
	"Legacy Quantis PCI bounded nonblocking seed reader (experimental overlay)");
MODULE_AUTHOR("ID Quantique SA; entropy-service contributors");

MODULE_VERSION("2.6-entropy1");
