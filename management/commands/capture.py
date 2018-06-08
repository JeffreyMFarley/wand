from django.conf import settings
from django.core.management import call_command
from django.core.management.base import BaseCommand, CommandError


class Command(BaseCommand):
    args = '<test_path>'
    help = 'Captures the request or response snapshots'

    missing_args_message = """
So that we don't overwrite all the snapshots, you must specify the subset of
tests that you want to run.

We use the NOSE_TEST style of paths for this

Example: bookstore/tests/test_view.py:DocumentTest.test_happy_path

Where:
bookstore/tests  -> a directory
/test_view.py    -> a specific test module
:DocumentTest    -> a specific class within the test module
.test_happy_path -> a specific test within the class


These work exactly like paths, so you can be specific or general

bookstore/tests/test_view.py:DocumentTest.test_happy_path
bookstore/tests/test_view.py:DocumentTest
bookstore/tests/test_view.py
bookstore
    """

    def handle(self, *args, **options):
        if(len(args) != 1):
            raise CommandError(self.missing_args_message)

        settings.NOSE_ARGS = settings.NOSE_ARGS + [args[0]]
        settings.WAND_CONFIGURATION['mode'] = 'capture'

        settings.WAND_CONFIGURATION['capture_request'] = True
        settings.WAND_CONFIGURATION['capture_response'] = True

        call_command('live_test')
