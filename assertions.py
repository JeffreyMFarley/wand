import deep
from wand.normalizer import normalizeResponse


def assertRequestsEqual(expected, actual):
    diff = deep.diff(actual, expected)
    if diff:
        diff.print_full()
        raise AssertionError("Requests do not match")


def assertResponsesEqual(expected, actual):
    e = normalizeResponse(expected)
    a = normalizeResponse(actual)

    diff = deep.diff(a, e)
    if diff:
        diff.print_full()
        raise AssertionError("Responses do not match")
