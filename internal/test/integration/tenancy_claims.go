package test

import (
	kcpapisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	kcpapisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
)

// appendTenancyAPIExportClaims merges workspaces and workspacetypes v1alpha1
// permission claims onto exp with identityHash (append or refresh hash only).
func appendTenancyAPIExportClaims(exp *kcpapisv1alpha1.APIExport, identityHash string) {
	for _, r := range []string{"workspaces", "workspacetypes"} {
		if tenancyClaimPresentV1(exp.Spec.PermissionClaims, r) {
			setTenancyClaimHashV1(&exp.Spec.PermissionClaims, r, identityHash)
			continue
		}
		exp.Spec.PermissionClaims = append(exp.Spec.PermissionClaims,
			kcpapisv1alpha1.PermissionClaim{
				GroupResource: kcpapisv1alpha1.GroupResource{
					Group:    "tenancy.kcp.io",
					Resource: r,
				},
				All:          true,
				IdentityHash: identityHash,
			},
		)
	}
}

// tenancyClaimPresentV1 reports whether claims already contains a
// tenancy.kcp.io entry for resource (workspaces or workspacetypes).
func tenancyClaimPresentV1(claims []kcpapisv1alpha1.PermissionClaim, resource string) bool {
	for _, c := range claims {
		if c.GroupResource.Group == "tenancy.kcp.io" && c.GroupResource.Resource == resource {
			return true
		}
	}
	return false
}

// setTenancyClaimHashV1 writes identityHash onto the existing v1alpha1 claim
// for tenancy.kcp.io/resource.
func setTenancyClaimHashV1(claims *[]kcpapisv1alpha1.PermissionClaim, resource string, identityHash string) {
	for i := range *claims {
		if (*claims)[i].GroupResource.Group == "tenancy.kcp.io" &&
			(*claims)[i].GroupResource.Resource == resource {
			(*claims)[i].IdentityHash = identityHash
		}
	}
}

// appendTenancyAPIBindingClaims merges accepted workspaces and workspacetypes
// claims on b with identityHash (append or refresh hash only).
func appendTenancyAPIBindingClaims(b *kcpapisv1alpha2.APIBinding, identityHash string) {
	for _, r := range []string{"workspaces", "workspacetypes"} {
		if tenancyClaimPresentV2(b.Spec.PermissionClaims, r) {
			setTenancyClaimHashV2(&b.Spec.PermissionClaims, r, identityHash)
			continue
		}
		b.Spec.PermissionClaims = append(b.Spec.PermissionClaims,
			kcpapisv1alpha2.AcceptablePermissionClaim{
				ScopedPermissionClaim: kcpapisv1alpha2.ScopedPermissionClaim{
					PermissionClaim: kcpapisv1alpha2.PermissionClaim{
						GroupResource: kcpapisv1alpha2.GroupResource{
							Group: "tenancy.kcp.io", Resource: r},
						Verbs:        []string{"*"},
						IdentityHash: identityHash,
					},
					Selector: kcpapisv1alpha2.PermissionClaimSelector{
						MatchAll: true,
					},
				},
				State: kcpapisv1alpha2.ClaimAccepted,
			},
		)
	}
}

// tenancyClaimPresentV2 reports whether b already accepts a claim for
// tenancy.kcp.io/resource.
func tenancyClaimPresentV2(claims []kcpapisv1alpha2.AcceptablePermissionClaim, resource string) bool {
	for _, c := range claims {
		if c.PermissionClaim.GroupResource.Group == "tenancy.kcp.io" &&
			c.PermissionClaim.GroupResource.Resource == resource {
			return true
		}
	}
	return false
}

// setTenancyClaimHashV2 writes identityHash onto the existing v1alpha2
// acceptable claim for tenancy.kcp.io/resource.
func setTenancyClaimHashV2(claims *[]kcpapisv1alpha2.AcceptablePermissionClaim, resource string, identityHash string) {
	for i := range *claims {
		if (*claims)[i].PermissionClaim.GroupResource.Group == "tenancy.kcp.io" &&
			(*claims)[i].PermissionClaim.GroupResource.Resource == resource {
			(*claims)[i].PermissionClaim.IdentityHash = identityHash
		}
	}
}
