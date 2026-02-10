package conditions

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IsPresentAndEqualForGeneration is like
// apimeta.IsStatusConditionPresentAndEqual but checks for observed generation.
func IsPresentAndEqualForGeneration(conditions []metav1.Condition, conditionType string, status metav1.ConditionStatus, generation int64) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType && condition.ObservedGeneration == generation {
			return condition.Status == status
		}
	}
	return false
}
