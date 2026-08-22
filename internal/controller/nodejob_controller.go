package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	nodejobv1alpha1 "github.com/tkukushkin/nodejob-controller/api/v1alpha1"
	"github.com/tkukushkin/nodejob-controller/internal/nodejob"
)

const jobOwnerKey = ".metadata.controller"

type NodeJobReconciler struct {
	client.Client
	// APIReader bypasses the cache, whose Job informer only holds Jobs carrying LabelNodeJob.
	APIReader client.Reader
}

// IndexJobOwner is exported so that tests can register the same index on the fake client
// that the manager registers on its cache.
func IndexJobOwner(obj client.Object) []string {
	owner := metav1.GetControllerOf(obj)
	if owner == nil || owner.Kind != "NodeJob" || owner.APIVersion != nodejobv1alpha1.GroupVersion.String() {
		return nil
	}
	return []string{owner.Name}
}

// +kubebuilder:rbac:groups=tkukushkin.github.io,resources=nodejobs,verbs=get;list;watch
// +kubebuilder:rbac:groups=tkukushkin.github.io,resources=nodejobs/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

func (r *NodeJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var nj nodejobv1alpha1.NodeJob
	if err := r.Get(ctx, req.NamespacedName, &nj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !nj.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	templateHash, err := nodejob.TemplateHash(nj.Spec.JobTemplate)
	if err != nil {
		return ctrl.Result{}, err
	}

	var nodeList corev1.NodeList
	if err := r.List(ctx, &nodeList); err != nil {
		return ctrl.Result{}, err
	}

	var jobList batchv1.JobList
	if err := r.List(ctx, &jobList,
		client.InNamespace(nj.Namespace),
		client.MatchingFields{jobOwnerKey: nj.Name},
	); err != nil {
		return ctrl.Result{}, err
	}

	plan, err := nodejob.Plan(nj.Spec, nodeInfos(nodeList.Items), jobList.Items, templateHash)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.deleteJobs(ctx, plan.Delete); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, r.createJobs(ctx, &nj, plan.Create)
}

func (r *NodeJobReconciler) deleteJobs(ctx context.Context, jobs []batchv1.Job) error {
	logger := log.FromContext(ctx)
	for i := range jobs {
		job := &jobs[i]
		// Foreground: the Job object survives until its pods are gone, which is what the
		// "no replacement while the stale Job is there" rule in Plan relies on.
		err := r.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationForeground))
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return err
		}
		logger.Info("deleted stale job", "job", job.Name, "node", job.Spec.Template.Spec.NodeName)
	}
	return nil
}

func (r *NodeJobReconciler) createJobs(ctx context.Context, nj *nodejobv1alpha1.NodeJob, creations []nodejob.Creation) error {
	logger := log.FromContext(ctx)
	for _, creation := range creations {
		job := nodejob.BuildJob(nj, creation.NodeName, creation.RunHash)
		if err := controllerutil.SetControllerReference(nj, job, r.Scheme()); err != nil {
			return err
		}
		// Names are deterministic, so AlreadyExists on our own Job just means a stale cache.
		// On someone else's Job it is a name collision, and swallowing it would leave this
		// NodeJob silently re-planning the same create forever.
		if err := r.Create(ctx, job); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return err
			}
			var existing batchv1.Job
			if getErr := r.APIReader.Get(ctx, client.ObjectKeyFromObject(job), &existing); getErr != nil {
				return fmt.Errorf("job %s already exists but could not be read back: %w", job.Name, getErr)
			}
			owner := metav1.GetControllerOf(&existing)
			if owner == nil || owner.UID != nj.UID {
				return fmt.Errorf(
					"job %s already exists and is not controlled by NodeJob %s (uid %s): name collision",
					job.Name, nj.Name, nj.UID,
				)
			}
			continue
		}
		logger.Info("created job", "job", job.Name, "node", creation.NodeName)
	}
	return nil
}

func nodeInfos(nodes []corev1.Node) []nodejob.NodeInfo {
	out := make([]nodejob.NodeInfo, 0, len(nodes))
	for i := range nodes {
		out = append(out, nodejob.NodeInfo{
			Name:   nodes[i].Name,
			BootID: nodes[i].Status.NodeInfo.BootID,
			Labels: nodes[i].Labels,
			Taints: nodes[i].Spec.Taints,
		})
	}
	return out
}

func (r *NodeJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(), &batchv1.Job{}, jobOwnerKey, IndexJobOwner,
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&nodejobv1alpha1.NodeJob{}).
		Owns(&batchv1.Job{}).
		Watches(
			&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(r.nodeJobsForNode),
			builder.WithPredicates(nodeRelevantChange()),
		).
		Named("nodejob").
		Complete(r)
}

// nodeJobsForNode enqueues every NodeJob; matching against the changed labels is the
// reconciler's job, not the mapper's.
func (r *NodeJobReconciler) nodeJobsForNode(ctx context.Context, _ client.Object) []ctrl.Request {
	var list nodejobv1alpha1.NodeJobList
	if err := r.List(ctx, &list); err != nil {
		log.FromContext(ctx).Error(err, "list NodeJobs for node event")
		return nil
	}

	requests := make([]ctrl.Request, 0, len(list.Items))
	for i := range list.Items {
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      list.Items[i].Name,
				Namespace: list.Items[i].Namespace,
			},
		})
	}
	return requests
}

// Nodes update their status every ~10s, so without this filter a 500-node cluster drives
// ~50 reconciles per second for nothing.
func nodeRelevantChange() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNode, oldOK := e.ObjectOld.(*corev1.Node)
			newNode, newOK := e.ObjectNew.(*corev1.Node)
			if !oldOK || !newOK {
				return true
			}
			return !maps.Equal(oldNode.Labels, newNode.Labels) ||
				oldNode.Status.NodeInfo.BootID != newNode.Status.NodeInfo.BootID ||
				!slices.EqualFunc(oldNode.Spec.Taints, newNode.Spec.Taints, sameTaint)
		},
	}
}

// TimeAdded is ignored: it moves when a NoExecute taint is re-applied without the taint
// itself changing, and it has no bearing on which nodes match.
func sameTaint(a, b corev1.Taint) bool {
	return a.Key == b.Key && a.Value == b.Value && a.Effect == b.Effect
}
