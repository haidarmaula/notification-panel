package config

import (
	"bytes"
	"encoding/json"
	"io"
	"os"

	"hello/internal/deployment"
	"hello/internal/ingress"
	"hello/internal/jsonobject"
	"hello/internal/observability"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ServerForCommand loads the local environment when invoked without arguments.
// The production entrypoint passes --deployment and reads only its protected
// role file. A missing or invalid policy never falls back to the environment.
func ServerForCommand(args []string) (*ServerConfig, error) {
	if len(args) == 0 {
		return LoadServerConfig(), nil
	}
	if len(args) != 1 || args[0] != "--deployment" {
		return nil, deployment.ErrConfiguration
	}
	data, err := deployment.Read(deployment.Server)
	if err != nil {
		return nil, err
	}
	return parseServerDeployment(data)
}

func parseServerDeployment(data []byte) (*ServerConfig, error) {
	var c ServerConfig
	var admin, gateway json.RawMessage
	var dsn string
	err := jsonobject.DecodeOptional(data, map[string]any{
		"ingress": &gateway, "database_url": &dsn, "api_key": &c.APIKey, "access_secret": &c.AccessSecret,
		"refresh_secret": &c.RefreshSecret, "mobile_policy_file": &c.mobilePolicyFile,
	}, map[string]any{"monitoring": &admin})
	if err != nil || len(c.APIKey) < 32 || len(c.AccessSecret) < 32 || len(c.RefreshSecret) < 32 ||
		c.AccessSecret == c.RefreshSecret || c.APIKey == c.AccessSecret || c.APIKey == c.RefreshSecret {
		return nil, deployment.ErrConfiguration
	}
	c.Gateway, err = ingress.Parse(gateway)
	if err != nil {
		return nil, deployment.ErrConfiguration
	}
	c.Monitoring, err = observability.ParseAdmin(admin)
	if err != nil {
		return nil, deployment.ErrConfiguration
	}
	c.protectedDatabase, err = protectedDatabase(dsn, "hello-server")
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// MobilePolicy returns an optional, caller-owned mobile policy stream. Production
// requires a protected root-owned secret. An empty configured path disables it.
func (c *ServerConfig) MobilePolicy() (io.ReadCloser, error) {
	if c.mobilePolicyFile == "" {
		return nil, nil
	}
	if c.protectedDatabase != nil {
		data, err := deployment.ReadSecret(c.mobilePolicyFile)
		if err != nil {
			return nil, deployment.ErrConfiguration
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	file, err := os.Open(c.mobilePolicyFile)
	if err != nil {
		return nil, deployment.ErrConfiguration
	}
	return file, nil
}

// BootstrapForCommand selects the one-shot setup job's policy independently
// from the long-running server and worker credentials.
func BootstrapForCommand(args []string) (*BootstrapConfig, error) {
	if len(args) == 0 {
		return LoadBootstrapConfig(), nil
	}
	if len(args) != 1 || args[0] != "--deployment" {
		return nil, deployment.ErrConfiguration
	}
	data, err := deployment.Read(deployment.Bootstrap)
	if err != nil {
		return nil, err
	}
	return parseBootstrapDeployment(data)
}

func parseBootstrapDeployment(data []byte) (*BootstrapConfig, error) {
	var c BootstrapConfig
	var dsn string
	err := jsonobject.Decode(data, map[string]any{
		"database_url": &dsn, "admin_name": &c.BootstrapAdminName, "admin_email": &c.BootstrapAdminEmail,
		"admin_password": &c.BootstrapAdminPassword,
	})
	if err != nil || c.BootstrapAdminName == "" || c.BootstrapAdminEmail == "" ||
		len(c.BootstrapAdminPassword) < 8 || len(c.BootstrapAdminPassword) > 72 {
		return nil, deployment.ErrConfiguration
	}
	c.protectedDatabase, err = protectedDatabase(dsn, "hello-bootstrap")
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func protectedDatabase(dsn, name string) (*pgxpool.Config, error) {
	if deployment.CheckDatabaseFiles(dsn) != nil {
		return nil, deployment.ErrConfiguration
	}
	return deployment.Database(dsn, name)
}
