package suss

import (
	"context"
	"fmt"
	"time"

	"github.com/gprossliner/xhdl"
	"github.com/world-direct/kmutex"
	"github.com/world-direct/looper"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"

	policyv1 "k8s.io/api/policy/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

type SussOptions struct {
	NodeName                     string
	LeaseNamespace               string
	ConsiderStatefulSetCritical  bool
	ConsiderSoleReplicasCritical bool
	K8s                          kubernetes.Interface
}

type service struct {
	km kmutex.Kmutex
	SussOptions
}

type Service interface {
	Start(ctx xhdl.Context)
	Synchronize(ctx xhdl.Context)
	Teardown(ctx xhdl.Context)
	Release(ctx xhdl.Context)
	ReleaseDelayed(ctx xhdl.Context)
	GetCriticalPods(ctx xhdl.Context)
}

type CallContext interface {

	// Logs info to log and client-stream as text/plain & Transfer-Encoding: chunked
	Infof(format string, args ...interface{})
}

const (
	labelPrefix         = "suss.world-direct.at/"
	labelDelayedRelease = labelPrefix + "delayedrelease"
	labelLastRelease    = labelPrefix + "lastrelease"
	labelCriticalPod    = labelPrefix + "critical"
)

func NewService(options SussOptions) Service {

	// init struct
	srv := service{
		SussOptions: options,
		km: kmutex.Kmutex{
			LeaseName:      "sync", // if we may have multiple groups in the future we can use different names
			LeaseNamespace: options.LeaseNamespace,
			HolderIdentity: options.NodeName,
			Clientset:      options.K8s,
			RetryInterval:  time.Second,
		},
	}

	return srv
}

func (srv service) Start(ctx xhdl.Context) {

	// get our node to test the connection and validate the argument
	own := srv.getNodeSet(ctx).OwnNode()
	infof(ctx, "node %s found\n", own.Name())

	// check for delayed release
	if own.GetLabel(ctx, labelDelayedRelease) == "true" {
		infof(ctx, "node marked for delayed release, releasing lock now")
		srv.Release(ctx)

		own.SetLabel(ctx, labelDelayedRelease, "")
	}
}

func infof(ctx context.Context, format string, args ...interface{}) {
	klog.FromContext(ctx).Info(fmt.Sprintf(format, args...))
}

func (srv service) Synchronize(ctx xhdl.Context) {

	looper.Loop(ctx, time.Second*10, func(ctx xhdl.Context) (exit bool) {
		return srv.trySynchronize(ctx)
	})

}

func (srv service) Teardown(ctx xhdl.Context) {

	ns := srv.getNodeSet(ctx)
	own := ns.OwnNode()

	// condon
	own.Cordoned(ctx, true)

	// get pods with critical label
	criticalPods := own.CriticalPods(ctx)
	for _, pod := range criticalPods {
		infof(ctx, "Evict Pod %s/%s", pod.Namespace, pod.Name)
		srv.apiEvictPod(ctx, pod.Namespace, pod.Name)
	}

	// TODO: should loop until no critical pods found

}

func (srv service) Release(ctx xhdl.Context) {

	// validate lock ownership
	owner := srv.km.CurrentOwner(ctx)

	if owner != srv.km.HolderIdentity {
		ctx.Throw(fmt.Errorf("unable to release lock not held, owned by %s", owner))
	}

	// and release lock
	srv.km.Release(ctx)
	infof(ctx, "lock released")

	// set lastRelease info label
	ns := srv.getNodeSet(ctx)
	own := ns.OwnNode()
	own.SetLabel(ctx, labelLastRelease, getTSValue())

	// uncordon
	infof(ctx, "node uncordoned")
	own.Cordoned(ctx, false)

}

func (srv service) ReleaseDelayed(ctx xhdl.Context) {

	ns := srv.getNodeSet(ctx)
	own := ns.OwnNode()

	own.SetLabel(ctx, labelDelayedRelease, "true")

}

func getTSValue() string {
	return fmt.Sprintf("%v", time.Now().Unix())
}

func (srv service) apiEvictPod(ctx xhdl.Context, ns string, name string) {

	err := srv.K8s.PolicyV1().Evictions(ns).Evict(ctx, &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
	})

	if errors.IsNotFound(err) {
		infof(ctx, "Evict failed with NotFound, Pod aready gone %s/%s", ns, name)
		return
	}

	// TODO: we need to make furter tests about possible error codes
	// I didn't manage to produce any of the documented errors, but this may
	// need special pods like some that will need more time to terminate
	ctx.Throw(err)

}

