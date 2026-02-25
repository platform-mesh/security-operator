package fga

import (
	"testing"

	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	accountName        = "one"
	accountInfoName    = "account"
	parentAccountName  = "default"
	generatedClusterID = "1mj722nrt4jo3ggn"
	originClusterID    = "14uc34987epvgggc"
	creator            = "new@example.com"
	creatorRelation    = "owner"
	parentRelation     = "parent"
	objectType         = "core_platform-mesh_io_account"
)

func TestInitialTuplesForAccount(t *testing.T) {
	creatorVal := creator
	acc := accountv1alpha1.Account{
		ObjectMeta: metav1.ObjectMeta{Name: accountName},
		Spec: accountv1alpha1.AccountSpec{
			Creator: &creatorVal,
		},
	}
	ai := accountv1alpha1.AccountInfo{
		ObjectMeta: metav1.ObjectMeta{Name: accountInfoName},
		Spec: accountv1alpha1.AccountInfoSpec{
			Account: accountv1alpha1.AccountLocation{
				Name:               accountName,
				GeneratedClusterId: generatedClusterID,
				OriginClusterId:    originClusterID,
			},
			ParentAccount: &accountv1alpha1.AccountLocation{
				Name:            parentAccountName,
				OriginClusterId: originClusterID,
			},
		},
	}

	tuples, err := InitialTuplesForAccount(acc, ai, creatorRelation, parentRelation, objectType)
	require.NoError(t, err)
	require.Len(t, tuples, 3)

	// Tuple 1: creator gets assignee on owner role
	assert.Equal(t, v1alpha1.Tuple{
		Object:   "role:core_platform-mesh_io_account/14uc34987epvgggc/one/owner",
		Relation: "assignee",
		User:     "user:new@example.com",
	}, tuples[0])

	// Tuple 2: owner role has creator relation on account
	assert.Equal(t, v1alpha1.Tuple{
		Object:   "core_platform-mesh_io_account:14uc34987epvgggc/one",
		Relation: "owner",
		User:     "role:core_platform-mesh_io_account/14uc34987epvgggc/one/owner#assignee",
	}, tuples[1])

	// Tuple 3: parent account has parent relation on account
	assert.Equal(t, v1alpha1.Tuple{
		Object:   "core_platform-mesh_io_account:14uc34987epvgggc/one",
		Relation: "parent",
		User:     "core_platform-mesh_io_account:14uc34987epvgggc/default",
	}, tuples[2])
}

func TestInitialTuplesForAccount_formatUser(t *testing.T) {
	creator := "system:serviceaccount:ns:name"
	acc := accountv1alpha1.Account{
		ObjectMeta: metav1.ObjectMeta{Name: accountName},
		Spec: accountv1alpha1.AccountSpec{
			Creator: &creator,
		},
	}
	ai := accountv1alpha1.AccountInfo{
		ObjectMeta: metav1.ObjectMeta{Name: accountInfoName},
		Spec: accountv1alpha1.AccountInfoSpec{
			Account: accountv1alpha1.AccountLocation{
				Name:               accountName,
				GeneratedClusterId: generatedClusterID,
				OriginClusterId:    originClusterID,
			},
			ParentAccount: &accountv1alpha1.AccountLocation{
				Name:            parentAccountName,
				OriginClusterId: originClusterID,
			},
		},
	}

	tuples, err := InitialTuplesForAccount(acc, ai, creatorRelation, parentRelation, objectType)
	require.NoError(t, err)
	require.Len(t, tuples, 3)

	assert.Equal(t, "user:system.serviceaccount.ns.name", tuples[0].User)
}

func TestInitialTuplesForAccount_nilCreator(t *testing.T) {
	acc := accountv1alpha1.Account{
		ObjectMeta: metav1.ObjectMeta{Name: accountName},
		Spec:       accountv1alpha1.AccountSpec{},
	}
	ai := accountv1alpha1.AccountInfo{
		ObjectMeta: metav1.ObjectMeta{Name: accountInfoName},
		Spec: accountv1alpha1.AccountInfoSpec{
			Account: accountv1alpha1.AccountLocation{
				Name:               accountName,
				GeneratedClusterId: generatedClusterID,
				OriginClusterId:    originClusterID,
			},
			ParentAccount: &accountv1alpha1.AccountLocation{
				Name:            parentAccountName,
				OriginClusterId: originClusterID,
			},
		},
	}

	_, err := InitialTuplesForAccount(acc, ai, creatorRelation, parentRelation, objectType)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creator is nil")
}
