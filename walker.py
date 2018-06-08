from __future__ import unicode_literals
from collections import namedtuple
import os

import sys
if sys.version < '3':  # pragma: no cover
    _unicode = unicode
else:  # pragma: no cover
    _unicode = lambda x: x

# -----------------------------------------------------------------------------
# Filters


def filterSnapshots(fullPath):
    n = fullPath.lower()
    return 'json' in n and '__mocks__' in n

# -----------------------------------------------------------------------------
# Walk Variations


def _walkDirectory(path, filterFn):
    for currentDir, _, files in os.walk(_unicode(path)):
        # Get the absolute path of the currentDir parameter
        currentDir = os.path.abspath(currentDir)

        # Traverse through all files
        for fileName in files:
            fullPath = os.path.join(currentDir, fileName)

            if filterFn(fullPath):
                yield fullPath


def _walkFile(path, filterFn):
    with io.open(path, 'r', encoding='utf-8') as f:
        for l in f:
            fullPath = os.path.abspath(l.strip())

            if filterFn(fullPath):
                yield fullPath


def walk(path, filterFn=None):
    if not filterFn:
        filterFn = filterSnapshots

    if os.path.isfile(path):
        for f in _walkFile(path, filterFn):
            yield f
    elif os.path.isdir(path):
        for f in _walkDirectory(path, filterFn):
            yield f
    else:
        raise ValueError(path + ' is not a file or directory')

# -----------------------------------------------------------------------------
# Snapshot Functions


Snapshot = namedtuple('Snapshot', [
    'path', 'index', 'endpoint', 'title', 'req', 'resp'
])


def splitPath(path):
    tokens = path.split(os.sep)
    return (tokens[-2], tokens[-1])


def splitFileName(fileName):
    n = fileName.lower()
    if '_req.json' in n:
        return (fileName[:-9], 'req', '')
    elif '_resp.json' in n:
        return (fileName[:-10], '', 'resp')

    return (fileName[:-5], '', '')


def findSnapshots(path):
    data = {}

    for f in walk(path, filterSnapshots):
        path, fileName = os.path.split(os.path.relpath(f))
        index, endpoint = splitPath(path)
        title, req, resp = splitFileName(fileName)

        if title in data:
            data[title] = Snapshot(path, index, endpoint, title,
                                   data[title].req or req,
                                   data[title].resp or resp)
        else:
            data[title] = Snapshot(path, index, endpoint, title, req, resp)

    return data.values()
