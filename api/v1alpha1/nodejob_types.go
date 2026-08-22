package v1alpha1

import (
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Trigger decides what counts as a fresh reason to run the task on a node.
// +kubebuilder:validation:Enum=OncePerNode;OncePerBoot
type Trigger string

const (
	// TriggerOncePerNode runs once per lifetime of the Node object.
	TriggerOncePerNode Trigger = "OncePerNode"
	// TriggerOncePerBoot runs on every boot of the node; bootID goes into the hash.
	TriggerOncePerBoot Trigger = "OncePerBoot"
)

type NodeJobSpec struct {
	// +kubebuilder:default=OncePerNode
	// +optional
	Trigger Trigger `json:"trigger,omitempty"`

	// JobTemplate goes into the Job unchanged; the controller only adds nodeName, an
	// ownerReference, its own labels, and the node-condition tolerations a DaemonSet adds.
	//
	// Nodes are picked by template.spec.nodeSelector and
	// template.spec.affinity.nodeAffinity, the same fields a DaemonSet uses. Neither takes
	// part in the run hash, so narrowing or widening the target set never re-runs the task
	// on nodes that were already done.
	//
	// +kubebuilder:validation:XValidation:rule="has(self.spec)",message="jobTemplate.spec is required"
	// +kubebuilder:validation:XValidation:rule="!has(self.spec) || !has(self.spec.ttlSecondsAfterFinished)",message="ttlSecondsAfterFinished is not allowed: a finished Job is how NodeJob remembers that the node is done; a TTL would erase that memory and the task would be run again in a loop"
	// +kubebuilder:validation:XValidation:rule="!has(self.spec) || !has(self.spec.template.spec) || !has(self.spec.template.spec.nodeName)",message="template.spec.nodeName is set by the NodeJob controller and must not be set here"
	// +kubebuilder:validation:XValidation:rule="!has(self.spec) || !has(self.spec.template.spec) || !has(self.spec.template.spec.affinity) || (!has(self.spec.template.spec.affinity.podAffinity) && !has(self.spec.template.spec.affinity.podAntiAffinity) && (!has(self.spec.template.spec.affinity.nodeAffinity) || !has(self.spec.template.spec.affinity.nodeAffinity.preferredDuringSchedulingIgnoredDuringExecution)))",message="only affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution is honoured: the pod is pinned with nodeName and never reaches the scheduler, so pod affinity and soft node preferences would be silently ignored"
	JobTemplate batchv1.JobTemplateSpec `json:"jobTemplate"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=nj
// +kubebuilder:validation:XValidation:rule="self.metadata.name.size() <= 63",message="a NodeJob name must fit 63 characters: it is copied verbatim into the tkukushkin.github.io/nodejob label on every Job, and Kubernetes rejects longer label values"
// +kubebuilder:printcolumn:name="Trigger",type=string,JSONPath=`.spec.trigger`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

type NodeJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec NodeJobSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

type NodeJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeJob `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &NodeJob{}, &NodeJobList{})
		return nil
	})
}
