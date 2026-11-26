#!/usr/bin/env python3
"""Minimal `unzip [-o] [-q] archive [-d dir]` that keeps file modes."""
import os
import sys
import zipfile

args = sys.argv[1:]
dest = "."
if "-d" in args:
    i = args.index("-d")
    dest = args[i + 1]
    del args[i : i + 2]
files = [a for a in args if not a.startswith("-")]
if len(files) != 1:
    sys.exit("usage: unzip [-o] [-q] archive [-d dir]")

with zipfile.ZipFile(files[0]) as zf:
    for info in zf.infolist():
        path = zf.extract(info, dest)
        mode = (info.external_attr >> 16) & 0o777
        if mode and not info.is_dir():
            os.chmod(path, mode)
