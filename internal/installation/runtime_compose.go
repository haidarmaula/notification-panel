package installation

import (
	"bytes"
	"encoding/json"
	"io"
)

// AppendRuntimeProject adds an operator-approved project to a protected mobile
// policy. Existing project and key material is preserved. The returned bytes
// contain secrets and must only be published through protected policy storage.
// Both generations are validated by the same parser used by the server; this
// function neither changes the running server nor grants enrollment authority.
func AppendRuntimeProject(source io.Reader, project []byte, store RuntimeStore, db Transactor) ([]byte, error) {
	// Protected deployment policy readers accept at most 64 KiB, even though
	// LoadRuntime also supports larger policies for local development.
	const limit = 64 * 1024
	if source == nil || len(project) == 0 || len(project) > limit {
		return nil, ErrConfiguration
	}
	data, err := io.ReadAll(io.LimitReader(source, limit+1))
	if err != nil || len(data) > limit {
		return nil, ErrConfiguration
	}
	if _, err := LoadRuntime(bytes.NewReader(data), store, db); err != nil {
		return nil, ErrConfiguration
	}
	var file map[string]json.RawMessage
	var projects []json.RawMessage
	if json.Unmarshal(data, &file) != nil || json.Unmarshal(file["projects"], &projects) != nil {
		return nil, ErrConfiguration
	}
	var candidate runtimeProjectIdentity
	if json.Unmarshal(project, &candidate) != nil || candidate.AppID == "" {
		return nil, ErrConfiguration
	}
	for _, existing := range projects {
		var identity runtimeProjectIdentity
		if json.Unmarshal(existing, &identity) != nil || identity.AppID == candidate.AppID ||
			identity.Audience == candidate.Audience || identity.Namespace == candidate.Namespace ||
			identity.OriginAudience == candidate.OriginAudience {
			return nil, ErrConfiguration
		}
	}
	projects = append(projects, bytes.Clone(project))
	file["projects"], err = json.Marshal(projects)
	if err != nil {
		return nil, ErrConfiguration
	}
	result, err := json.Marshal(file)
	if err != nil || len(result) > limit {
		return nil, ErrConfiguration
	}
	if _, err := LoadRuntime(bytes.NewReader(result), store, db); err != nil {
		return nil, ErrConfiguration
	}
	return result, nil
}

type runtimeProjectIdentity struct {
	AppID          string `json:"app_id"`
	Audience       string `json:"audience"`
	Namespace      string `json:"namespace"`
	OriginAudience string `json:"origin_audience"`
}
