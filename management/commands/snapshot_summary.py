from __future__ import print_function
from django.core.management.base import BaseCommand
from wand.walker import findSnapshots


class Command(BaseCommand):
    help = 'Lists the request or response snapshots'

    def handle(self, *args, **options):
        snapshots = findSnapshots('.')

        print('\t', '\t'.join(['Req?', 'Resp?']))

        currDir = ''
        for s in sorted(snapshots,
                        key=lambda x: (x.index, x.endpoint, x.path, x.title)):
            if s.path != currDir:
                currDir = s.path
                print(currDir)

            line = ['.' if s.req else 'x', '.' if s.resp else 'x', s.title]

            print('\t', '\t'.join(line))
