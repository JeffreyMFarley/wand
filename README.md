# Wand

This is an implementation of an idea I had in [this blog post](https://dev.to/jeffreymfarley/mocking-elasticsearch-5goj)

## Configuration

<TODO> _add x to SETTINGS.PY_

## Writing tests

If your tests look like this, you will get the full benefit of the Wand commands

```python
fake = FakeElasticsearch(<module_name>)
with fake.asSpy(<some_test_name>):
    # act
    response = self.client.get(self.end_point, self.baseParams)
    actual = response.data

    # assert
    self.assertEqual(response.status_code, 200)
    # other assertions

```

## Commands

Wand provides a few commands to help with building the app.

#### Passthrough

```
./manage.py passthrough <test_path>
```

Runs tests against Elasticsearch without validation

Provide a `<test_path>` to run a subset of tests

#### Live Test

```
./manage.py live_test <test_path>
```

This command will set up the fake Elasticsearch to:
1. Validate the request being sent to Elasticsearch matches the known request
1. Forwards the request to Elasticsearch
1. Validates the response received from Elasticsearch matches the known response
1. Forwards the response to the caller

Provide a `<test_path>` to run a subset of tests


#### Capture

```
./manage.py capture test_path
```

Creates or overwrites the request and response snapshots for all the tests in `test_path`

`test_path` is in the form used by NOSE

###### Example:

`./manage.py capture bookstore/tests/test_view.py:DocumentTest.test_happy_path`

Where:

Section|Means
---|---
`bookstore/tests`|a directory
`/test_view.py`|a specific test module
`:DocumentTest`|a specific class within the test module
`.test_happy_path`|a specific test within the class


These work exactly like paths, so you can be specific or general

```
bookstore/tests/test_view.py:DocumentTest.test_happy_path
bookstore/tests/test_view.py:DocumentTest
bookstore/tests/test_view.py
bookstore
```

#### Snapshot Summary

```
./manage.py snapshot_summary
```

Lists the request and response snapshots.
This also shows which snapshots are missing their other half

## Development Guide

#### New Code

1. Make sure you have an Elasticsearch instance running
1. Create your test indexes
1. Start with `./manage.py passthrough` to write your code and tests
1. When you are satisified that everything is passing and covered, use `./manage.py capture <test_path>` to create the request and response pair
1. Verify everything works as expected with `./manage.py live_test`
1. Commit and push the code
1. The CI server will exercise your code using the stored snapshots

#### Fixing Code

1. Make sure you have an Elasticsearch instance running
1. Create your test indexes
1. Use `./manage.py live_test` to narrow down where the problem is
1. Fix the code or test
1. When you are satisified that everything is passing and covered, use `./manage.py capture <test_path>` to update the request and response pair
1. Verify everything works as expected with `./manage.py live_test`
1. Check the number of changed files with `git status`
    1. If there are too many changes, use `git checkout -- .` to discard the changes
1. Commit and push the code
1. The CI server will exercise your code using the stored snapshots

## Details - Fixture files

In order to replicate ES functionality without requiring an instance running,
we use a pair of fixture files to act as a test double.

#### These files are used to:

1. Use as spies to verify the queries formed by the Django modules are the same as the expected queries
1. Provide a simulated response from Elasticsearch so those modules can be tested via mocks
1. Use as a contract test to verify Elasticsearch behaves as expected

An Elasticsearch query is represented with a pair of files:

1. _somename_`_req.json` is the request portion of the query
1. _somename_`_resp.json` is Elasticsearch's response to the query

#### Directory structure

The request response pairs should be put in a directory tree that is similar
to the resource location in a live instance.

```
+- <module>
   +- __mocks__
     +- <index-name>
       +- <endpoint>
```
