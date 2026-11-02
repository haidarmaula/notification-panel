# Kafka review and self-hosted readiness

Tanggal: 2026-11-02. Status: design evidence, bukan hasil provisioning/HA test.
Rancangan: [ADR-07](../adr/adr-07-kafka-topology.md).

## Konfirmasi pengguna

Kafka direncanakan di server perusahaan sendiri (self-hosted). Jumlah host,
failure domain/storage, resource dan operator belum diketahui. Tiga container
pada satu server tidak memenuhi toleransi kehilangan satu server.

## Temuan aktual pada source/dependency

| Finding | Evidence | Status |
| --- | --- | --- |
| H47: keyed partition affinity tidak berjalan | NewProducer memakai LeastBytes; dependency balancer.go tidak hash key | Open; ADR-07 mengusulkan Hash dan mapping rollout |
| H48: default batch wait berisiko mendominasi serial publish | NewProducer tidak mengatur BatchSize/BatchTimeout; writer.go default 100/1s; processor menunggu publish setiap delivery | Open; low-latency config dan benchmark diperlukan |
| M48: seed CSV bukan list endpoint | kafka.TCP(broker) dan Reader.Brokers berisi satu string; dependency TCP bersifat variadic, tidak split CSV | Open; explicit validated slice diperlukan |

Probe lokal (bukan broker integration test), 2026-11-02:

```text
same key, partitions 0/1/2; LeastBytes: [0 1 2 0 1 2]
same key, partitions 0/1/2; Hash: [2 2 2 2 2 2]
```

Probe memakai Message.Key=notification_42, payload tetap dan enam pemanggilan
Balance pada github.com/segmentio/kafka-go v0.4.51. Command yang dijalankan:
GOCACHE=/tmp/hello-go-review-cache go run /tmp/hello-kafka-partition-probe.go.
File probe hanya sementara; SPEC harus membuat regression test permanen ketika
producer diperbaiki. Tidak ada request jaringan/provider/broker dari probe.

H48 adalah inferensi source/defaults, bukan pengukuran broker. Jika tiap publish
serial menunggu flush 1s, 20.000 publish dapat menambah sekitar 5,6 jam waktu
tunggu batching pada satu loop; itu ilustrasi kondisi, bukan hasil benchmark
atau estimasi pasti produksi. Provider submission dan analytics event latency
harus diukur terpisah. M48 juga belum diuji terhadap cluster multi-seed nyata.

## Production evidence checklist

| Gate | Bukti yang diperlukan | Status |
| --- | --- | --- |
| K01 host topology | Independent host/storage/network domains, broker/controller placement, resource budget | Self-hosted confirmed; footprint unknown |
| K02 versions/features | Pinned broker/client, KRaft quorum, feature/ELR and patch review | Compose version known; production unreviewed |
| K03 topics | Actual cluster/topic IDs, partitions/RF/ISR/retention/timestamp and internal-topic placement | Desired profile proposed; not provisioned |
| K04 clients | Hash golden vectors, batch/timeout bounds, parsed seeds, TLS/SASL and advertised connectivity | Current gaps registered; not fixed |
| K05 provisioning | Check/plan/apply artifact, drift detection, target-cluster guard, scoped admin | NEW artifacts not implemented |
| K06 group/retention | Explicit groups, offset range/expiry/reset behavior, outage/catch-up and dedup retention windows | Needs SPEC and consumer inventory |
| K07 security | Private listeners, ACL matrix, key/cert lifecycle and operator UI controls | ADR-11 pending |
| K08 failure tests | Real broker/host loss, low ISR, quorum loss, disk pressure, rebalance/offset loss and rotation | Not executed |
| K09 capacity | Hot-partition broadcast and event lag with resource/disk headroom | Not benchmarked |
| K10 operations | Alert actions, on-call, backup/restore coordination, rollout/rollback evidence | ADR-08/10 pending |

Proposed profiles: dev RF1/minISR1 (non-HA); production three partitions per
logical topic, RF3/minISR2, delete policy, 7-day log retention and 30-day inactive
offset retention. Final hosting, retention and SLO must be accepted before
production. Replication is not backup and retention is not a promise to retain
data forever until every consumer acknowledges it.

## Boundary of this increment

Step 12 selesai sebagai draft ADR dan checklist. AGENTS.md dikoreksi agar tidak
mengajarkan LeastBytes sebagai hash routing; code-review.md mendapat temuan
baru berstatus open. Kode Go, compose, Makefile, topic/group/offset dan credential
tidak diubah. Tidak ada live Kafka test, deployment, migration atau FCM send.
