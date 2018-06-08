from django.conf import settings
from django.core.management import call_command
from django.core.management.base import BaseCommand


class Command(BaseCommand):
    args = '<test_path>'
    help = 'Runs tests against the development instance of Elasticsearch'

    def handle(self, *args, **options):
        if(len(args) == 1):
            settings.NOSE_ARGS = settings.NOSE_ARGS + [args[0]]

        settings.ES_HOST = settings.DEV_ES_HOST
        call_command('test')
