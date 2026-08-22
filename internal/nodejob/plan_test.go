package nodejob

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	nodejobv1alpha1 "github.com/tkukushkin/nodejob-controller/api/v1alpha1"
)

const testHash = "abcdef0123456789"

func specWithSelector(sel map[string]string, trigger nodejobv1alpha1.Trigger) nodejobv1alpha1.NodeJobSpec {
	spec := nodejobv1alpha1.NodeJobSpec{Trigger: trigger}
	spec.JobTemplate.Spec.Template.Spec.NodeSelector = sel
	return spec
}

func specWithNodeAffinity(
	terms []corev1.NodeSelectorTerm,
	trigger nodejobv1alpha1.Trigger,
) nodejobv1alpha1.NodeJobSpec {
	spec := nodejobv1alpha1.NodeJobSpec{Trigger: trigger}
	spec.JobTemplate.Spec.Template.Spec.Affinity = &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: terms},
		},
	}
	return spec
}

func existingJob(nodeName, runHash string) batchv1.Job {
	return batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:   JobName("fix", nodeName, runHash),
			Labels: map[string]string{LabelRunHash: runHash},
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{NodeName: nodeName}},
		},
	}
}

func TestPlanNilSelectorMatchesEveryNode(t *testing.T) {
	nodes := []NodeInfo{{Name: "a"}, {Name: "b"}}
	got, err := Plan(specWithSelector(nil, nodejobv1alpha1.TriggerOncePerNode), nodes, nil, testHash)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(got.Create) != 2 {
		t.Errorf("create = %d, want 2", len(got.Create))
	}
}

func TestPlanEmptySelectorMatchesEveryNode(t *testing.T) {
	nodes := []NodeInfo{{Name: "a"}, {Name: "b"}}
	got, err := Plan(specWithSelector(map[string]string{}, nodejobv1alpha1.TriggerOncePerNode), nodes, nil, testHash)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(got.Create) != 2 {
		t.Errorf("create = %d, want 2", len(got.Create))
	}
}

func TestPlanSelectorFiltersNodes(t *testing.T) {
	nodes := []NodeInfo{
		{Name: "a", Labels: map[string]string{"role": "worker"}},
		{Name: "b", Labels: map[string]string{"role": "master"}},
	}
	sel := map[string]string{"role": "worker"}
	got, err := Plan(specWithSelector(sel, nodejobv1alpha1.TriggerOncePerNode), nodes, nil, testHash)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(got.Create) != 1 || got.Create[0].NodeName != "a" {
		t.Errorf("create = %v, want only node a", got.Create)
	}
}

func TestPlanCreatesNothingWhenJobIsCurrent(t *testing.T) {
	nodes := []NodeInfo{{Name: "a"}}
	existing := []batchv1.Job{existingJob("a", testHash)}
	got, _ := Plan(specWithSelector(nil, nodejobv1alpha1.TriggerOncePerNode), nodes, existing, testHash)
	if len(got.Create) != 0 || len(got.Delete) != 0 {
		t.Errorf("create=%d delete=%d, want 0 and 0", len(got.Create), len(got.Delete))
	}
}

func TestPlanDeletesJobOfVanishedNode(t *testing.T) {
	existing := []batchv1.Job{existingJob("gone", testHash)}
	got, _ := Plan(specWithSelector(nil, nodejobv1alpha1.TriggerOncePerNode), nil, existing, testHash)
	if len(got.Delete) != 1 {
		t.Fatalf("delete = %d, want 1", len(got.Delete))
	}
}

func TestPlanDeletesJobOfUnmatchedNode(t *testing.T) {
	nodes := []NodeInfo{{Name: "a", Labels: map[string]string{"role": "master"}}}
	existing := []batchv1.Job{existingJob("a", testHash)}
	sel := map[string]string{"role": "worker"}
	got, _ := Plan(specWithSelector(sel, nodejobv1alpha1.TriggerOncePerNode), nodes, existing, testHash)
	if len(got.Delete) != 1 {
		t.Fatalf("delete = %d, want 1", len(got.Delete))
	}
}

