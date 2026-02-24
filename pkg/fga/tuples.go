package fga

import (
	"errors"
	"fmt"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
)

// InitialTuplesForAccount returns FGA tuples for an account not of type
// organization.
func InitialTuplesForAccount(acc accountv1alpha1.Account, ai accountv1alpha1.AccountInfo, creatorRelation, parentRelation, objectType string) ([]v1alpha1.Tuple, error) {
	base, err := baseTuples(acc, ai, creatorRelation, objectType)
	if err != nil {
		return nil, err
	}
	tuples := append(base, v1alpha1.Tuple{
		User:     renderAccountEntity(objectType, ai.Spec.ParentAccount.OriginClusterId, ai.Spec.ParentAccount.Name),
		Relation: parentRelation,
		Object:   renderAccountEntity(objectType, ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
	})
	return tuples, nil
}

// TuplesForOrganization returns FGA tuples for an Account of type organization.
func TuplesForOrganization(acc accountv1alpha1.Account, ai accountv1alpha1.AccountInfo, creatorRelation, objectType string) ([]v1alpha1.Tuple, error) {
	return baseTuples(acc, ai, creatorRelation, objectType)
}

// IsTupleOfAccountFilter returns a filter determining whether a tuple is tied
// to the given account, i.e. contains its cluster id.
func IsTupleOfAccountFilter(ai accountv1alpha1.AccountInfo) TupleFilter {
	generatedClusterID := ai.Spec.Account.GeneratedClusterId
	return func(t v1alpha1.Tuple) bool {
		return strings.Contains(t.Object, generatedClusterID) || strings.Contains(t.User, generatedClusterID)
	}
}

// ReferencingAccountTupleKey returns a key that can be used to List tuples that
// reference a given account.
func ReferencingAccountTupleKey(objectType string, ai accountv1alpha1.AccountInfo) *openfgav1.ReadRequestTupleKey {
	return &openfgav1.ReadRequestTupleKey{
		Object: renderAccountEntity(objectType, ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
	}
}

// ReferencingOwnerRoleTupleKey returns a key that can be used to List tuples
// that reference the owner role of a given account.
func ReferencingOwnerRoleTupleKey(objectType string, ai accountv1alpha1.AccountInfo) *openfgav1.ReadRequestTupleKey {
	return &openfgav1.ReadRequestTupleKey{
		Object: renderOwnerRole(objectType, ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
	}
}
func baseTuples(acc accountv1alpha1.Account, ai accountv1alpha1.AccountInfo, creatorRelation, objectType string) ([]v1alpha1.Tuple, error) {
	if acc.Spec.Creator == nil {
		return nil, errors.New("account creator is nil")
	}

	return []v1alpha1.Tuple{
		{
			User:     renderCreatorUser(*acc.Spec.Creator),
			Relation: "assignee",
			Object:   renderOwnerRole(objectType, ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
		},
		{
			User:     renderOwnerRoleAssigneeGroup(objectType, ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
			Relation: creatorRelation,
			Object:   renderAccountEntity(objectType, ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
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

func renderOwnerRole(objectType, originClusterID, name string) string {
	return fmt.Sprintf("role:%s/%s/%s/owner", objectType, originClusterID, name)
}

func renderOwnerRoleAssigneeGroup(objectType, originClusterID, name string) string {
	return fmt.Sprintf("role:%s/%s/%s/owner#assignee", objectType, originClusterID, name)
}
