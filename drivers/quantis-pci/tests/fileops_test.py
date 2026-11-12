#!/usr/bin/env python3
"""Exercise actual kernel file operations of the simulated device in a VM."""
import errno
import fcntl
import os
from pathlib import Path
import select
import signal
import stat
import subprocess
import sys
import threading
import time

PATH = "/dev/qrandom-test0"
FEED, FAULT, CLEAR, REMOVE, OVERFLOW = [ord('Q') << 8 | number for number in range(100, 105)]


def expect_error(expected, call):
    try:
        call()
    except OSError as error:
        assert error.errno == expected, error
    else:
        raise AssertionError(f"expected errno {expected}")


def main():
    if not Path(PATH).exists():
        major, minor = map(int, Path('/sys/class/quantis_pci_test/qrandom-test0/dev').read_text().split(':'))
        os.mknod(PATH, stat.S_IFCHR | 0o600, os.makedev(major, minor))
    fd = os.open(PATH, os.O_RDONLY | os.O_NONBLOCK)
    blocking = os.open(PATH, os.O_RDONLY)
    epoll = select.epoll()
    try:
        fcntl.ioctl(fd, CLEAR, 0)
        epoll.register(fd, select.EPOLLIN | select.EPOLLET)
        assert not epoll.poll(.05), "empty FIFO falsely advertised readiness"
        began = time.monotonic()
        expect_error(errno.EAGAIN, lambda: os.read(fd, 65536))
        assert time.monotonic()-began < .5
        assert os.read(fd, 0) == b''
        fcntl.ioctl(fd, FEED, 1024)
        events = epoll.poll(1)
        assert events and events[0][1] & select.EPOLLIN, events
        assert os.read(fd, 65536) == bytes(range(256))*4, "read must return a valid short prefix without waiting to fill 64 KiB"
        assert not epoll.poll(.05)
        fcntl.ioctl(fd, CLEAR, 0)
        fcntl.ioctl(fd, FEED, 1024)
        assert epoll.poll(1)
        assert os.read(fd, 3) == bytes([0,1,2])
        assert os.read(fd, 1) == bytes([3]), "pending word bytes were lost"
        fcntl.ioctl(fd, CLEAR, 0)
        time.sleep(.03)
        epoll.poll(0)
        subprocess.run([sys.argv[1]], check=True, timeout=5)

        # A real interruptible kernel wait, not a user-space polling loop.
        def interrupt(_number, _frame):
            raise TimeoutError("signal interrupted blocking read")
        old = signal.signal(signal.SIGALRM, interrupt)
        signal.setitimer(signal.ITIMER_REAL, .05)
        try:
            try:
                os.read(blocking, 64)
            except TimeoutError:
                pass
            else:
                raise AssertionError("blocking read did not wait for data")
        finally:
            signal.setitimer(signal.ITIMER_REAL, 0)
            signal.signal(signal.SIGALRM, old)

        def waiter():
            try:
                results.append(os.read(blocking, 4096))
            except OSError as error:
                results.append(error.errno)
        results = []
        thread = threading.Thread(target=waiter)
        thread.start();time.sleep(.03)
        assert not results
        fcntl.ioctl(fd, FEED, 1024)
        thread.join(1)
        assert not thread.is_alive() and results == [bytes(range(256))*4], results
        results.clear()
        thread = threading.Thread(target=waiter)
        thread.start();time.sleep(.03)
        fcntl.ioctl(fd, FAULT, 1)
        assert epoll.poll(1)[0][1] & select.EPOLLERR
        thread.join(1)
        assert not thread.is_alive() and results == [errno.EIO], results
        expect_error(errno.EIO, lambda: os.read(fd, 64))
        fcntl.ioctl(fd, FAULT, 0)
        time.sleep(.03)
        epoll.poll(0)
        fcntl.ioctl(fd, OVERFLOW, 0)
        time.sleep(.03)
        expect_error(errno.EAGAIN, lambda: os.read(fd, 64))
        fcntl.ioctl(fd, CLEAR, 0)
        fcntl.ioctl(fd, FEED, 1024)
        assert epoll.poll(1)[0][1] & select.EPOLLIN
        assert os.read(fd, 65536) == bytes(range(256))*4

        results.clear()
        thread = threading.Thread(target=waiter)
        thread.start();time.sleep(.03)
        fcntl.ioctl(fd, REMOVE, 0)
        events = epoll.poll(1)
        assert events and events[0][1] & select.EPOLLHUP and events[0][1] & select.EPOLLERR, events
        thread.join(1)
        assert not thread.is_alive() and results == [errno.ENODEV], results
        expect_error(errno.ENODEV, lambda: os.read(fd, 64))
        expect_error(errno.ENODEV, lambda: fcntl.ioctl(fd, CLEAR, 0))
        print('Kernel file operations: epoll/nonblocking, partial data, signal wakeup, starvation, errors and removal passed')
    finally:
        epoll.close()
        os.close(blocking)
        os.close(fd)


if __name__ == '__main__':
    main()
