#!/usr/bin/env python3
"""Generate a one-use public pairing QR link from the staging issuer.

Run with sudo on srv-kafka after enable-public-enrollment.py. The link is
printed for the operator; render it as a QR or transfer it to the test phone.
"""
import importlib.util
import json
import os
from pathlib import Path
import socket

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('enrollment', HERE / 'enable-public-enrollment.py')
enrollment = importlib.util.module_from_spec(spec)
spec.loader.exec_module(enrollment)


def main():
    if socket.gethostname() != 'srv-kafka' or os.geteuid() != 0:
        raise SystemExit('Run with sudo on srv-kafka.')
    if not (enrollment.ROOT / 'public-enrollment.json').exists():
        raise SystemExit('Public enrollment is not enabled; run enable-public-enrollment.py first.')
    data = enrollment.pairing_request(b'{"public":true}')
    print(json.dumps({'link': data['link'], 'origin': data['origin'], 'expires_at': data['expires_at']}, indent=2))


if __name__ == '__main__':
    main()