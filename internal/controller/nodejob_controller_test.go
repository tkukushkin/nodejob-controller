package controller

import (
	"context"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	nodejobv1alpha1 "github.com/tkukushkin/nodejob-controller/api/v1alpha1"
	"github.com/tkukushkin/nodejob-controller/internal/nodejob"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := nodejobv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add nodejob scheme: %v", err)
	}
	return s
}

func newReconciler(t *testing.T, objs ...client.Object) *NodeJobReconciler {
	t.Helper()
	s := testScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithIndex(&batchv1.Job{}, jobOwnerKey, IndexJobOwner).
		WithObjects(objs...).
		Build()
	return &NodeJobReconciler{Client: c, APIReader: c}
}

func testNodeJob() *nodejobv1alpha1.NodeJob {
	return &nodejobv1alpha1.NodeJob{
		ObjectMeta: metav1.ObjectMeta{Name: "fix", Namespace: "default"},
		Spec: nodejobv1alpha1.NodeJobSpec{
			Trigger: nodejobv1alpha1.TriggerOncePerNode,
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers:    []corev1.Container{{Name: "fix", Image: "busybox:1"}},
						},
					},
				},
			},
		},
	}
}

func testNode(name string, labelsMap map[string]string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labelsMap}}
}

func reconcile(t *testing.T, r *NodeJobReconciler) {
	t.Helper()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "fix", Namespace: "default"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

func listJobs(t *testing.T, r *NodeJobReconciler) []batchv1.Job {
	t.Helper()
	var jobs batchv1.JobList
	if err := r.List(context.Background(), &jobs, client.InNamespace("default")); err != nil {
		t.Fatalf("List: %v", err)
	}
	return jobs.Items
}

func TestReconcileCreatesJobPerNode(t *testing.T) {
	r := newReconciler(t, testNodeJob(), testNode("node-a", nil), testNode("node-b", nil))
	reconcile(t, r)

	jobs := listJobs(t, r)
	if len(jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(jobs))
	}
	for _, job := range jobs {
		if job.Spec.Template.Spec.NodeName == "" {
			t.Errorf("job %s has no nodeName", job.Name)
		}
		if len(job.OwnerReferences) != 1 || job.OwnerReferences[0].Kind != "NodeJob" {
			t.Errorf("job %s has no NodeJob owner reference", job.Name)
		}
		if len(job.OwnerReferences) != 1 || job.OwnerReferences[0].Controller == nil || !*job.OwnerReferences[0].Controller {
			t.Errorf("job %s owner reference is not a controller reference", job.Name)
		}
		if job.Labels[nodejob.LabelNodeJob] != "fix" {
			t.Errorf("job %s missing contract label", job.Name)
		}
	}
}

// Before JobName included the NodeJob name in its digest, both Jobs here got a
// byte-identical name and the second reconcile silently lost to AlreadyExists.
func TestReconcileTwoNodeJobsWithSharedNamePrefixEachGetOwnJob(t *testing.T) {
	njA := testNodeJob()
	njA.Name = "fix-sysctl-somaxconn-zone-a"
	njA.UID = "uid-a"

	njB := testNodeJob()
	njB.Name = "fix-sysctl-somaxconn-zone-b"
	njB.UID = "uid-b"

	r := newReconciler(t, njA, njB, testNode("node-a", nil))

	for _, name := range []string{njA.Name, njB.Name} {
		req := ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: "default"}}
		if _, err := r.Reconcile(context.Background(), req); err != nil {
			t.Fatalf("Reconcile(%s): %v", name, err)
		}
	}

	jobs := listJobs(t, r)
	if len(jobs) != 2 {
		t.Fatalf("jobs = %d, want 2 (one per NodeJob)", len(jobs))
	}
	if jobs[0].Name == jobs[1].Name {
		t.Fatalf("both NodeJobs produced the same job name %q", jobs[0].Name)
	}
	seenOwners := map[types.UID]bool{}
	for _, job := range jobs {
		owner := metav1.GetControllerOf(&job)
		if owner == nil {
			t.Fatalf("job %s has no controller owner reference", job.Name)
		}
		seenOwners[owner.UID] = true
	}
	if !seenOwners[njA.UID] || !seenOwners[njB.UID] {
		t.Fatalf("jobs are not owned one each by njA/njB: owners seen = %v", seenOwners)
	}
}

