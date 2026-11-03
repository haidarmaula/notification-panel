package installation

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/jackc/pgx/v5/pgtype"
)

type RuntimeStore interface {
	MobileIdentityStore
	OriginUseStore
}

type Runtime struct {
	Authority *MobileAuthority
	Lifecycle *Lifecycle
	routes    map[pgtype.UUID]map[[2]string]Route
}

type policyFile struct {
	CurrentKeyVersion int32  `json:"current_key_version"`
	FingerprintKey    []byte `json:"fingerprint_key"`
	EncryptionKeys    []struct {
		Version int32  `json:"version"`
		Key     []byte `json:"key"`
	} `json:"encryption_keys"`
	Projects []struct {
		AppID           string `json:"app_id"`
		Issuer          string `json:"issuer"`
		Audience        string `json:"audience"`
		Purpose         string `json:"purpose"`
		Namespace       string `json:"namespace"`
		VerificationKey []byte `json:"verification_key"`
		OriginIssuer    string `json:"origin_issuer"`
		OriginAudience  string `json:"origin_audience"`
		OriginKey       []byte `json:"origin_key"`
		Routes          []struct {
			Platform          string `json:"platform"`
			AddressType       string `json:"address_type"`
			ProviderNamespace string `json:"provider_namespace"`
			ProviderRouteKey  string `json:"provider_route_key"`
		} `json:"routes"`
	} `json:"projects"`
}

func LoadRuntime(source io.Reader, store RuntimeStore, db Transactor) (*Runtime, error) {
	const limit = 1 << 20
	data, err := io.ReadAll(io.LimitReader(source, limit+1))
	if err != nil || len(data) > limit {
		return nil, ErrConfiguration
	}
	var file policyFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return nil, ErrConfiguration
	}
	if err := decoder.Decode(new(any)); err != io.EOF || len(file.Projects) == 0 {
		return nil, ErrConfiguration
	}
	keys := make(map[int32][]byte, len(file.EncryptionKeys))
	for _, key := range file.EncryptionKeys {
		if _, exists := keys[key.Version]; exists {
			return nil, ErrConfiguration
		}
		keys[key.Version] = key.Key
	}
	protector, err := NewProtector(file.CurrentKeyVersion, keys, file.FingerprintKey)
	if err != nil {
		return nil, err
	}
	lifecycle, err := NewLifecycle(db, protector)
	if err != nil {
		return nil, err
	}
	policies := make(map[pgtype.UUID]MobilePolicy, len(file.Projects))
	routes := make(map[pgtype.UUID]map[[2]string]Route, len(file.Projects))
	for _, project := range file.Projects {
		var id pgtype.UUID
		if err := id.Scan(project.AppID); err != nil || !id.Valid || id.String() != project.AppID || bytes.Equal(project.VerificationKey, project.OriginKey) {
			return nil, ErrConfiguration
		}
		if _, exists := policies[id]; exists || len(project.Routes) == 0 {
			return nil, ErrConfiguration
		}
		origin, err := NewSignedOrigin(store, project.OriginIssuer, project.OriginAudience, project.OriginKey)
		if err != nil {
			return nil, err
		}
		policies[id] = MobilePolicy{Issuer: project.Issuer, Audience: project.Audience, Purpose: project.Purpose, Namespace: project.Namespace, VerificationKey: project.VerificationKey, Origin: origin}
		routes[id] = make(map[[2]string]Route, len(project.Routes))
		for _, route := range project.Routes {
			pair := [2]string{route.Platform, route.AddressType}
			if _, exists := routes[id][pair]; exists || !validIdentityText(route.ProviderNamespace) || !validIdentityText(route.ProviderRouteKey) ||
				(route.Platform != "ANDROID" && route.Platform != "IOS") || (route.AddressType != FCMFID && route.AddressType != FCMRegistrationToken) {
				return nil, ErrConfiguration
			}
			routes[id][pair] = Route{Namespace: route.ProviderNamespace, Key: route.ProviderRouteKey, Platform: route.Platform, AddressType: route.AddressType}
		}
	}
	authority, err := NewMobileAuthority(store, policies)
	if err != nil {
		return nil, err
	}
	return &Runtime{Authority: authority, Lifecycle: lifecycle, routes: routes}, nil
}

func (r *Runtime) Route(project pgtype.UUID, platform, addressType string) (Route, bool) {
	route, ok := r.routes[project][[2]string{platform, addressType}]
	return route, ok
}

// ValidAddress checks the opaque input budget before consuming an origin assertion.
func ValidAddress(value string) bool {
	return len(value) > 0 && len(value) <= maxAddressBytes
}
