#!/usr/bin/env python3
"""Run a Linux amd64 test command with socket I/O and io_uring denied.

This preserves UIDs, unlike an unprivileged network namespace, so tests can still
verify that system executables are root-owned. This is test infrastructure, not a
production sandbox. Use only with ordinary stdin/stdout/stderr and no inherited
network sockets. The filter is inherited by the command and its descendants.
"""
import ctypes
import errno
import os
import platform
import socket
import sys


def main():
    if platform.system() != "Linux" or platform.machine() != "x86_64":
        raise SystemExit("offline-exec supports Linux amd64 only")
    if len(sys.argv) < 2:
        raise SystemExit("usage: offline-exec.py PROGRAM [ARGS...]")

    class Filter(ctypes.Structure):
        _fields_ = [("code", ctypes.c_ushort), ("jt", ctypes.c_ubyte),
                    ("jf", ctypes.c_ubyte), ("k", ctypes.c_uint)]

    class Program(ctypes.Structure):
        _fields_ = [("length", ctypes.c_ushort),
                    ("filters", ctypes.POINTER(Filter))]

    # seccomp_data.arch must be AUDIT_ARCH_X86_64. Reject other ABIs and x32.
    instructions = [
        (0x20, 0, 0, 4), (0x15, 1, 0, 0xC000003E),
        (0x06, 0, 0, 0x80000000), (0x20, 0, 0, 0),
        (0x35, 0, 1, 0x40000000), (0x06, 0, 0, 0x00050000 | errno.ENOSYS),
    ]
    denied = [41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 53,
              288, 299, 307, 425, 426, 427]
    for number in denied:
        instructions += [(0x15, 0, 1, number),
                         (0x06, 0, 0, 0x00050000 | errno.EPERM)]
    instructions.append((0x06, 0, 0, 0x7FFF0000))
    filters = (Filter * len(instructions))(*(Filter(*x) for x in instructions))
    program = Program(len(filters), filters)
    libc = ctypes.CDLL(None, use_errno=True)
    libc.prctl.argtypes = [ctypes.c_int, ctypes.c_ulong, ctypes.c_ulong,
                          ctypes.c_ulong, ctypes.c_ulong]
    libc.prctl.restype = ctypes.c_int
    if libc.prctl(38, 1, 0, 0, 0) != 0:  # PR_SET_NO_NEW_PRIVS
        raise OSError(ctypes.get_errno(), "cannot set no-new-privileges")
    if libc.prctl(22, 2, ctypes.addressof(program), 0, 0) != 0:  # SECCOMP FILTER
        raise OSError(ctypes.get_errno(), "cannot install offline filter")
    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    except PermissionError:
        pass
    else:
        sock.close()
        raise SystemExit("socket creation was not blocked; refusing offline claim")
    os.execvp(sys.argv[1], sys.argv[1:])


if __name__ == "__main__":
    main()
