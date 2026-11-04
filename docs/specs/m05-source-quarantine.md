# M05 source-aware quarantine

Quarantine is durable evidence for a specific Kafka log incarnation, not a
terminal campaign outcome. No HTTP endpoint, replay, purge or worker activation
is added by this slice. Raw messages may contain sensitive data: database
access, encryption at rest, retention and restricted operator access are
production gates. Raw input must never be copied to error messages or logs.

Deployment provisions an explicit log_source UUID with topic, verified cluster
identity and topic-incarnation identity/generation. Rows start inactive. This
library does not infer incarnation from topic name and cannot activate a source.
The eventual reader startup must verify this deployment identity against Kafka;
topic recreation or unknown incarnation requires reconciliation before fetching.

One bounded transaction locks deployment control and the registered source FOR
SHARE. It requires an unsealed deployment and active source. Publication lineage
is checked by preparation; unexpected event epochs can still be quarantined.
Physical log identity is immutable and separate from publication source_epoch.
The tuple (log_source_id, partition, offset) uniquely identifies
evidence. Raw key and payload are bounded to 1 MiB each; reasons are fixed
allowlisted classes, not exception strings. Attempts are bounded 1–3. No arbitrary
notification foreign key is inferred from untrusted bytes.

An exact redelivery is a no-op. The same source position carrying different
bytes/topic is a source-identity error, never an overwrite or a second record.
Insert failure or rejected source/size means the message is unsafe to commit.
The serial consumer must retain that message, stop fetching and retry evidence
storage with bounded calls/backoff until success or shutdown. No later offset
may pass it. Unknown commit outcome is resolved by storing the same evidence.

Down refuses evidence or active source registrations. This avoids silently
destroying unresolved work. Registration, inactive source cleanup, replay
authorization and retention remain explicit operator/recovery work.

The proposed [operator reprocessing contract](m05-operator-reprocessing.md) refines
replay eligibility, authority, claims and audit. It does not add a runtime replay
path or change this evidence-storage contract.
