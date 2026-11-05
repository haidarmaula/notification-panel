package requeue

import (
	"bytes"

	"hello/internal/deployment"
)

// LoadDeploymentConfiguration loads the fixed protected replay policy. Shared
// file/TLS checks do not confer replay authority; the credential and current DB
// grant checks remain mandatory for every operation.
func LoadDeploymentConfiguration() (*Configuration, error) {
	data, err := deployment.Read(deployment.Requeue)
	if err != nil {
		return nil, ErrConfiguration
	}
	cfg, err := LoadConfiguration(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if deployment.CheckDatabaseFiles(cfg.database.ConnString()) != nil {
		return nil, ErrConfiguration
	}
	return cfg, nil
}
