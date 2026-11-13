#!/usr/bin/env bash
# Run as haidar after configure-api.py has completed with sudo.
set -euo pipefail
[[ $(hostname) == srv-kafka ]] || exit 1
command -v curl >/dev/null
revision=4695a2c9730740a9fb88e009fd2c00599edc9ba6
gateway_image='nginx@sha256:583b7f2fce1cdb8da5ad5c39ebd3c33b98b87d493190e548bdee1fa8c619c311'
gateway_options=(--network host --user 65532:65532 --read-only --cap-drop ALL
  --security-opt no-new-privileges --pids-limit 128 --memory 128m --cpus 0.5
  --tmpfs /tmp:rw,noexec,nosuid,size=32m,uid=65532,gid=65532
  --mount type=bind,src=/etc/hello/gateway-assets,dst=/gateway,readonly
  --entrypoint nginx)
docker run --rm "${gateway_options[@]}" "$gateway_image" -c /gateway/nginx.conf -t
for container in hello-staging-server hello-staging-gateway; do
  if docker container inspect "$container" >/dev/null 2>&1; then
    echo "Container $container already exists; inspect before replacing it." >&2
    exit 1
  fi
done
docker run -d --name hello-staging-server --restart unless-stopped \
  --network host --read-only --cap-drop ALL --security-opt no-new-privileges \
  --pids-limit 128 --memory 512m --cpus 1 \
  --log-opt max-size=10m --log-opt max-file=3 \
  --mount type=bind,src=/etc/hello/server.json,dst=/etc/hello/server.json,readonly \
  --mount type=bind,src=/etc/hello/server-assets/db-ca.crt,dst=/etc/hello/server-assets/db-ca.crt,readonly \
  "hello-server:$revision"
docker run -d --name hello-staging-gateway --restart unless-stopped \
  --log-opt max-size=10m --log-opt max-file=3 \
  "${gateway_options[@]}" "$gateway_image" -c /gateway/nginx.conf -g 'daemon off;'
ca_file=$(mktemp)
trap 'rm -f -- "$ca_file"' EXIT
docker exec hello-staging-gateway cat /gateway/server-ca.crt > "$ca_file"
for attempt in {1..12}; do
  code=$(curl --silent --cacert "$ca_file" --connect-timeout 1 --max-time 2 \
    --output /dev/null --write-out '%{http_code}' https://10.1.16.109:8443/ || true)
  if [[ $code =~ ^[234][0-9][0-9]$ && $code != 403 ]]; then
    echo "Verified trusted HTTPS through gateway to API (HTTP $code)."
    echo 'Login and application behavior still require verification; dispatch remains held.'
    exit 0
  fi
  sleep 1
done
echo 'HTTPS readiness failed; inspect application and gateway status before proceeding.' >&2
exit 1
