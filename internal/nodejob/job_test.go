package nodejob

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	nodejobv1alpha1 "github.com/tkukushkin/nodejob-controller/api/v1alpha1"
)

func nodeJobWithTemplate() *nodejobv1alpha1.NodeJob {
	nj := &nodejobv1alpha1.NodeJob{
		ObjectMeta: metav1.ObjectMeta{Name: "fix", Namespace: "node-jobs"},
		Spec:       nodejobv1alpha1.NodeJobSpec{JobTemplate: templateWithImage("busybox:1")},
	}
	nj.Spec.JobTemplate.Labels = map[string]string{"team": "infra"}
	return nj
}

func TestBuildJobPinsNodeName(t *testing.T) {
	job := BuildJob(nodeJobWithTemplate(), "node-a", "abcdef0123456789")
	if got := job.Spec.Template.Spec.NodeName; got != "node-a" {
		t.Errorf("nodeName = %q, want node-a", got)
	}
	if job.Namespace != "node-jobs" {
		t.Errorf("namespace = %q, want node-jobs", job.Namespace)
	}
}

func TestBuildJobSetsContractLabels(t *testing.T) {
	job := BuildJob(nodeJobWithTemplate(), "node-a", "abcdef0123456789")
	if got := job.Labels[LabelNodeJob]; got != "fix" {
		t.Errorf("%s = %q, want fix", LabelNodeJob, got)
	}
	if got := job.Labels[LabelRunHash]; got != "abcdef0123456789" {
		t.Errorf("%s = %q, want abcdef0123456789", LabelRunHash, got)
	}
	if got := job.Labels["team"]; got != "infra" {
		t.Errorf("user label lost: team = %q, want infra", got)
	}
	if got := job.Annotations[AnnotationNodeName]; got != "node-a" {
		t.Errorf("%s = %q, want node-a", AnnotationNodeName, got)
	}
}

func TestBuildJobDoesNotMutateNodeJob(t *testing.T) {
	nj := nodeJobWithTemplate()
	BuildJob(nj, "node-a", "abcdef0123456789")
	if nj.Spec.JobTemplate.Spec.Template.Spec.NodeName != "" {
		t.Error("BuildJob mutated the NodeJob's template")
	}
	if _, ok := nj.Spec.JobTemplate.Labels[LabelNodeJob]; ok {
		t.Error("BuildJob mutated the NodeJob's template labels")
	}
}

func TestBuildJobAddsImplicitTolerations(t *testing.T) {
	nj := nodeJobWithTemplate()
	nj.Spec.JobTemplate.Spec.Template.Spec.Tolerations = []corev1.Toleration{{
		Key: "dedicated", Operator: corev1.TolerationOpExists,
	}}

	job := BuildJob(nj, "node-a", "abcdef0123456789")

	keys := map[string]bool{}
	for _, tol := range job.Spec.Template.Spec.Tolerations {
		keys[tol.Key] = true
	}
	for _, want := range []string{
		"dedicated",
		corev1.TaintNodeNotReady,
		corev1.TaintNodeUnreachable,
		corev1.TaintNodeDiskPressure,
		corev1.TaintNodeMemoryPressure,
		corev1.TaintNodePIDPressure,
		corev1.TaintNodeUnschedulable,
	} {
		if !keys[want] {
			t.Errorf("toleration for %s missing", want)
		}
	}
	if keys[corev1.TaintNodeNetworkUnavailable] {
		t.Error("network-unavailable tolerated without hostNetwork")
	}
}

func TestBuildJobToleratesNetworkUnavailableWithHostNetwork(t *testing.T) {
	nj := nodeJobWithTemplate()
	nj.Spec.JobTemplate.Spec.Template.Spec.HostNetwork = true

	job := BuildJob(nj, "node-a", "abcdef0123456789")

	for _, tol := range job.Spec.Template.Spec.Tolerations {
		if tol.Key == corev1.TaintNodeNetworkUnavailable {
			return
		}
	}
	t.Error("hostNetwork pod does not tolerate network-unavailable")
}
