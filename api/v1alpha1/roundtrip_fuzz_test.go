package v1alpha1

import (
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/api/equality"
)

func FuzzStoreRoundTrip(f *testing.F) {
	f.Add([]byte(`{"spec":{"coreModule":"module","tuples":[{"object":"doc:1","relation":"viewer","user":"user:anne"}]}}`))
	f.Add([]byte(`{"status":{"storeID":"s1","authorizationModelID":"am1","managedTuples":[{"object":"o","relation":"r","user":"u"}]}}`))
	f.Add([]byte(`{}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzRoundTrip(t, data, &Store{}, &Store{})
	})
}

func FuzzAuthorizationModelRoundTrip(f *testing.F) {
	f.Add([]byte(`{"spec":{"storeRef":{"name":"store","cluster":"cl1"},"model":"model openfga/v1","tuples":[{"object":"doc:1","relation":"viewer","user":"user:anne"}]}}`))
	f.Add([]byte(`{"status":{"managedTuples":[{"object":"o","relation":"r","user":"u"}]}}`))
	f.Add([]byte(`{}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzRoundTrip(t, data, &AuthorizationModel{}, &AuthorizationModel{})
	})
}

func FuzzAPIExportPolicyRoundTrip(f *testing.F) {
	f.Add([]byte(`{"spec":{"apiExportRef":{"name":"export","clusterPath":"root:org"},"allowPathExpressions":["root:org:*"]}}`))
	f.Add([]byte(`{"status":{"managedAllowExpressions":["root:org:ws1"]}}`))
	f.Add([]byte(`{}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzRoundTrip(t, data, &APIExportPolicy{}, &APIExportPolicy{})
	})
}

func FuzzIdentityProviderConfigurationRoundTrip(f *testing.F) {
	f.Add([]byte(`{"spec":{"registrationAllowed":true,"clients":[{"clientType":"confidential","clientName":"app","redirectURIs":["https://app/callback"]}]}}`))
	f.Add([]byte(`{"status":{"managedClients":{"app":{"clientID":"c1","registrationClientURI":"https://kc/clients/c1"}}}}`))
	f.Add([]byte(`{}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzRoundTrip(t, data, &IdentityProviderConfiguration{}, &IdentityProviderConfiguration{})
	})
}

func FuzzInviteRoundTrip(f *testing.F) {
	f.Add([]byte(`{"spec":{"email":"user@example.com"}}`))
	f.Add([]byte(`{"spec":{"email":""}}`))
	f.Add([]byte(`{}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzRoundTrip(t, data, &Invite{}, &Invite{})
	})
}

// fuzzRoundTrip unmarshals arbitrary JSON into obj, marshals it back, unmarshals
// into obj2, and checks semantic equality. We use equality.Semantic.DeepEqual from
// k8s.io/apimachinery which treats nil and empty slices/maps as equivalent — the
// standard Kubernetes comparison semantic for API objects.
func fuzzRoundTrip[T any](t *testing.T, data []byte, obj *T, obj2 *T) {
	t.Helper()

	if err := json.Unmarshal(data, obj); err != nil {
		return
	}

	roundtripped, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	if err := json.Unmarshal(roundtripped, obj2); err != nil {
		t.Fatalf("failed to unmarshal roundtripped data: %v", err)
	}

	if !equality.Semantic.DeepEqual(obj, obj2) {
		t.Errorf("roundtrip mismatch for %T", obj)
	}
}
