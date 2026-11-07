#!/usr/bin/env bash
# Generate short-lived identities for the network-isolated drill only.
set -euo pipefail
cd -- "${1:?temporary fixture directory required}"
mkdir node clients
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out ca.key
cat > ca.conf <<'CONF'
[req]
distinguished_name=dn
x509_extensions=ca
[dn]
[ca]
basicConstraints=critical,CA:true
keyUsage=critical,keyCertSign,cRLSign
CONF
openssl req -x509 -new -sha256 -days 1 -key ca.key -subj /CN=fixture-ca -config ca.conf -out node/ca.crt
cp node/ca.crt clients/
for role in node admin scheduler admission delivery requeue analytics stranger; do
  openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$role.key"
  openssl req -new -sha256 -key "$role.key" -subj "/CN=fixture-$role" -out "$role.csr"
  if [[ "$role" = node ]]; then
    printf '%s\n' 'extendedKeyUsage=serverAuth,clientAuth' 'subjectAltName=DNS:b1,DNS:b2,DNS:b3,DNS:c1,DNS:c2,DNS:c3' > usage
  else
    echo 'extendedKeyUsage=clientAuth' > usage
  fi
  openssl x509 -req -sha256 -days 1 -in "$role.csr" -CA node/ca.crt -CAkey ca.key -CAcreateserial -extfile usage -out "$role.crt"
  if [[ "$role" = node ]]; then
    cat node.key node.crt > node/node.pem
    chmod 644 node/node.pem
  else
    cp "$role.crt" "$role.key" clients/
    chmod 644 "clients/$role.key"
  fi
done
cat admin.key admin.crt > clients/admin.pem
chmod 644 clients/admin.pem
cat > clients/admin.properties <<'PROPS'
security.protocol=SSL
ssl.keystore.type=PEM
ssl.keystore.location=/fixture/admin.pem
ssl.truststore.type=PEM
ssl.truststore.location=/fixture/ca.crt
PROPS
