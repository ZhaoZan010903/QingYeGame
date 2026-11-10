#!/usr/bin/env python3
"""Explicit maintenance commands using a private environment and credential file."""
import argparse
import json
import os
from pathlib import Path
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('command', choices=['migrate', 'seed-users', 'import-legacy', 'reconcile-media'])
parser.add_argument('--binary', default='/opt/qingye-test/current/qingye-api')
parser.add_argument('--env', default='/etc/qingye-test/app.env')
parser.add_argument('--secrets', default='/root/qingye-private/integration/secrets.json')
parser.add_argument('--products', default='/var/lib/qingye-test/import/products.raw.json')
args = parser.parse_args()
environment = dict(os.environ)
for line in Path(args.env).read_text().splitlines():
    if line.strip() and not line.lstrip().startswith('#'):
        key, value = line.split('=', 1)
        environment[key] = value
if args.command == 'seed-users':
    for account in json.loads(Path(args.secrets).read_text())['accounts']:
        subprocess.run([args.binary, 'seed-user'], input=json.dumps(account), text=True,
                       env=environment, check=True)
else:
    command = [args.binary, args.command]
    if args.command == 'import-legacy':
        command += ['--products', args.products]
    subprocess.run(command, env=environment, check=True)