// trys sync one time, returns true if succesful
func (srv service) trySynchronize(ctx xhdl.Context) bool {

	// this is for information only to notify about existing owner
	// if is not race-free if owned by another node (this is done in TryAcquire),
	// but safe it owned by us
	owner := srv.km.CurrentOwner(ctx)
	if owner == srv.km.HolderIdentity {
		infof(ctx, "lock already owned by us %s", owner)
		return true
	}

	if !srv.km.TryAcquire(ctx) {
		infof(ctx, "could not aquire Lease, currently owned by %s", srv.km.CurrentOwner(ctx))
		return false
	} else {
		infof(ctx, "lease successfully aquired by %s", srv.km.HolderIdentity)
		return true
	}
}

func (srv service) GetCriticalPods(ctx xhdl.Context) {

	// .CriticalPods logs with cc, so we do not need to return something
	srv.getNodeSet(ctx).OwnNode().CriticalPods(ctx)
}

func (n Node) CriticalPods(ctx xhdl.Context) []v1.Pod {
	listOpts := metav1.ListOptions{}
	listOpts.FieldSelector = fmt.Sprintf("spec.nodeName=%s,status.phase=Running", n.Name())

	pods, err := n.srv.K8s.CoreV1().Pods(metav1.NamespaceAll).List(ctx, listOpts)
	ctx.Throw(err)

	var lst []v1.Pod
	for _, pod := range pods.Items {
		if n.srv.isPodCritical(ctx, &pod) {
			lst = append(lst, pod)
		}
	}
	return lst
}

func (srv service) isPodCritical(ctx xhdl.Context, pod *v1.Pod) bool {

	// check if pod has the critical label explicitly set to true
	if pod.Labels[labelCriticalPod] == "true" {
		infof(ctx, "Pod %s/%s is critical %s", pod.Namespace, pod.Name, labelCriticalPod)
		return true
	}

	// check if pod has the critical label explicitly set to false
	if pod.Labels[labelCriticalPod] == "false" {
		infof(ctx, "Pod %s/%s is explicitly not critical", pod.Namespace, pod.Name)
		return true
	}

	// check if pod has critical owner
	for _, or := range pod.OwnerReferences {
		if srv.isCriticalOwner(ctx, or, pod) {
			return true
		}
	}

	return false
}

func (srv service) isCriticalOwner(ctx xhdl.Context, or metav1.OwnerReference, pod *v1.Pod) bool {

	// check StatefulSet
	if srv.ConsiderStatefulSetCritical && or.APIVersion == appsv1.SchemeGroupVersion.Identifier() && or.Kind == "StatefulSet" {
		infof(ctx, "Pod %s/%s is critical (Statefulset)", pod.Namespace, pod.Name)
		return true
	}

	// check ReplicaSet (for Deployments)
	if srv.ConsiderSoleReplicasCritical && or.APIVersion == appsv1.SchemeGroupVersion.Identifier() && or.Kind == "ReplicaSet" {

		// with only one replica
		rs, err := srv.K8s.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, or.Name, metav1.GetOptions{})
		ctx.Throw(err)

		if rs.Status.Replicas == 1 {
			infof(ctx, "Pod %s/%s is critical (only one replica)", pod.Namespace, pod.Name)
			return true
		} else {
			return false
		}
	}

	return false
}

type NodeSet struct {
	nodes []Node
	srv   service
}

type Node struct {
	node *v1.Node
	srv  service
}

func (n Node) Name() string {
	return n.node.Name
}

func (srv service) getNodeSet(ctx xhdl.Context) NodeSet {
	nodes, err := srv.K8s.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	ctx.Throw(err)

	var s []Node
	for i := range nodes.Items {
		np := &nodes.Items[i]
		s = append(s, Node{np, srv})
	}

	return NodeSet{s, srv}
}

func (ns NodeSet) OwnNode() Node {
	return ns.GetNode(ns.srv.NodeName)
}

func (ns NodeSet) GetNode(name string) Node {
	for _, node := range ns.nodes {
		if node.Name() == name {
			return node
		}
	}

	panic("node no longer available, can't continue")
}

// sets the Label for a node. If value is am empty string the label is deleted
func (n *Node) SetLabel(ctx xhdl.Context, name, value string) {
	valuestr := "null"
	if value != "" {
		valuestr = fmt.Sprintf(`"%s"`, value)
	}

	patch := fmt.Sprintf(`{"metadata":{"labels":{"%s":%s}}}`, name, valuestr)

	nobj := n.node
	nn, err := n.srv.K8s.CoreV1().Nodes().Patch(ctx, nobj.Name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	ctx.Throw(err)

	n.node = nn
}

func (n Node) GetLabel(ctx xhdl.Context, name string) string {
	return n.node.Labels[name]
}

// Cordoned marks the node as schedulable
func (n *Node) Cordoned(ctx xhdl.Context, value bool) {
	valuestr := "false"
	if value {
		valuestr = "true"
	}
	patch := fmt.Sprintf(`{"spec":{"unschedulable":%s}}`, valuestr)

	nobj := n.node
	nn, err := n.srv.K8s.CoreV1().Nodes().Patch(ctx, nobj.Name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	ctx.Throw(err)

	n.node = nn
}
