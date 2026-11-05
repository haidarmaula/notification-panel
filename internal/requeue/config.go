package requeue

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"hello/internal/deployment"
	"hello/internal/dispatch"
	"hello/internal/jsonobject"
	"hello/internal/kafka"
)

// ErrConfiguration deliberately omits decoder, DSN, key and filesystem details.
var ErrConfiguration = errors.New("invalid replay deployment configuration")

// Configuration retains read-once deployment policy for future session wiring.
// Fields remain private: callers cannot change routing or trust after loading.
// Loading is not approval of a source incarnation and opens no network connection.
type Configuration struct {
	database *pgxpool.Config
	verifier *dispatch.ReplayVerifier
	source   sourceManifest
	kafka    kafka.ScopedProducerConfig
}

type sourceManifest struct {
	id               pgtype.UUID
	clusterIdentity  string
	topicName        string
	topicIncarnation string
	partitionCount   int32
}

// LoadConfiguration reads an exact-schema policy from a caller-owned trusted
// stream. The caller must protect the file and its parent directories against
// unauthorized modification. It does not load .env, activate a source or accept
// an insecure transport override. Broker TLS/SASL and live source verification
// remain separate prerequisites before a session factory may enable execution.
func LoadConfiguration(source io.Reader) (*Configuration, error) {
	if source == nil || !deployment.EnvironmentAllowed() {
		return nil, ErrConfiguration
	}
	data, err := jsonobject.Read(source)
	if err != nil {
		return nil, ErrConfiguration
	}
	var databaseURL, issuer, audience, deploymentID, keyText, sourceID string
	var manifest sourceManifest
	var kafkaPolicy json.RawMessage
	if jsonobject.Decode(data, map[string]any{
		"database_url": &databaseURL, "issuer": &issuer, "audience": &audience, "deployment": &deploymentID,
		"verification_key": &keyText, "log_source_id": &sourceID, "cluster_identity": &manifest.clusterIdentity,
		"topic_name": &manifest.topicName, "topic_incarnation": &manifest.topicIncarnation, "partition_count": &manifest.partitionCount, "kafka": &kafkaPolicy,
	}) != nil {
		return nil, ErrConfiguration
	}
	key, err := base64.StdEncoding.Strict().DecodeString(keyText)
	if err != nil || len(key) != ed25519.PublicKeySize || bytes.Equal(key, make([]byte, ed25519.PublicKeySize)) || base64.StdEncoding.EncodeToString(key) != keyText {
		return nil, ErrConfiguration
	}
	verifier, err := dispatch.NewReplayVerifier(issuer, audience, deploymentID, key)
	if err != nil {
		return nil, ErrConfiguration
	}
	manifest.id, err = parseUUID(sourceID)
	if err != nil || !validPolicyText(manifest.clusterIdentity) || !validPolicyText(manifest.topicIncarnation) ||
		!validTopic(manifest.topicName) || manifest.partitionCount < 1 {
		return nil, ErrConfiguration
	}
	database, err := deployment.Database(databaseURL, "hello-requeue")
	if err != nil {
		return nil, ErrConfiguration
	}
	brokerConfig, err := deployment.Broker(kafkaPolicy, manifest.topicName)
	if err != nil {
		return nil, ErrConfiguration
	}
	return &Configuration{database: database, verifier: verifier, source: manifest, kafka: brokerConfig}, nil
}

func validPolicyText(value string) bool {
	return len(value) > 0 && len(value) <= 255 && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}

func validTopic(value string) bool {
	if len(value) < 1 || len(value) > 249 || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