func TestReconcileFailsLoudlyOnForeignJobNameCollision(t *testing.T) {
	nj := testNodeJob()
	nj.UID = "uid-owner"

	templateHash, err := nodejob.TemplateHash(nj.Spec.JobTemplate)
	if err != nil {
		t.Fatalf("TemplateHash: %v", err)
	}
	runHash := nodejob.RunHash(templateHash, "")
	foreignName := nodejob.JobName(nj.Name, "node-a", runHash)

	foreignJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      foreignName,
			Namespace: "default",
			// No ownerReference to nj: someone else's Job that landed on the same name.
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					NodeName:      "node-a",
					Containers:    []corev1.Container{{Name: "fix", Image: "busybox:1"}},
				},
			},
		},
	}

	r := newReconciler(t, nj, testNode("node-a", nil), foreignJob)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: nj.Name, Namespace: "default"}}
	if _, err := r.Reconcile(context.Background(), req); err == nil {
		t.Fatal("Reconcile succeeded, want an error: existing job is not owned by this NodeJob")
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	r := newReconciler(t, testNodeJob(), testNode("node-a", nil))
	reconcile(t, r)
	reconcile(t, r)

	if jobs := listJobs(t, r); len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
}

func TestReconcileDeletesJobWhenNodeIsGone(t *testing.T) {
	node := testNode("node-a", nil)
	r := newReconciler(t, testNodeJob(), node)
	reconcile(t, r)

	if err := r.Delete(context.Background(), node); err != nil {
		t.Fatalf("delete node: %v", err)
	}
	reconcile(t, r)

	if jobs := listJobs(t, r); len(jobs) != 0 {
		t.Fatalf("jobs = %d, want 0", len(jobs))
	}
}

func TestReconcileDeletesJobWhenNodeStopsMatching(t *testing.T) {
	nj := testNodeJob()
	nj.Spec.JobTemplate.Spec.Template.Spec.NodeSelector = map[string]string{"role": "worker"}
	node := testNode("node-a", map[string]string{"role": "worker"})
	r := newReconciler(t, nj, node)
	reconcile(t, r)

	node.Labels = map[string]string{"role": "master"}
	if err := r.Update(context.Background(), node); err != nil {
		t.Fatalf("update node: %v", err)
	}
	reconcile(t, r)

	if jobs := listJobs(t, r); len(jobs) != 0 {
		t.Fatalf("jobs = %d, want 0", len(jobs))
	}
}

func TestReconcileReplacesJobInTwoPassesWhenTemplateChanges(t *testing.T) {
	r := newReconciler(t, testNodeJob(), testNode("node-a", nil))
	reconcile(t, r)
	oldName := listJobs(t, r)[0].Name

	var stored nodejobv1alpha1.NodeJob
	key := types.NamespacedName{Name: "fix", Namespace: "default"}
	if err := r.Get(context.Background(), key, &stored); err != nil {
		t.Fatalf("get nodejob: %v", err)
	}
	stored.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Image = "busybox:2"
	if err := r.Update(context.Background(), &stored); err != nil {
		t.Fatalf("update nodejob: %v", err)
	}

	reconcile(t, r)
	if jobs := listJobs(t, r); len(jobs) != 0 {
		t.Fatalf("after first pass jobs = %d, want 0: stale must go before the replacement appears", len(jobs))
	}

	reconcile(t, r)
	jobs := listJobs(t, r)
	if len(jobs) != 1 {
		t.Fatalf("after second pass jobs = %d, want 1", len(jobs))
	}
	if jobs[0].Name == oldName {
		t.Error("job name unchanged, want a new hash in the name")
	}
}

