import json
import os.path
from collections import deque
from core.helpers import get_es_client
from django.conf import settings
from django.test import TestCase
from elasticsearch import Elasticsearch, TransportError
from wand.assertions import assertRequestsEqual, assertResponsesEqual
from wand.normalizer import normalizeResponse

try:
    from unittest.mock import patch
except ImportError:
    from mock import patch


class FakeElasticsearch(object):
    def __init__(self,
                 subdir,
                 index_name,
                 endpoint='_search'):
        self.basedir = os.path.dirname(__file__) + '/../v2'
        self.subdir = subdir
        self.index_name = index_name
        self.endpoint = endpoint

    # -------------------------------------------------------------------------
    # I/O Methods
    # -------------------------------------------------------------------------

    def buildPath(self, fileName):
        return os.path.join(
            self.basedir,
            self.subdir,
            "__mocks__",
            self.index_name,
            self.endpoint,
            fileName
        )

    def load_request(self, shortName):
        fileName = self.buildPath(shortName + '_req.json')
        with open(fileName, 'r') as f:
            return json.load(f)

    def load_response(self, shortName):
        fileName = self.buildPath(shortName + '_resp.json')
        with open(fileName, 'r') as f:
            return json.load(f)

    def load_static(self, shortName, fileExtension):
        fileName = self.buildPath("{}.{}".format(shortName, fileExtension))
        with open(fileName, 'r') as f:
            return f.read()

    def save_request(self, shortName, req):
        fileName = self.buildPath(shortName + '_req.json')
        with open(fileName, 'w') as f:
            json.dump(req, f)

    def save_response(self, shortName, resp):
        fileName = self.buildPath(shortName + '_resp.json')
        with open(fileName, 'w') as f:
            json.dump(resp, f)

    # -------------------------------------------------------------------------
    # Spy Methods
    # -------------------------------------------------------------------------

    def __enter__(self):
        self.cleanup = []

        self.originalSearch = Elasticsearch.search
        p = patch.object(Elasticsearch, 'search', self.replacementFn)
        p.start()
        self.cleanup.append(p.stop)

        self.originalScroll = Elasticsearch.scroll
        p = patch.object(Elasticsearch, 'scroll', self.fakeScroll)
        p.start()
        self.cleanup.append(p.stop)

    def __exit__(self, exc_type, exc_value, traceback):
        for p in self.cleanup:
            p()

    def capture(self, *args, **kwargs):
        shortName = self.names.popleft()
        self.index_name = kwargs['index']

        if settings.WAND_CONFIGURATION['capture_request']:
            self.save_request(shortName, kwargs['body'])
        else:
            print('Skipping capture request for %s' % shortName)

        es = get_es_client()
        actual = normalizeResponse(
            self.originalSearch(es, *args, **kwargs)
        )

        if settings.WAND_CONFIGURATION['capture_response']:
            self.save_response(shortName, actual)
        else:
            print('Skipping capture response for %s' % shortName)

        return actual

    def fakeScroll(self, *args, **kwargs):
        shortName = self.names.popleft()
        # self.index_name is the same
        return self.load_response(shortName)

    def liveValidate(self, *args, **kwargs):
        shortName = self.names.popleft()
        self.index_name = kwargs['index']

        # Check the input
        body = self.load_request(shortName)
        assertRequestsEqual(body, kwargs['body'])

        es = get_es_client()
        actual = self.originalSearch(es, *args, **kwargs)

        # Check the results
        expected = self.load_response(shortName)
        assertResponsesEqual(expected, actual)

        return actual

    def passthrough(self, *args, **kwargs):
        es = get_es_client()
        actual = self.originalSearch(es, *args, **kwargs)
        return actual

    def replayValidate(self, *args, **kwargs):
        shortName = self.names.popleft()
        self.index_name = kwargs['index']

        # Check the input
        body = self.load_request(shortName)
        assertRequestsEqual(body, kwargs['body'])

        return self.load_response(shortName)

    def transportError(self, *args, **kwargs):
        raise TransportError(404, "Not found")

    # -------------------------------------------------------------------------
    # Spy Setup
    # -------------------------------------------------------------------------

    def asSpy(self, *args):
        self.names = deque(args)

        # Determine the spy function to use
        self.replacementFn = self.liveValidate \
            if settings.ES_HOST != settings.FAKE_ES_HOST \
            else self.replayValidate

        if settings.WAND_CONFIGURATION['mode'] == 'capture':
            self.replacementFn = self.capture
        elif settings.WAND_CONFIGURATION['mode'] == 'passthrough':
            self.replacementFn = self.passthrough

        return self

    def simulateError(self):
        self.replacementFn = self.transportError
        return self
