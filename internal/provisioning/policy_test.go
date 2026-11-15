package provisioning

import "testing"

func TestPrivateListenAddressAcceptsOnlyCanonicalPrivateAddresses(t *testing.T) {
	for _, value := range []string{"127.0.0.1:9090", "10.8.1.2:443", "[fd00::1]:9443"} {
		if got, err := privateListenAddress(value); err != nil || got != value {
			t.Fatalf("privateListenAddress(%q) = %q, %v", value, got, err)
		}
	}
	for _, value := range []string{"0.0.0.0:443", "[::]:443", "8.8.8.8:443", "127.0.0.1:0", "localhost:443", "127.0.0.1:443/", "fe80::1%en0:443", "[fd00::01]:443"} {
		if _, err := privateListenAddress(value); err == nil {
			t.Errorf("privateListenAddress(%q) accepted an unsafe or noncanonical address", value)
		}
	}
}

func TestLoadPolicyRejectsMalformedExactSchemaBeforeOpeningResources(t *testing.T) {
	cases := []string{
		`{"listen_address":"127.0.0.1:1","listen_address":"127.0.0.1:1"}`,
		`{"unexpected":true}`,
		`{"listen_address":"0.0.0.0:1","credential_root":"/var/lib/hello/credentials","database_url":"postgres://x:y@db:5432/hello?sslmode=verify-full","ingress_identity":"spiffe://hello/ingress","ca_pem":"","server_certificate_pem":"","server_private_key_pem":""}`,
	}
	for _, raw := range cases {
		if _, err := LoadPolicy([]byte(raw)); err == nil {
			t.Errorf("LoadPolicy accepted malformed policy %s", raw)
		}
	}
}
