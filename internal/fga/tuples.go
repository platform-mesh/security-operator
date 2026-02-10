package fga

import (
	"fmt"
	"strings"

	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
)

func TuplesForAccount(acc accountv1alpha1.Account, ai accountv1alpha1.AccountInfo, creatorRelation, parentRelation, objectType string) []v1alpha1.Tuple {
	tuples := append(baseTuples(acc, ai, creatorRelation, objectType), v1alpha1.Tuple{
		User:     fmt.Sprintf("%s:%s/%s", objectType, ai.Spec.ParentAccount.OriginClusterId, ai.Spec.ParentAccount.Name),
		Relation: parentRelation,
		Object:   fmt.Sprintf("%s:%s/%s", objectType, ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
	})

	return tuples
}

func TuplesForOrganization(acc accountv1alpha1.Account, ai accountv1alpha1.AccountInfo, creatorRelation, objectType string) []v1alpha1.Tuple {
	return baseTuples(acc, ai, creatorRelation, objectType)
}

func baseTuples(acc accountv1alpha1.Account, ai accountv1alpha1.AccountInfo, creatorRelation, objectType string) []v1alpha1.Tuple {
	return []v1alpha1.Tuple{
		v1alpha1.Tuple{
			User:     fmt.Sprintf("user:%s", formatUser(*acc.Spec.Creator)),
			Relation: "assignee",
			Object:   fmt.Sprintf("role:%s/%s/%s/owner", objectType, ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
		},
		v1alpha1.Tuple{
			User:     fmt.Sprintf("role:%s/%s/%s/owner#assignee", objectType, ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
			Relation: creatorRelation,
			Object:   fmt.Sprintf("%s:%s/%s", objectType, ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
		},
	}
}

// formatUser formats a user to be stored in an FGA tuple, i.e. replaces colons
// with dots in case of a Kubernetes ServiceAccount.
// todo(simontesar): why was this implemented ot only be done in case of SAs?
func formatUser(user string) string {
	if isServiceAccount(user) {
		return strings.ReplaceAll(user, ":", ".")
	}

	return user
}

// isServiceAccount determines wheter a user appears to be a Kubernetes
// ServiceAccount.
func isServiceAccount(user string) bool {
	return strings.HasPrefix(user, "system:serviceaccount:")
}
