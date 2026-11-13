#!/usr/bin/env bash
# Run on kafka-vps as root. Creates staging PKI and installs the staged FCM key.
# Does not start services, open ports, alter grants, or release dispatch holds.
set -euo pipefail
umask 077
[[ $EUID == 0 ]] || { echo 'Run with sudo.' >&2; exit 1; }
[[ $(hostname) == srv-kafka ]] || { echo 'Unexpected target host.' >&2; exit 1; }
credential=${1:?Usage: prepare-host.sh /absolute/path/to/staged-fcm.json}
[[ $credential == /* && -f $credential && ! -L $credential ]] || exit 1
command -v openssl >/dev/null
command -v python3 >/dev/null
python3 - "$credential" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    c = json.load(f)
if not (c.get('type') == 'service_account' and c.get('project_id') == 'push-notifications-stagi-a6a41'
        and c.get('private_key', '').startswith('-----BEGIN PRIVATE KEY-----') and c.get('client_email')):
    raise SystemExit('Unexpected or incomplete Firebase credential; no values printed.')
PY
for dir in /etc/hello /etc/hello/staging-tls /etc/hello/fcm; do
    [[ ! -L $dir ]] || { echo 'Refusing symlink directory.' >&2; exit 1; }
    install -d -o root -g 65532 -m 0750 "$dir"
done
# A dedicated client CA prevents ordinary server certificates authenticating as gateways.
pki=/etc/hello/staging-tls
work=$(mktemp -d "$pki/.prepare.XXXXXXXX")
trap 'rm -rf -- "$work"' EXIT
for ca in server-ca gateway-client-ca; do
    if [[ ! -e $pki/$ca.key && ! -e $pki/$ca.crt ]]; then
        openssl req -x509 -newkey rsa:3072 -nodes -sha256 -days 365 \
            -subj "/CN=hello-staging-$ca" -keyout "$work/$ca.key" -out "$work/$ca.crt" 2>/dev/null
        install -o root -g root -m 0600 "$work/$ca.key" "$pki/$ca.key"
        install -o root -g 65532 -m 0640 "$work/$ca.crt" "$pki/$ca.crt"
    fi
    [[ -f $pki/$ca.key && -f $pki/$ca.crt && ! -L $pki/$ca.key && ! -L $pki/$ca.crt ]] || exit 1
done
for name in gateway api gateway-client; do
    ca=server-ca
    usage=serverAuth
    san='IP:10.1.16.109,IP:127.0.0.1,DNS:localhost'
    if [[ $name == gateway-client ]]; then
        ca=gateway-client-ca
        usage=clientAuth
        san='DNS:hello-staging-gateway'
    fi
    if [[ ! -e $pki/$name.key && ! -e $pki/$name.crt ]]; then
        openssl req -new -newkey rsa:3072 -nodes -subj "/CN=hello-staging-$name" \
            -keyout "$work/$name.key" -out "$work/$name.csr" 2>/dev/null
        printf 'basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=%s\nsubjectAltName=%s\n' "$usage" "$san" > "$work/extensions"
        openssl x509 -req -in "$work/$name.csr" -CA "$pki/$ca.crt" -CAkey "$pki/$ca.key" \
            -set_serial "0x$(openssl rand -hex 16)" -days 90 -sha256 -extfile "$work/extensions" \
            -out "$work/$name.crt" 2>/dev/null
        install -o root -g 65532 -m 0640 "$work/$name.key" "$pki/$name.key"
        install -o root -g 65532 -m 0640 "$work/$name.crt" "$pki/$name.crt"
    fi
    [[ -f $pki/$name.key && -f $pki/$name.crt && ! -L $pki/$name.key && ! -L $pki/$name.crt ]] || exit 1
    openssl verify -CAfile "$pki/$ca.crt" "$pki/$name.crt"
done
destination=/etc/hello/fcm/service-account.json
[[ ! -L $destination ]] || exit 1
if [[ -e $destination ]]; then
    cmp -s "$credential" "$destination" || { echo 'Existing FCM credential differs; refusing replacement.' >&2; exit 1; }
else
    install -o root -g 65532 -m 0640 "$credential" "$destination"
fi
rm -- "$credential"
echo 'Prepared staging TLS and protected Firebase credential. No services started.'
