package kubernetes

import (
	"github.com/rs/zerolog/log"
	"k8s.io/apimachinery/pkg/api/resource"
)

// BoolPtr returns a pointer to the provided bool value.
// This is commonly used for Kubernetes API objects that require pointer fields.
func BoolPtr(b bool) *bool {
	return &b
}

// StringPtr returns a pointer to the provided string value.
// This is commonly used for Kubernetes API objects that require pointer fields.
func StringPtr(s string) *string {
	return &s
}

// Int32Ptr returns a pointer to the provided int32 value.
// This is commonly used for Kubernetes API objects that require pointer fields.
func Int32Ptr(i int32) *int32 {
	return &i
}

// Helper function to parse resource quantities
func ParseResourceQuantity(value string) resource.Quantity {
	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		log.Error().Str("component", "coredns").Err(err).Msgf("Failed to parse resource quantity %s", value)
		return resource.Quantity{}
	}
	return quantity
}
