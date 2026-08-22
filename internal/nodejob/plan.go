package nodejob

import (
	"fmt"
	"maps"
	"slices"

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1helper "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/component-helpers/scheduling/corev1/nodeaffinity"

	nodejobv1alpha1 "github.com/tkukushkin/nodejob-controller/api/v1alpha1"
)

type NodeInfo struct {
	Name   string
	UID    string
	BootID string
	Labels map[string]string
	Taints []corev1.Taint
}

type Creation struct {
	NodeName string
	RunHash  string
}

type Result struct {
	Create []Creation
	Delete []batchv1.Job
}

func Plan(
	spec nodejobv1alpha1.NodeJobSpec,
	nodes []NodeInfo,
	existing []batchv1.Job,
	templateHash string,
) (Result, error) {
	podSpec := spec.JobTemplate.Spec.Template.Spec
	required := requiredNodeAffinity(podSpec)
	tolerations := podTolerations(podSpec)

	wanted := make(map[string]string, len(nodes))
	for _, node := range nodes {
		matches, err := required.Match(asNode(node))
		if err != nil {
			return Result{}, fmt.Errorf("match node %s: %w", node.Name, err)
		}
		if !matches || hasUntoleratedTaint(node, tolerations) {
			continue
		}
		// A node whose kubelet has not reported a bootID yet would otherwise run under the
		// OncePerNode hash and be torn down mid-run once the real bootID appears.
		if spec.Trigger == nodejobv1alpha1.TriggerOncePerBoot && node.BootID == "" {
			continue
		}
		wanted[node.Name] = RunHash(templateHash, runKey(spec.Trigger, node))
	}

	var result Result
	current := make(map[string]bool, len(existing))
	stale := make(map[string]bool)

	for _, job := range existing {
		nodeName := job.Spec.Template.Spec.NodeName
		want, matched := wanted[nodeName]
		if !matched || job.Labels[LabelRunHash] != want {
			result.Delete = append(result.Delete, job)
			stale[nodeName] = true
			continue
		}
		current[nodeName] = true
	}

	for _, name := range sortedKeys(wanted) {
		// No replacement until the stale Job is gone, or both pods would fix one node at once.
		if current[name] || stale[name] {
			continue
		}
		result.Create = append(result.Create, Creation{NodeName: name, RunHash: wanted[name]})
	}

	return result, nil
}

// Node selection reuses the scheduler's own matcher, so nodeSelector and nodeAffinity mean
// here exactly what they mean on a DaemonSet. Neither set means every node.
func requiredNodeAffinity(pod corev1.PodSpec) nodeaffinity.RequiredNodeAffinity {
	return nodeaffinity.GetRequiredNodeAffinity(&corev1.Pod{
		Spec: corev1.PodSpec{
			NodeSelector: pod.NodeSelector,
			Affinity:     pod.Affinity,
		},
	})
}

// Only NoSchedule and NoExecute exclude a node; PreferNoSchedule is a scheduler preference
// with nothing to prefer once the pod is pinned.
func hasUntoleratedTaint(node NodeInfo, tolerations []corev1.Toleration) bool {
	_, untolerated := v1helper.FindMatchingUntoleratedTaint(
		logr.Discard(),
		node.Taints,
		tolerations,
		func(t *corev1.Taint) bool {
			return t.Effect == corev1.TaintEffectNoSchedule || t.Effect == corev1.TaintEffectNoExecute
		},
		true,
	)
	return untolerated
}

func asNode(node NodeInfo) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: node.Name, Labels: node.Labels},
	}
}

// What counts as one occasion: a boot for OncePerBoot, the Node object itself for
// OncePerNode — its UID rather than its name, so a node reimaged under the same name is a
// new object and gets the task again.
func runKey(trigger nodejobv1alpha1.Trigger, node NodeInfo) string {
	if trigger == nodejobv1alpha1.TriggerOncePerBoot {
		return node.BootID
	}
	return node.UID
}

func sortedKeys(m map[string]string) []string {
	return slices.Sorted(maps.Keys(m))
}
