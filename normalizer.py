''' Provides a set of normalizing functions
'''

FAKE_SCROLL_ID = 'fake-scroll-id'


def normalizeResponse(response):
    '''
    Take raw a raw Elasticsearch response and fix it for the Fake
    implemenation
    '''
    # Remove these fields
    remove = ['_shards', 'timed_out', 'took']
    for field in remove:
        if field in response:
            del response[field]

    # Fix the index (Neutralize -v1 vs. -v2)
    for hit in response['hits']['hits']:
        if 'v1' in hit['_index'] or 'v2' in hit['_index']:
            tokens = hit['_index'].split('-')
            hit['_index'] = '-'.join(tokens[:-1])

    # Neutralize the _scroll_id
    response['_scroll_id'] = FAKE_SCROLL_ID

    return response
