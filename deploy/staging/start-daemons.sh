#!/usr/bin/env bash
# Requires reviewed active source rows and committed admission checkpoints.
set -euo pipefail
[[ $(hostname) == srv-kafka ]] || exit 1
revision=4695a2c9730740a9fb88e009fd2c00599edc9ba6
sealed=$(docker exec hello-staging-postgres psql -X -U hello_staging \
  -d hello_staging -v ON_ERROR_STOP=1 -At -c 'SELECT sealed FROM notification_runtime;')
if [[ $sealed != f ]]; then
  echo 'Runtime is sealed; daemon preflight requires an unsealed runtime. No containers started.' >&2
  exit 1
fi
for role in scheduler admission; do
  if docker container inspect "hello-staging-$role" >/dev/null 2>&1; then
    echo "Existing $role container requires inspection; refusing replacement." >&2
    exit 1
  fi
done
for role in scheduler admission; do
  image=scheduler
  if [[ $role == admission ]]; then image=worker; fi
  docker run -d --name "hello-staging-$role" --restart unless-stopped \
    --network host --read-only --cap-drop ALL --security-opt no-new-privileges \
    --pids-limit 128 --memory 256m --cpus 0.5 \
    --log-opt max-size=10m --log-opt max-file=3 \
    --mount "type=volume,src=hello-staging-$role-policy,dst=/etc/hello,readonly" \
    "hello-$image:$revision"
done
echo 'Daemon containers created; verify startup logs and Kafka group assignment.'
