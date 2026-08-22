// Package v1alpha1 contains API Schema definitions for the v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=tkukushkin.github.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	GroupVersion = schema.GroupVersion{Group: "tkukushkin.github.io", Version: "v1alpha1"}

	SchemeBuilder = runtime.NewSchemeBuilder(func(scheme *runtime.Scheme) error {
		metav1.AddToGroupVersion(scheme, GroupVersion)
		return nil
	})

	AddToScheme = SchemeBuilder.AddToScheme
)
