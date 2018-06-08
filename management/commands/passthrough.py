from django.conf import settings
from django.core.management import call_command
from django.core.management.base import BaseCommand


class Command(BaseCommand):
    args = '<test_path>'
    help = 'Runs tests against Elasticsearch without validation'

    def handle(self, *args, **options):
        if(len(args) == 1):
            settings.NOSE_ARGS = settings.NOSE_ARGS + [args[0]]

        settings.ES_HOST = settings.DEV_ES_HOST
        settings.WAND_CONFIGURATION['mode'] = 'passthrough'

        call_command('test')
