package fcm

// ValidateServiceAccount checks an uploaded same-project credential without
// acquiring a token or following any document-provided URL. It returns only the
// sanitized configuration error used by the protected-file constructor.
func ValidateServiceAccount(data []byte, targetProject, expectedEmail string) error {
	_, err := parseServiceAccount(data, targetProject, FileCredentialPolicy{
		CredentialProjectID: targetProject, ServiceAccountEmail: expectedEmail,
	})
	return err
}