func TestPlanWaitsForStaleJobBeforeRecreating(t *testing.T) {
	nodes := []NodeInfo{{Name: "a"}}
	existing := []batchv1.Job{existingJob("a", "0000000000000000")}
	got, _ := Plan(specWithSelector(nil, nodejobv1alpha1.TriggerOncePerNode), nodes, existing, testHash)
	if len(got.Delete) != 1 {
		t.Errorf("delete = %d, want 1", len(got.Delete))
	}
	if len(got.Create) != 0 {
		t.Errorf("create = %d, want 0 while the stale Job is still there", len(got.Create))
	}
}

func TestPlanOncePerBootRerunsAfterReboot(t *testing.T) {
	spec := specWithSelector(nil, nodejobv1alpha1.TriggerOncePerBoot)
	nodes := []NodeInfo{{Name: "a", BootID: "boot-2"}}
	existing := []batchv1.Job{existingJob("a", RunHash(testHash, "boot-1"))}

	got, _ := Plan(spec, nodes, existing, testHash)
	if len(got.Delete) != 1 {
		t.Errorf("delete = %d, want 1 after reboot", len(got.Delete))
	}
}

func TestPlanOncePerNodeIgnoresReboot(t *testing.T) {
	spec := specWithSelector(nil, nodejobv1alpha1.TriggerOncePerNode)
	nodes := []NodeInfo{{Name: "a", BootID: "boot-2"}}
	existing := []batchv1.Job{existingJob("a", testHash)}

	got, _ := Plan(spec, nodes, existing, testHash)
	if len(got.Delete) != 0 || len(got.Create) != 0 {
		t.Errorf("delete=%d create=%d, want 0 and 0", len(got.Delete), len(got.Create))
	}
}

func TestPlanDeletesJobWithoutNodeName(t *testing.T) {
	orphan := existingJob("", testHash)
	got, _ := Plan(specWithSelector(nil, nodejobv1alpha1.TriggerOncePerNode), nil, []batchv1.Job{orphan}, testHash)
	if len(got.Delete) != 1 {
		t.Errorf("delete = %d, want 1", len(got.Delete))
	}
}

func TestPlanCreateOrderIsDeterministic(t *testing.T) {
	nodes := []NodeInfo{{Name: "c"}, {Name: "a"}, {Name: "b"}}
	got, _ := Plan(specWithSelector(nil, nodejobv1alpha1.TriggerOncePerNode), nodes, nil, testHash)
	want := []string{"a", "b", "c"}
	for i, w := range want {
		if got.Create[i].NodeName != w {
			t.Fatalf("create order = %v, want %v", got.Create, want)
		}
	}
}

func TestPlanNodeAffinityFiltersNodes(t *testing.T) {
	nodes := []NodeInfo{
		{Name: "a", Labels: map[string]string{"zone": "eu-central-1a"}},
		{Name: "b", Labels: map[string]string{"zone": "eu-central-1b"}},
		{Name: "c", Labels: map[string]string{"zone": "eu-central-1c"}},
	}
	spec := specWithNodeAffinity([]corev1.NodeSelectorTerm{{
		MatchExpressions: []corev1.NodeSelectorRequirement{{
			Key:      "zone",
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{"eu-central-1a", "eu-central-1c"},
		}},
	}}, nodejobv1alpha1.TriggerOncePerNode)

	got, err := Plan(spec, nodes, nil, testHash)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(got.Create) != 2 || got.Create[0].NodeName != "a" || got.Create[1].NodeName != "c" {
		t.Errorf("create = %v, want nodes a and c", got.Create)
	}
}

func TestPlanNodeAffinityMatchesByNodeName(t *testing.T) {
	nodes := []NodeInfo{{Name: "a"}, {Name: "b"}}
	spec := specWithNodeAffinity([]corev1.NodeSelectorTerm{{
		MatchFields: []corev1.NodeSelectorRequirement{{
			Key:      "metadata.name",
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{"b"},
		}},
	}}, nodejobv1alpha1.TriggerOncePerNode)

	got, err := Plan(spec, nodes, nil, testHash)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(got.Create) != 1 || got.Create[0].NodeName != "b" {
		t.Errorf("create = %v, want only node b", got.Create)
	}
}

