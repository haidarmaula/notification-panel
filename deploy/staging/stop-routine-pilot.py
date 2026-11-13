#!/usr/bin/env python3
"""Hold/seal the staging pilot and stop/join its processing roles."""
import datetime
import importlib.util
import json
from pathlib import Path
import socket

if socket.gethostname()!='srv-kafka':raise SystemExit('Run on srv-kafka.')
directory=Path.home()/'hello-staging-provision'
spec=importlib.util.spec_from_file_location('pilot_runtime',directory/'run-one-pilot.py')
pilot=importlib.util.module_from_spec(spec);spec.loader.exec_module(pilot)
pilot.restore_holds(directory)
path=directory/'routine-pilot.json'
if path.exists():
    record=json.loads(path.read_text())
    record.update(state='held',held_at=datetime.datetime.now(datetime.timezone.utc).isoformat())
    path.write_text(json.dumps(record,indent=2)+'\n')
