package nodejob

import (
	"maps"
	"slices"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	nodejobv1alpha1 "github.com/tkukushkin/nodejob-controller/api/v1alpha1"
)

const (
	// LabelNodeJob is public contract: with no .status, kubectl get jobs -l is the only way
	// to see a rollout as a whole.
	LabelNodeJob = "tkukushkin.github.io/nodejob"
	// LabelRunHash is stored because it cannot be recomputed from the Job: the controller
	// added nodeName and its own labels on the way in.
	LabelRunHash = "tkukushkin.github.io/run-hash"
	// AnnotationNodeName is an annotation, not a label: a node name can exceed 63 characters.
	AnnotationNodeName = "tkukushkin.github.io/node-name"
)

func BuildJob(nj *nodejobv1alpha1.NodeJob, nodeName, runHash string) *batchv1.Job {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      JobName(nj.Name, nodeName, runHash),
			Namespace: nj.Namespace,
			Labels: merge(nj.Spec.JobTemplate.Labels, map[string]string{
				LabelNodeJob: nj.Name,
				LabelRunHash: runHash,
			}),
			Annotations: merge(nj.Spec.JobTemplate.Annotations, map[string]string{
				AnnotationNodeName: nodeName,
			}),
		},
		Spec: *nj.Spec.JobTemplate.Spec.DeepCopy(),
	}
	job.Spec.Template.Spec.NodeName = nodeName
	job.Spec.Template.Spec.Tolerations = podTolerations(nj.Spec.JobTemplate.Spec.Template.Spec)
	return job
}

// podTolerations adds the tolerations a DaemonSet adds implicitly, so that a node being
// not-ready or under pressure does not keep a node fix from running there.
func podTolerations(pod corev1.PodSpec) []corev1.Toleration {
	out := slices.Clone(pod.Tolerations)
	tolerate := func(key string, effect corev1.TaintEffect) {
		out = append(out, corev1.Toleration{
			Key:      key,
			Operator: corev1.TolerationOpExists,
			Effect:   effect,
		})
	}

	tolerate(corev1.TaintNodeNotReady, corev1.TaintEffectNoExecute)
	tolerate(corev1.TaintNodeUnreachable, corev1.TaintEffectNoExecute)
	tolerate(corev1.TaintNodeDiskPressure, corev1.TaintEffectNoSchedule)
	tolerate(corev1.TaintNodeMemoryPressure, corev1.TaintEffectNoSchedule)
	tolerate(corev1.TaintNodePIDPressure, corev1.TaintEffectNoSchedule)
	tolerate(corev1.TaintNodeUnschedulable, corev1.TaintEffectNoSchedule)
	if pod.HostNetwork {
		tolerate(corev1.TaintNodeNetworkUnavailable, corev1.TaintEffectNoSchedule)
	}
	return out
}

func merge(user, own map[string]string) map[string]string {
	out := make(map[string]string, len(user)+len(own))
	maps.Copy(out, user)
	maps.Copy(out, own)
	return out
}