func TestPlanRejectsInvalidNodeAffinity(t *testing.T) {
	spec := specWithNodeAffinity([]corev1.NodeSelectorTerm{{
		MatchExpressions: []corev1.NodeSelectorRequirement{{
			Key:      "cores",
			Operator: corev1.NodeSelectorOpGt,
			Values:   []string{"not-a-number"},
		}},
	}}, nodejobv1alpha1.TriggerOncePerNode)

	if _, err := Plan(spec, []NodeInfo{{Name: "a"}}, nil, testHash); err == nil {
		t.Error("invalid node affinity accepted, want error")
	}
}

func TestPlanSkipsNodeWithUntoleratedTaint(t *testing.T) {
	nodes := []NodeInfo{
		{Name: "a"},
		{Name: "b", Taints: []corev1.Taint{
			{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule},
		}},
	}

	got, err := Plan(specWithSelector(nil, nodejobv1alpha1.TriggerOncePerNode), nodes, nil, testHash)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(got.Create) != 1 || got.Create[0].NodeName != "a" {
		t.Errorf("create = %v, want only the untainted node a", got.Create)
	}
}

func TestPlanRunsOnTaintedNodeWhenTolerated(t *testing.T) {
	nodes := []NodeInfo{{Name: "b", Taints: []corev1.Taint{
		{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule},
	}}}
	spec := specWithSelector(nil, nodejobv1alpha1.TriggerOncePerNode)
	spec.JobTemplate.Spec.Template.Spec.Tolerations = []corev1.Toleration{{
		Key:      "dedicated",
		Operator: corev1.TolerationOpEqual,
		Value:    "gpu",
		Effect:   corev1.TaintEffectNoSchedule,
	}}

	got, err := Plan(spec, nodes, nil, testHash)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(got.Create) != 1 {
		t.Errorf("create = %d, want 1 on the tolerated tainted node", len(got.Create))
	}
}

func TestPlanRunsOnNotReadyAndCordonedNodes(t *testing.T) {
	nodes := []NodeInfo{
		{Name: "not-ready", Taints: []corev1.Taint{
			{Key: corev1.TaintNodeNotReady, Effect: corev1.TaintEffectNoExecute},
		}},
		{Name: "cordoned", Taints: []corev1.Taint{
			{Key: corev1.TaintNodeUnschedulable, Effect: corev1.TaintEffectNoSchedule},
		}},
		{Name: "disk-pressure", Taints: []corev1.Taint{
			{Key: corev1.TaintNodeDiskPressure, Effect: corev1.TaintEffectNoSchedule},
		}},
	}

	got, err := Plan(specWithSelector(nil, nodejobv1alpha1.TriggerOncePerNode), nodes, nil, testHash)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(got.Create) != 3 {
		t.Errorf("create = %d, want 3: node-condition taints are tolerated implicitly", len(got.Create))
	}
}

func TestPlanSkipsNodeWithoutBootIDUnderOncePerBoot(t *testing.T) {
	nodes := []NodeInfo{{Name: "a", UID: "uid-a"}}
	got, err := Plan(specWithSelector(nil, nodejobv1alpha1.TriggerOncePerBoot), nodes, nil, testHash)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(got.Create) != 0 {
		t.Errorf("create = %d, want 0 while the node has not reported a bootID", len(got.Create))
	}
}

func TestPlanRerunsWhenNodeIsRecreatedUnderSameName(t *testing.T) {
	spec := specWithSelector(nil, nodejobv1alpha1.TriggerOncePerNode)
	before := RunHash(testHash, "uid-old")
	nodes := []NodeInfo{{Name: "a", UID: "uid-new"}}
	existing := []batchv1.Job{existingJob("a", before)}

	got, err := Plan(spec, nodes, existing, testHash)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(got.Delete) != 1 {
		t.Errorf("delete = %d, want 1: the Job belongs to the previous Node object", len(got.Delete))
	}
}

func TestPlanKeepsJobWhenNodeObjectIsUnchanged(t *testing.T) {
	spec := specWithSelector(nil, nodejobv1alpha1.TriggerOncePerNode)
	nodes := []NodeInfo{{Name: "a", UID: "uid-a"}}
	existing := []batchv1.Job{existingJob("a", RunHash(testHash, "uid-a"))}

	got, _ := Plan(spec, nodes, existing, testHash)
	if len(got.Delete) != 0 || len(got.Create) != 0 {
		t.Errorf("delete=%d create=%d, want 0 and 0", len(got.Delete), len(got.Create))
	}
}
