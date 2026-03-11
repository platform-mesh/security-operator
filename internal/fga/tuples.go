package fga

import (
	"errors"
	"fmt"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
)

// InitialTuplesForAccount returns FGA tuples for an account not of type
// organization.
func InitialTuplesForAccount(creator, accountOriginClusterID, accountName, parentOriginClusterID, parentName, creatorRelation, parentRelation, objectType string) ([]v1alpha1.Tuple, error) {
	base, err := baseTuples(creator, accountOriginClusterID, accountName, creatorRelation, objectType)
	if err != nil {
		return nil, err
	}
	tuples := append(base, v1alpha1.Tuple{
		User:     renderAccountEntity(objectType, parentOriginClusterID, parentName),
		Relation: parentRelation,
		Object:   renderAccountEntity(objectType, accountOriginClusterID, accountName),
	})
	return tuples, nil
}

// TuplesForOrganization returns FGA tuples for an Account of type organization.
func TuplesForOrganization(creator, accountOriginClusterID, accountName, creatorRelation, objectType string) ([]v1alpha1.Tuple, error) {
	return baseTuples(creator, accountOriginClusterID, accountName, creatorRelation, objectType)
}

// IsTupleOfAccountFilter returns a filter determining whether a tuple is tied
// to the given account, i.e. contains its cluster id.
func IsTupleOfAccountFilter(generatedClusterID string) TupleFilter {
	return func(t v1alpha1.Tuple) bool {
		return generatedClusterID != "" && (strings.Contains(t.Object, generatedClusterID) || strings.Contains(t.User, generatedClusterID))
	}
}

// ReferencingAccountTupleKey returns a key that can be used to List tuples that
// reference a given account.
func ReferencingAccountTupleKey(objectType, accountOriginClusterID, accountName string) *openfgav1.ReadRequestTupleKey {
	return &openfgav1.ReadRequestTupleKey{
		Object: renderAccountEntity(objectType, accountOriginClusterID, accountName),
	}
}

// ReferencingOwnerRoleTupleKey returns a key that can be used to List tuples
// that reference the owner role of a given account.
func ReferencingOwnerRoleTupleKey(objectType, accountOriginClusterID, accountName string) *openfgav1.ReadRequestTupleKey {
	return &openfgav1.ReadRequestTupleKey{
		Object: renderOwnerRole(objectType, accountOriginClusterID, accountName),
	}
}

func baseTuples(creator, accountOriginClusterID, accountName, creatorRelation, objectType string) ([]v1alpha1.Tuple, error) {
	if creator == "" {
		return nil, errors.New("account creator is empty")
	}

	return []v1alpha1.Tuple{
		{
			User:     renderCreatorUser(creator),
			Relation: "assignee",
			Object:   renderOwnerRole(objectType, accountOriginClusterID, accountName),
		},
		{
			User:     renderOwnerRoleAssigneeGroup(objectType, accountOriginClusterID, accountName),
			Relation: creatorRelation,
			Object:   renderAccountEntity(objectType, accountOriginClusterID, accountName),
		},
	}, nil
}

// formatUser formats a user to be stored in an FGA tuple, i.e. replaces colons
// with dots.
func formatUser(user string) string {
	return strings.ReplaceAll(user, ":", ".")
}

func renderAccountEntity(objectType, originClusterID, name string) string {
	return fmt.Sprintf("%s:%s/%s", objectType, originClusterID, name)
}

func renderCreatorUser(creator string) string {
	return fmt.Sprintf("user:%s", formatUser(creator))
}

// RenderRolePrefix returns the prefix for role User strings that reference an
// Account's roles (e.g. "role:objectType/originClusterID/name/").
func RenderRolePrefix(objectType, originClusterID, name string) string {
	return fmt.Sprintf("role:%s/%s/%s/", objectType, originClusterID, name)
}

func renderOwnerRole(objectType, originClusterID, name string) string {
	return RenderRolePrefix(objectType, originClusterID, name) + "owner"
}

func renderOwnerRoleAssigneeGroup(objectType, originClusterID, name string) string {
	return RenderRolePrefix(objectType, originClusterID, name) + "owner#assignee"
}
