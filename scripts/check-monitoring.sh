#!/usr/bin/env bash
# Offline rule verification using the locally pulled Prometheus 3.5.0 test image.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../deploy/observability"
promtool=(docker run -i --rm --network none --read-only --tmpfs /tmp:rw,noexec,nosuid,size=96m
  --mount "type=bind,src=$PWD,dst=/rules,readonly" --workdir /rules --entrypoint /bin/promtool
  prom/prometheus@sha256:63805ebb8d2b3920190daf1cb14a60871b16fd38bed42b857a3182bc621f4996)
"${promtool[@]}" check config --syntax-only prometheus.yml
"${promtool[@]}" check rules rules.yml
"${promtool[@]}" test rules rules_test.yml
# Verify each checked-in panel's PromQL with Grafana's environment variable expanded.
python3 - <<'PY' | "${promtool[@]}" check rules /dev/stdin
import json
with open("dashboard.json") as source:
    dashboard = json.load(source)
rules = [{"record": "hello_panel_%d" % p["id"], "expr": t["expr"].replace("$environment", ".*")}
         for p in dashboard["panels"] for t in p["targets"]]
print(json.dumps({"groups": [{"name": "dashboard", "rules": rules}]}))
PY
# Provision the board in a real, disposable Grafana with private loopback only.
container=$(docker run -d --rm --network none --read-only --cap-drop ALL \
    --tmpfs /tmp:rw,noexec,nosuid,size=32m \
    --tmpfs /var/lib/grafana:rw,noexec,nosuid,uid=472,size=96m \
    --mount "type=bind,src=$PWD/dashboard.json,dst=/var/lib/hello/dashboards/dashboard.json,readonly" \
    --mount "type=bind,src=$PWD/grafana-provider.yml,dst=/etc/grafana/provisioning/dashboards/hello.yml,readonly" \
    -e GF_AUTH_ANONYMOUS_ENABLED=true -e GF_SERVER_HTTP_ADDR=127.0.0.1 \
    -e GF_ANALYTICS_REPORTING_ENABLED=false -e GF_PLUGINS_PREINSTALL_DISABLED=true \
    grafana/grafana@sha256:6ac590e7cabc2fbe8d7b8fc1ce9c9f0582177b334e0df9c927ebd9670469440f)
trap 'docker stop --time 3 "$container" >/dev/null' EXIT
for _ in {1..60}; do
    if docker exec "$container" wget -qO /dev/null http://127.0.0.1:3000/api/health 2>/dev/null; then break; fi
    sleep 0.5
done
docker exec "$container" wget -qO- http://127.0.0.1:3000/api/dashboards/uid/hello-notification-runtime | \
  python3 -c 'import json,sys; saved=json.load(open("dashboard.json")); live=json.load(sys.stdin)["dashboard"]; assert live["uid"]==saved["uid"] and [p["targets"] for p in live["panels"]]==[p["targets"] for p in saved["panels"]], "provisioned dashboard differs"; print("Grafana provisioned and returned all", len(live["panels"]), "panels")'