// Without this test, reading the wrong NodeSystemInfo field would pass the whole suite.
func TestReconcileReplacesJobOnBootIDChangeWithOncePerBoot(t *testing.T) {
	nj := testNodeJob()
	nj.Spec.Trigger = nodejobv1alpha1.TriggerOncePerBoot
	node := nodeWith(nil, "boot-1", true)

	r := newReconciler(t, nj, node)
	reconcile(t, r)
	oldName := listJobs(t, r)[0].Name

	node.Status.NodeInfo.BootID = "boot-2"
	// Node.Status is a subresource, in the fake client too: a plain Update leaves it alone.
	if err := r.Status().Update(context.Background(), node); err != nil {
		t.Fatalf("update node status: %v", err)
	}

	reconcile(t, r)
	if jobs := listJobs(t, r); len(jobs) != 0 {
		t.Fatalf("after first pass jobs = %d, want 0: stale job from the old boot must go first", len(jobs))
	}

	reconcile(t, r)
	jobs := listJobs(t, r)
	if len(jobs) != 1 {
		t.Fatalf("after second pass jobs = %d, want 1", len(jobs))
	}
	if jobs[0].Name == oldName {
		t.Error("job name unchanged after bootID change, want a new run hash in the name")
	}
}

func TestReconcileMissingNodeJobIsNotAnError(t *testing.T) {
	reconcile(t, newReconciler(t))
}

func nodeWith(labelsMap map[string]string, bootID string, ready bool) *corev1.Node {
	condition := corev1.ConditionFalse
	if ready {
		condition = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a", Labels: labelsMap},
		Status: corev1.NodeStatus{
			NodeInfo:   corev1.NodeSystemInfo{BootID: bootID},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: condition}},
		},
	}
}

func TestNodePredicateIgnoresStatusOnlyChange(t *testing.T) {
	p := nodeRelevantChange()
	old := nodeWith(map[string]string{"role": "worker"}, "boot-1", true)
	updated := nodeWith(map[string]string{"role": "worker"}, "boot-1", false)

	if p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}) {
		t.Error("status-only change passed the predicate, want it filtered out")
	}
}

func TestNodePredicatePassesLabelChange(t *testing.T) {
	p := nodeRelevantChange()
	old := nodeWith(map[string]string{"role": "worker"}, "boot-1", true)
	updated := nodeWith(map[string]string{"role": "master"}, "boot-1", true)

	if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}) {
		t.Error("label change filtered out, want it to pass")
	}
}

func TestNodePredicatePassesBootIDChange(t *testing.T) {
	p := nodeRelevantChange()
	if !p.Update(event.UpdateEvent{
		ObjectOld: nodeWith(nil, "boot-1", true),
		ObjectNew: nodeWith(nil, "boot-2", true),
	}) {
		t.Error("bootID change filtered out, want it to pass")
	}
}

func TestNodePredicatePassesCreateAndDelete(t *testing.T) {
	p := nodeRelevantChange()
	if !p.Create(event.CreateEvent{Object: nodeWith(nil, "boot-1", true)}) {
		t.Error("create filtered out, want it to pass")
	}
	if !p.Delete(event.DeleteEvent{Object: nodeWith(nil, "boot-1", true)}) {
		t.Error("delete filtered out, want it to pass")
	}
}

func TestNodePredicatePassesTaintChange(t *testing.T) {
	p := nodeRelevantChange()
	old := nodeWith(nil, "boot-1", true)
	updated := nodeWith(nil, "boot-1", true)
	updated.Spec.Taints = []corev1.Taint{
		{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule},
	}

	if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}) {
		t.Error("taint change filtered out, want it to pass")
	}
}

func TestNodePredicateIgnoresTaintTimeAdded(t *testing.T) {
	taint := func(added *metav1.Time) []corev1.Taint {
		return []corev1.Taint{{
			Key:       corev1.TaintNodeNotReady,
			Effect:    corev1.TaintEffectNoExecute,
			TimeAdded: added,
		}}
	}
	old := nodeWith(nil, "boot-1", true)
	old.Spec.Taints = taint(&metav1.Time{})
	updated := nodeWith(nil, "boot-1", true)
	updated.Spec.Taints = taint(&metav1.Time{Time: time.Unix(1, 0)})

	if nodeRelevantChange().Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}) {
		t.Error("a moved TimeAdded passed the predicate, want it filtered out")
	}
}
