package fga

import (
	"errors"
	"fmt"
	"strings"

	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
)

// TuplesForAccount returns FGA tuples for an account not of type organization.
func TuplesForAccount(acc accountv1alpha1.Account, ai accountv1alpha1.AccountInfo, creatorRelation, parentRelation, objectType string) ([]v1alpha1.Tuple, error) {
	base, err := baseTuples(acc, ai, creatorRelation, objectType)
	if err != nil {
		return nil, err
	}
	tuples := append(base, v1alpha1.Tuple{
		User:     fmt.Sprintf("%s:%s/%s", objectType, ai.Spec.ParentAccount.OriginClusterId, ai.Spec.ParentAccount.Name),
		Relation: parentRelation,
		Object:   fmt.Sprintf("%s:%s/%s", objectType, ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
	})
	return tuples, nil
}

// TuplesForOrganization returns FGA tuples for an Account of type organization.
func TuplesForOrganization(acc accountv1alpha1.Account, ai accountv1alpha1.AccountInfo, creatorRelation, objectType string) ([]v1alpha1.Tuple, error) {
	return baseTuples(acc, ai, creatorRelation, objectType)
}

func baseTuples(acc accountv1alpha1.Account, ai accountv1alpha1.AccountInfo, creatorRelation, objectType string) ([]v1alpha1.Tuple, error) {
	if acc.Spec.Creator == nil {
		return nil, errors.New("account creator is nil")
	}

	return []v1alpha1.Tuple{
		{
			User:     fmt.Sprintf("user:%s", formatUser(*acc.Spec.Creator)),
			Relation: "assignee",
			Object:   fmt.Sprintf("role:%s/%s/%s/owner", objectType, ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
		},
		{
			User:     fmt.Sprintf("role:%s/%s/%s/owner#assignee", objectType, ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
			Relation: creatorRelation,
			Object:   fmt.Sprintf("%s:%s/%s", objectType, ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
		},
	}, nil
}

// formatUser formats a user to be stored in an FGA tuple, i.e. replaces colons
// with dots.
func formatUser(user string) string {
	return strings.ReplaceAll(user, ":", ".")
}
